package supportforge

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge/store"
)

const (
	backoffBase = 30 * time.Second
	backoffCap  = time.Hour
	batchSize   = 20
)

// Run calls [Service.Deliver] every interval until ctx is done, passing any
// error from a pass to onError, which may be nil. Run returns nil when ctx
// is done.
func (s *Service) Run(ctx context.Context, interval time.Duration, onError func(error)) error {
	if interval <= 0 {
		return errors.New("supportforge: Run needs a positive interval")
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.Deliver(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Deliver makes one pass: it claims due outbox entries and tries each at
// its current desk. It returns how many entries it handled, and the errors
// of any it could not handle, which stay in the outbox for a later pass.
func (s *Service) Deliver(ctx context.Context) (int, error) {
	entries, err := s.store.Claim(ctx, s.now(), s.lease, batchSize)
	if err != nil {
		return 0, fmt.Errorf("supportforge: claiming deliveries: %w", err)
	}
	var errs []error
	for _, e := range entries {
		if err := s.deliverOne(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("entry %s: %w", e.ID, err))
		}
	}
	return len(entries), errors.Join(errs...)
}

func (s *Service) deliverOne(ctx context.Context, e store.Entry) error {
	c, err := s.store.Case(ctx, e.CaseID)
	if err != nil {
		return err
	}
	e.Desk = min(e.Desk, len(s.desks)-1)
	desk := s.desks[e.Desk]

	ack, err := s.templates.Render(c.Case)
	if err != nil {
		return err
	}
	private, err := s.responderContext(ctx, c)
	if err != nil {
		return err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, s.lease/2)
	d, deliverErr := desk.Deliver(attemptCtx, c.Case, ack, private)
	cancel()

	// Record the outcome even if ctx was canceled during the attempt, so a
	// shutdown does not leave a delivered case in the outbox.
	ctx = context.WithoutCancel(ctx)
	if deliverErr == nil {
		if d.Desk == "" {
			d.Desk = desk.Name()
		}
		if err := s.store.Delivered(ctx, e.ID, d, s.now()); err != nil {
			return fmt.Errorf("recording delivery to %s: %w", desk.Name(), err)
		}
		ev := event(EventDelivered, c.Case)
		ev.Desk = desk.Name()
		s.observer.Observe(ctx, ev)
		s.storeLink(ctx, c, desk, d)
		return nil
	}
	return s.reschedule(ctx, c, e, deliverErr)
}

// reschedule decides what happens after a failed attempt.
//
//   - Retryable or unclassified: try the same desk again after a backoff,
//     until MaxAttempts, then the next desk.
//   - Rejected or unsupported: the next desk at once.
//   - Credential: alert, then the next desk at once.
//
// The last desk never gives up: it is retried with backoff, so a case is
// never dropped.
func (s *Service) reschedule(ctx context.Context, c store.Case, e store.Entry, cause error) error {
	class := supportcore.Classify(cause)
	last := e.Desk >= len(s.desks)-1
	desk := s.desks[e.Desk].Name()
	e.Attempts++
	e.LastError = className(class)

	ev := event(EventDeliveryFailed, c.Case)
	ev.Desk, ev.Class, ev.Attempts, ev.LastDesk = desk, e.LastError, e.Attempts, last
	if class == supportcore.ErrCredential {
		ev.Type = EventCredentialFailed
	}
	s.observer.Observe(ctx, ev)

	now := s.now()
	moveOn := !last && (class == supportcore.ErrRejected || class == supportcore.ErrUnsupported ||
		class == supportcore.ErrCredential || e.Attempts >= s.maxAttempts)
	if moveOn {
		e.Desk++
		e.Attempts = 0
		e.NextAttemptAt = now
	} else {
		e.NextAttemptAt = now.Add(backoff(e.Attempts, cause))
	}
	if err := s.store.Reschedule(ctx, e); err != nil {
		return fmt.Errorf("rescheduling after %s failed: %w", desk, err)
	}
	return nil
}

// backoff is exponential with jitter, capped at an hour. A desk's
// retry-after wins when it is longer.
func backoff(attempts int, cause error) time.Duration {
	d := backoffBase << min(attempts-1, 10)
	d = min(d, backoffCap)
	d = d/2 + rand.N(d/2+1) //nolint:gosec // jitter, not security
	if after, ok := supportcore.RetryAfter(cause); ok && after > d {
		d = min(after, backoffCap)
	}
	return d
}

func className(class error) string {
	switch class {
	case supportcore.ErrRetryable:
		return "retryable"
	case supportcore.ErrCredential:
		return "credential"
	case supportcore.ErrRejected:
		return "rejected"
	case supportcore.ErrUnsupported:
		return "unsupported"
	}
	return "unclassified"
}

// responderContext is what responders see: the case's own facts, then
// anything the host adds. The host cannot override the case's facts.
func (s *Service) responderContext(ctx context.Context, c store.Case) (supportcore.Context, error) {
	out := supportcore.Context{}
	if s.context != nil {
		extra, err := s.context.ResponderContext(ctx, c.Case)
		if err != nil {
			return nil, fmt.Errorf("responder context: %w", err)
		}
		for k, v := range extra {
			out[k] = v
		}
	}
	facts := map[string]string{
		"reference":    c.Reference,
		"product":      c.Product,
		"kind":         string(c.Kind),
		"page":         c.Page,
		"principal":    c.Requester.PrincipalID,
		"organization": c.Requester.OrganizationID,
		"shared":       string(c.ShareReason),
	}
	for k, v := range facts {
		if v != "" {
			out[k] = v
		} else {
			delete(out, k)
		}
	}
	if c.Requester.Visitor() {
		out["requester"] = "visitor"
	} else {
		out["requester"] = "signed in"
	}
	return out, nil
}

// storeLink records the desk's page for the requester, if it has one. A
// failure is reported and does not undo the delivery.
func (s *Service) storeLink(ctx context.Context, c store.Case, desk supportcore.Desk, d supportcore.Delivery) {
	linker, ok := desk.(supportcore.TicketLinker)
	if !ok {
		return
	}
	linkCtx, cancel := context.WithTimeout(ctx, s.lease/2)
	defer cancel()
	link, err := linker.TicketURL(linkCtx, d)
	if err == nil && link != "" {
		err = s.store.Update(ctx, c.ID, func(sc *store.Case) error {
			sc.DeskURL = link
			return nil
		})
	}
	if err != nil {
		ev := event(EventDeskFailed, c.Case)
		ev.Desk, ev.Class = desk.Name(), className(supportcore.Classify(err))
		s.observer.Observe(ctx, ev)
	}
}
