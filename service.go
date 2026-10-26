// Package supportforge accepts support requests and feedback, stores them
// before anything is sent, and delivers them through an ordered list of
// desks that ends with mail.
//
// The host resolves who is asking, authorizes every call, and supplies
// storage and the other ports. supportforge reads no sessions and serves no
// HTTP.
package supportforge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge/store"
)

// Errors returned by [Service.Submit] and the lookups. Each wraps further
// detail; test with errors.Is.
var (
	ErrInvalid     = errors.New("supportforge: invalid request")
	ErrChallenge   = errors.New("supportforge: bot challenge failed")
	ErrRateLimited = errors.New("supportforge: rate limited")
	ErrNotFound    = errors.New("supportforge: not found")
)

// ChallengeVerifier checks a visitor's bot challenge token. It returns nil
// when the token is valid.
type ChallengeVerifier interface {
	VerifyChallenge(ctx context.Context, token string) error
}

// RateLimiter decides whether a requester may submit now. It returns nil to
// allow, and an error wrapping [ErrRateLimited] to refuse.
type RateLimiter interface {
	Allow(ctx context.Context, key string) error
}

// SharingPolicy reports whether an organization shares every request from
// its members with its administrators.
type SharingPolicy interface {
	SharesAll(ctx context.Context, organizationID string) (bool, error)
}

// ContextSource adds what responders should see beyond the case itself,
// such as the plan or the organization's name. It is called at delivery and
// its result is not stored.
type ContextSource interface {
	ResponderContext(ctx context.Context, c supportcore.Case) (supportcore.Context, error)
}

// Config configures a [Service]. Store and Desks are required.
type Config struct {
	Store store.Store

	// Desks is the delivery order. The last should be the mail desk, which
	// never runs out of retries.
	Desks []supportcore.Desk

	Templates *Templates        // acknowledgments; nil uses the defaults
	Challenge ChallengeVerifier // required to accept visitors
	Limiter   RateLimiter       // optional
	Sharing   SharingPolicy     // optional
	Context   ContextSource     // optional
	Observer  Observer          // optional

	// MaxAttempts is how many retryable failures a desk gets before the
	// next desk is tried. Default 5.
	MaxAttempts int
	// Lease is how long a worker holds an entry. A delivery attempt gets half
	// of it. Default two minutes.
	Lease time.Duration

	Now    func() time.Time // default time.Now
	Random io.Reader        // for IDs and references; default crypto/rand
}

// Service is the support-request service.
type Service struct {
	store       store.Store
	desks       []supportcore.Desk
	templates   *Templates
	challenge   ChallengeVerifier
	limiter     RateLimiter
	sharing     SharingPolicy
	context     ContextSource
	observer    Observer
	maxAttempts int
	lease       time.Duration
	now         func() time.Time
	ids         *idSource
}

// New returns a service, or an error if cfg is incomplete.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("supportforge: Config.Store is required")
	}
	if len(cfg.Desks) == 0 {
		return nil, errors.New("supportforge: Config.Desks needs at least one desk")
	}
	names := map[string]bool{}
	for i, d := range cfg.Desks {
		if d == nil || d.Name() == "" {
			return nil, fmt.Errorf("supportforge: desk %d has no name", i)
		}
		if names[d.Name()] {
			return nil, fmt.Errorf("supportforge: two desks are named %q", d.Name())
		}
		names[d.Name()] = true
	}
	s := &Service{
		store:       cfg.Store,
		desks:       cfg.Desks,
		templates:   cfg.Templates,
		challenge:   cfg.Challenge,
		limiter:     cfg.Limiter,
		sharing:     cfg.Sharing,
		context:     cfg.Context,
		observer:    cfg.Observer,
		maxAttempts: cfg.MaxAttempts,
		lease:       cfg.Lease,
		now:         cfg.Now,
		ids:         newIDSource(cfg.Random),
	}
	if s.templates == nil {
		s.templates = DefaultTemplates()
	}
	if s.observer == nil {
		s.observer = nopObserver{}
	}
	if s.maxAttempts <= 0 {
		s.maxAttempts = 5
	}
	if s.lease <= 0 {
		s.lease = 2 * time.Minute
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// desk returns the desk with the given name.
func (s *Service) desk(name string) (supportcore.Desk, bool) {
	for _, d := range s.desks {
		if d.Name() == name {
			return d, true
		}
	}
	return nil, false
}
