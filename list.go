package supportforge

import (
	"context"
	"errors"
	"fmt"
	"time"

	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge/store"
)

// ListEntry is one row of a requester's own list.
type ListEntry struct {
	Reference string
	Kind      supportcore.Kind
	Product   string
	Title     string
	Status    store.Status
	DeskURL   string // empty unless the desk has a page for the requester
	CreatedAt time.Time
	UpdatedAt time.Time // the latest of creation, delivery, the last reply and closure
}

// RequesterCases lists a signed-in requester's cases, newest first. The
// host passes the principal from the session. Visitors have no list.
func (s *Service) RequesterCases(ctx context.Context, principalID string, p store.Page) ([]ListEntry, error) {
	if principalID == "" {
		return nil, invalid("principal", "is required")
	}
	cases, err := s.store.ByPrincipal(ctx, principalID, p)
	if err != nil {
		return nil, fmt.Errorf("supportforge: listing cases: %w", err)
	}
	out := make([]ListEntry, len(cases))
	for i, c := range cases {
		out[i] = ListEntry{
			Reference: c.Reference,
			Kind:      c.Kind,
			Product:   c.Product,
			Title:     c.Title,
			Status:    c.Status,
			DeskURL:   c.DeskURL,
			CreatedAt: c.CreatedAt,
			UpdatedAt: latest(c.CreatedAt, c.DeliveredAt, c.LastReplyAt, c.ClosedAt),
		}
	}
	return out, nil
}

// RequesterCase returns one of a requester's cases by reference. It returns
// ErrNotFound when the case belongs to someone else, so a reference cannot
// be used to probe for other people's cases.
func (s *Service) RequesterCase(ctx context.Context, principalID, ref string) (ListEntry, error) {
	c, err := s.store.CaseByReference(ctx, ref)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (principalID == "" || c.Requester.PrincipalID != principalID)) {
		return ListEntry{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	if err != nil {
		return ListEntry{}, fmt.Errorf("supportforge: reading case: %w", err)
	}
	return ListEntry{
		Reference: c.Reference, Kind: c.Kind, Product: c.Product, Title: c.Title, Status: c.Status,
		DeskURL: c.DeskURL, CreatedAt: c.CreatedAt, UpdatedAt: latest(c.CreatedAt, c.DeliveredAt, c.LastReplyAt, c.ClosedAt),
	}, nil
}

// RefreshStatuses asks desks that report status about up to limit open
// cases, least recently checked first, and records the answers. A case the
// desk reports closed gets a closing time; one reopened loses it. Run it
// on a schedule; the requester's list never calls a desk.
func (s *Service) RefreshStatuses(ctx context.Context, limit int) (int, error) {
	var names []string
	for _, d := range s.desks {
		if _, ok := d.(supportcore.TicketStatuser); ok {
			names = append(names, d.Name())
		}
	}
	if len(names) == 0 {
		return 0, nil
	}
	cases, err := s.store.StatusDue(ctx, names, limit)
	if err != nil {
		return 0, fmt.Errorf("supportforge: finding cases to check: %w", err)
	}
	var errs []error
	for _, c := range cases {
		desk, ok := s.desk(c.Delivery.Desk)
		if !ok {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, s.lease/2)
		status, checkErr := desk.(supportcore.TicketStatuser).TicketStatus(checkCtx, c.Delivery)
		cancel()
		now := s.now()
		if checkErr == nil && !status.Valid() {
			checkErr = fmt.Errorf("desk %s reported status %q: %w", desk.Name(), status, supportcore.ErrRejected)
		}
		err := s.store.Update(ctx, c.ID, func(sc *store.Case) error {
			sc.StatusCheckedAt = now
			if checkErr != nil {
				return nil
			}
			sc.Status = store.Status(status)
			switch {
			case status == supportcore.StatusClosed && sc.ClosedAt.IsZero():
				sc.ClosedAt = now
			case status != supportcore.StatusClosed:
				sc.ClosedAt = time.Time{}
			}
			return nil
		})
		if checkErr != nil {
			ev := event(EventDeskFailed, c.Case)
			ev.Desk, ev.Class = desk.Name(), className(supportcore.Classify(checkErr))
			s.observer.Observe(ctx, ev)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("case %s: %w", c.ID, err))
		}
	}
	return len(cases), errors.Join(errs...)
}

func latest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}
