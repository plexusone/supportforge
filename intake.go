package supportforge

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge/store"
)

// Limits on a request's fields.
const (
	MaxTitle   = 200    // characters
	MaxBody    = 10_000 // characters
	MaxProduct = 64
	MaxPage    = 512
	MaxAddress = 254
)

// Request is a support request or feedback as the host received it.
//
// The host fills Requester: from the session for a signed-in user, never
// from the request body, and from the form for a visitor.
type Request struct {
	Product     string
	Kind        supportcore.Kind
	Title       string
	Body        string
	Page        string // the page it came from; reduced to its path
	ReplyWanted bool   // false only for feedback that needs no reply
	Share       bool   // the requester chose to share it with their organization's admins
	Requester   supportcore.Requester

	Challenge string // a visitor's bot challenge token
	RateKey   string // a visitor's rate-limit key from the host, such as a hashed client address
}

// Receipt is what the requester is told: the reference, and whether the
// request is shared and why.
type Receipt struct {
	CaseID      string
	Reference   string
	Shared      bool
	ShareReason store.ShareReason
}

// ValidationError says which field of a request is invalid. It wraps
// [ErrInvalid].
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return "supportforge: invalid " + e.Field + ": " + e.Reason
}

func (e *ValidationError) Unwrap() error { return ErrInvalid }

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

// Submit validates r, checks the challenge and rate limit, and stores the
// case with its outbox entry in one transaction. Nothing is sent before the
// case is stored; the worker delivers it.
func (s *Service) Submit(ctx context.Context, r Request) (Receipt, error) {
	c, err := normalize(r)
	if err != nil {
		return Receipt{}, err
	}
	visitor := c.Requester.Visitor()
	if visitor {
		if s.challenge == nil {
			return Receipt{}, errors.New("supportforge: visitors need a ChallengeVerifier")
		}
		if r.Challenge == "" {
			return Receipt{}, fmt.Errorf("%w: no token", ErrChallenge)
		}
		if err := s.challenge.VerifyChallenge(ctx, r.Challenge); err != nil {
			return Receipt{}, fmt.Errorf("%w: %w", ErrChallenge, err)
		}
	}
	if s.limiter != nil {
		key := "principal:" + c.Requester.PrincipalID
		if visitor {
			key = "visitor:" + r.RateKey
		}
		if err := s.limiter.Allow(ctx, key); err != nil {
			if !errors.Is(err, ErrRateLimited) {
				err = fmt.Errorf("%w: %w", ErrRateLimited, err)
			}
			return Receipt{}, err
		}
	}
	sc := store.Case{Case: c, Status: store.StatusReceived}
	if sc.ShareReason, err = s.shareReason(ctx, c, r.Share); err != nil {
		return Receipt{}, err
	}
	sc.Shared = sc.ShareReason != ""
	sc.CreatedAt = s.now()

	for attempt := 0; ; attempt++ {
		if sc.ID, err = s.ids.id(); err != nil {
			return Receipt{}, err
		}
		if sc.Reference, err = s.ids.reference(); err != nil {
			return Receipt{}, err
		}
		e := store.Entry{CaseID: sc.ID, NextAttemptAt: sc.CreatedAt}
		if e.ID, err = s.ids.id(); err != nil {
			return Receipt{}, err
		}
		err = s.store.Create(ctx, sc, e)
		if err == nil {
			break
		}
		if !errors.Is(err, store.ErrDuplicate) || attempt == 2 {
			return Receipt{}, fmt.Errorf("supportforge: storing the case: %w", err)
		}
	}
	s.observer.Observe(ctx, event(EventCaseCreated, sc.Case))
	return Receipt{CaseID: sc.ID, Reference: sc.Reference, Shared: sc.Shared, ShareReason: sc.ShareReason}, nil
}

// shareReason decides whether the case is shared with the requester's
// organization administrators, and why.
func (s *Service) shareReason(ctx context.Context, c supportcore.Case, chosen bool) (store.ShareReason, error) {
	if c.Requester.OrganizationID == "" {
		return "", nil
	}
	if c.Kind == supportcore.KindBilling {
		return store.ShareBilling, nil
	}
	if s.sharing != nil {
		all, err := s.sharing.SharesAll(ctx, c.Requester.OrganizationID)
		if err != nil {
			return "", fmt.Errorf("supportforge: sharing policy: %w", err)
		}
		if all {
			return store.SharePolicy, nil
		}
	}
	if chosen {
		return store.ShareRequester, nil
	}
	return "", nil
}

// normalize validates r and returns the case it describes, without IDs.
func normalize(r Request) (supportcore.Case, error) {
	c := supportcore.Case{
		Product:     strings.TrimSpace(r.Product),
		Kind:        r.Kind,
		Title:       strings.TrimSpace(r.Title),
		Body:        strings.TrimSpace(r.Body),
		Page:        pagePath(r.Page),
		ReplyWanted: r.ReplyWanted,
		Requester: supportcore.Requester{
			PrincipalID:    strings.TrimSpace(r.Requester.PrincipalID),
			OrganizationID: strings.TrimSpace(r.Requester.OrganizationID),
			ReplyTo:        strings.TrimSpace(r.Requester.ReplyTo),
		},
	}
	if c.Requester.Visitor() {
		c.Requester.OrganizationID = ""
	}
	if !c.Kind.Valid() {
		return c, invalid("kind", fmt.Sprintf("%q is not a kind", r.Kind))
	}
	if err := checkLine("product", c.Product, MaxProduct); err != nil {
		return c, err
	}
	if err := checkLine("title", c.Title, MaxTitle); err != nil {
		return c, err
	}
	if err := checkText("body", c.Body, MaxBody); err != nil {
		return c, err
	}
	if c.Kind != supportcore.KindFeedback {
		c.ReplyWanted = true
	}
	switch {
	case c.Requester.ReplyTo != "":
		if err := checkAddress(c.Requester.ReplyTo); err != nil {
			return c, err
		}
	case c.ReplyWanted:
		return c, invalid("replyTo", "an email address is needed for a reply")
	}
	return c, nil
}

// checkLine checks a required single-line field: no line breaks or other
// control characters, since it may become an email subject.
func checkLine(field, v string, maxLen int) error {
	if err := checkText(field, v, maxLen); err != nil {
		return err
	}
	if strings.ContainsFunc(v, unicode.IsControl) {
		return invalid(field, "must be a single line")
	}
	return nil
}

// checkText checks a required text field: valid UTF-8, within the limit,
// and no control characters other than line breaks and tabs.
func checkText(field, v string, maxLen int) error {
	switch {
	case v == "":
		return invalid(field, "is required")
	case !utf8.ValidString(v):
		return invalid(field, "is not valid text")
	case utf8.RuneCountInString(v) > maxLen:
		return invalid(field, fmt.Sprintf("is longer than %d characters", maxLen))
	case strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }):
		return invalid(field, "contains control characters")
	}
	return nil
}

// checkAddress accepts a bare address such as a@example.com, without a
// display name.
func checkAddress(v string) error {
	if len(v) > MaxAddress {
		return invalid("replyTo", "is too long")
	}
	a, err := mail.ParseAddress(v)
	if err != nil || a.Name != "" || a.Address != v {
		return invalid("replyTo", "is not an email address")
	}
	return nil
}

// pagePath reduces a page to its path, dropping the scheme, host, query
// and fragment, which can carry tokens or personal data. A page that is not
// a path is dropped.
func pagePath(v string) string {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || !strings.HasPrefix(u.Path, "/") || strings.ContainsFunc(u.Path, unicode.IsControl) {
		return ""
	}
	if len(u.Path) > MaxPage {
		return ""
	}
	return u.Path
}

func event(t EventType, c supportcore.Case) Event {
	return Event{Type: t, CaseID: c.ID, Reference: c.Reference, Product: c.Product, Kind: string(c.Kind)}
}
