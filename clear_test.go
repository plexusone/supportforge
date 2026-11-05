package supportforge_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/plexusone/supportforge"
	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge-core/memdesk"
	"github.com/plexusone/supportforge/store"
	"github.com/plexusone/supportforge/store/memstore"
)

const day = 24 * time.Hour

func (h *harness) clear(t *testing.T) supportforge.ClearReport {
	t.Helper()
	r, err := h.svc.Clear(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// deliverAll runs the worker until nothing is left to deliver.
func (h *harness) deliverAll(t *testing.T) {
	t.Helper()
	for {
		n, err := h.svc.Deliver(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

func (h *harness) update(t *testing.T, id string, fn func(*store.Case)) {
	t.Helper()
	if err := h.store.Update(t.Context(), id, func(c *store.Case) error { fn(c); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestClearAfterTheDelay(t *testing.T) {
	h := newHarness(t, []string{"mail"}, nil)
	rc := h.submit(t, signedIn())
	h.deliver(t)
	delivered := h.clock.Now()

	h.clock.Advance(7*day - time.Second)
	if r := h.clear(t); r.Bodies != 0 || r.Addresses != 0 {
		t.Fatalf("Clear before the delay = %+v", r)
	}
	h.clock.Advance(time.Second)
	if r := h.clear(t); r.Bodies != 1 || r.Addresses != 1 {
		t.Fatalf("Clear at the delay = %+v, want the body and the address", r)
	}
	c := h.caseOf(t, rc)
	if c.Body != "" || c.Requester.ReplyTo != "" {
		t.Error("the body or the address is still stored")
	}
	if c.Title != signedIn().Title {
		t.Errorf("title = %q; it stays for the requester's list", c.Title)
	}
	if want := delivered.Add(7 * day); !c.BodyClearedAt.Equal(want) || !c.ReplyToClearedAt.Equal(want) {
		t.Errorf("cleared at %s and %s, want %s", c.BodyClearedAt, c.ReplyToClearedAt, want)
	}
	if r := h.clear(t); r.Bodies != 0 || r.Addresses != 0 {
		t.Errorf("a second Clear = %+v, want nothing", r)
	}
}

func TestClearNeverTouchesUndeliveredCases(t *testing.T) {
	h := newHarness(t, []string{"mail"}, nil)
	rc := h.submit(t, signedIn())
	h.clock.Advance(365 * day)
	if r := h.clear(t); r.Bodies != 0 || r.Addresses != 0 {
		t.Errorf("Clear = %+v, want nothing for an undelivered case", r)
	}
	if c := h.caseOf(t, rc); c.Body == "" || c.Requester.ReplyTo == "" {
		t.Error("an undelivered case lost its text")
	}
}

func TestClearKeepsTheAddressWhileTheConversationContinues(t *testing.T) {
	h := newHarness(t, []string{"posthog"}, nil)
	open := h.submit(t, signedIn())
	replied := h.submit(t, signedIn())
	closed := h.submit(t, signedIn())
	h.deliverAll(t)
	h.update(t, open.CaseID, func(c *store.Case) { c.Status = store.StatusOpen })
	h.update(t, closed.CaseID, func(c *store.Case) { c.Status = store.StatusOpen })

	h.clock.Advance(5 * day)
	h.update(t, replied.CaseID, func(c *store.Case) { c.LastReplyAt = h.clock.Now() })
	h.update(t, closed.CaseID, func(c *store.Case) { c.Status, c.ClosedAt = store.StatusClosed, h.clock.Now() })

	h.clock.Advance(2 * day) // a week after delivery
	if r := h.clear(t); r.Bodies != 3 || r.Addresses != 0 {
		t.Fatalf("Clear a week after delivery = %+v, want three bodies and no addresses", r)
	}
	h.clock.Advance(5 * day) // a week after the reply and the closure
	if r := h.clear(t); r.Addresses != 2 {
		t.Fatalf("Clear a week after the last activity = %+v, want two addresses", r)
	}
	if c := h.caseOf(t, open); c.Requester.ReplyTo == "" {
		t.Fatal("the address of an open ticket was cleared")
	}
	h.clock.Advance(90*day - 12*day) // the maximum hold since delivery
	if r := h.clear(t); r.Addresses != 1 {
		t.Errorf("Clear at the maximum hold = %+v, want the open ticket's address", r)
	}
}

// holds holds the listed principals, and fails for "broken".
type holds map[string]bool

func (h holds) Held(_ context.Context, principalID string) (bool, error) {
	if principalID == "broken" {
		return false, errors.New("hold service unavailable")
	}
	return h[principalID], nil
}

func TestClearSkipsHeldRequesters(t *testing.T) {
	h := newHarness(t, []string{"mail"}, func(c *supportforge.Config) { c.Holds = holds{"p1": true} })
	// More held cases than one batch, delivered first, so they come before
	// the others in every batch Clear reads.
	var held []string
	for range 150 {
		held = append(held, h.submit(t, signedIn()).CaseID)
	}
	h.deliverAll(t)
	h.clock.Advance(time.Minute)
	broken := signedIn()
	broken.Requester.PrincipalID = "broken"
	failed := h.submit(t, broken)
	other := h.submit(t, visitor())
	h.deliverAll(t)

	h.clock.Advance(8 * day)
	r, err := h.svc.Clear(t.Context())
	if err == nil || !strings.Contains(err.Error(), failed.CaseID) {
		t.Errorf("Clear error = %v, want the failed hold check for %s", err, failed.CaseID)
	}
	if len(r.Held) != len(held) || r.Bodies != 1 || r.Addresses != 1 {
		t.Errorf("Clear = %d held, %d bodies, %d addresses; want %d held and the visitor's case cleared",
			len(r.Held), r.Bodies, r.Addresses, len(held))
	}
	for _, id := range append(held[:1:1], failed.CaseID) {
		c, err := h.store.Case(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if c.Body == "" {
			t.Errorf("case %s was cleared", id)
		}
	}
	if c := h.caseOf(t, other); c.Body != "" {
		t.Error("the visitor's case was not cleared")
	}
}

func TestClearStopsWithContext(t *testing.T) {
	h := newHarness(t, []string{"mail"}, nil)
	h.submit(t, signedIn())
	h.deliver(t)
	h.clock.Advance(8 * day)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.svc.Clear(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Clear with a canceled context = %v", err)
	}
}

func TestNewRejectsAMaxHoldShorterThanTheDelay(t *testing.T) {
	_, err := supportforge.New(supportforge.Config{
		Store:      memstore.New(),
		Desks:      []supportcore.Desk{memdesk.New("mail")},
		ClearDelay: 30 * day,
		MaxHold:    7 * day,
	})
	if err == nil {
		t.Error("New accepted a maximum hold shorter than the clear delay")
	}
}
