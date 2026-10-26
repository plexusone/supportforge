// Package storetest checks that a [store.Store] keeps the contract. A store
// implementation's tests call [Run] with a function that returns an empty
// store.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge/store"
)

// NewFunc returns an empty store.
type NewFunc func(t *testing.T) store.Store

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// Run runs every check against a fresh store from newStore.
func Run(t *testing.T, newStore NewFunc) {
	t.Helper()
	checks := []struct {
		name string
		run  func(t *testing.T, s store.Store)
	}{
		{"CreateAndRead", checkCreateAndRead},
		{"RejectsDuplicates", checkDuplicates},
		{"NotFound", checkNotFound},
		{"Update", checkUpdate},
		{"ClaimsOnlyDueAndUnleased", checkClaim},
		{"ClaimsEachEntryOnce", checkConcurrentClaim},
		{"Reschedule", checkReschedule},
		{"Delivered", checkDelivered},
		{"ByPrincipal", checkByPrincipal},
		{"StatusDue", checkStatusDue},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) { c.run(t, newStore(t)) })
	}
}

func newCase(id, principal string, created time.Time) store.Case {
	return store.Case{
		Case: supportcore.Case{
			ID:        id,
			Reference: "SR-" + id,
			Product:   "storetest",
			Kind:      supportcore.KindQuestion,
			Title:     "title " + id,
			Body:      "body " + id,
			Requester: supportcore.Requester{PrincipalID: principal, ReplyTo: id + "@example.com"},
			CreatedAt: created,
		},
		Status: store.StatusReceived,
	}
}

func create(t *testing.T, s store.Store, c store.Case, due time.Time) store.Entry {
	t.Helper()
	e := store.Entry{ID: "e-" + c.ID, CaseID: c.ID, NextAttemptAt: due}
	if err := s.Create(t.Context(), c, e); err != nil {
		t.Fatalf("Create(%s): %v", c.ID, err)
	}
	return e
}

func checkCreateAndRead(t *testing.T, s store.Store) {
	want := newCase("c1", "p1", t0)
	want.Shared = true
	want.ShareReason = store.ShareBilling
	create(t, s, want, t0)
	for name, get := range map[string]func() (store.Case, error){
		"Case":            func() (store.Case, error) { return s.Case(t.Context(), "c1") },
		"CaseByReference": func() (store.Case, error) { return s.CaseByReference(t.Context(), "SR-c1") },
	} {
		got, err := get()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !equal(got, want) {
			t.Errorf("%s =\n%s\nwant\n%s", name, fields(got), fields(want))
		}
	}
}

func checkDuplicates(t *testing.T, s store.Store) {
	create(t, s, newCase("c1", "p1", t0), t0)
	sameID := newCase("c1", "p1", t0)
	sameID.Reference = "SR-other"
	sameRef := newCase("c2", "p1", t0)
	sameRef.Reference = "SR-c1"
	for name, c := range map[string]store.Case{"same ID": sameID, "same reference": sameRef} {
		err := s.Create(t.Context(), c, store.Entry{ID: "e-" + name, CaseID: c.ID})
		if !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("Create with the %s = %v, want ErrDuplicate", name, err)
		}
	}
	if _, err := s.Case(t.Context(), "c2"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a rejected Create left case c2 behind: %v", err)
	}
}

func checkNotFound(t *testing.T, s store.Store) {
	ctx := t.Context()
	errs := map[string]error{}
	_, errs["Case"] = s.Case(ctx, "missing")
	_, errs["CaseByReference"] = s.CaseByReference(ctx, "SR-missing")
	errs["Update"] = s.Update(ctx, "missing", func(*store.Case) error { return nil })
	errs["Reschedule"] = s.Reschedule(ctx, store.Entry{ID: "missing"})
	errs["Delivered"] = s.Delivered(ctx, "missing", supportcore.Delivery{}, t0)
	for name, err := range errs {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", name, err)
		}
	}
}

func checkUpdate(t *testing.T, s store.Store) {
	create(t, s, newCase("c1", "p1", t0), t0)
	err := s.Update(t.Context(), "c1", func(c *store.Case) error {
		c.Body = ""
		c.BodyClearedAt = t0.Add(time.Hour)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("refused")
	err = s.Update(t.Context(), "c1", func(c *store.Case) error {
		c.Title = "changed"
		return failure
	})
	if !errors.Is(err, failure) {
		t.Errorf("Update returned %v, want fn's error", err)
	}
	got, err := s.Case(t.Context(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "" || !got.BodyClearedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("the first update was not saved: %s", fields(got))
	}
	if got.Title != "title c1" {
		t.Errorf("an update whose fn failed was saved: title %q", got.Title)
	}
}

func checkClaim(t *testing.T, s store.Store) {
	create(t, s, newCase("c1", "p1", t0), t0)
	create(t, s, newCase("c2", "p1", t0), t0.Add(-time.Minute))
	create(t, s, newCase("c3", "p1", t0), t0.Add(time.Hour))
	ctx := t.Context()
	got, err := s.Claim(ctx, t0, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(got); fmt.Sprint(ids) != "[e-c2 e-c1]" {
		t.Errorf("Claim = %v, want the two due entries, oldest first", ids)
	}
	if again, err := s.Claim(ctx, t0.Add(30*time.Second), time.Minute, 10); err != nil || len(again) != 0 {
		t.Errorf("Claim during the lease = %v, %v; want none", entryIDs(again), err)
	}
	if after, err := s.Claim(ctx, t0.Add(2*time.Minute), time.Minute, 1); err != nil || len(after) != 1 {
		t.Errorf("Claim with limit 1 after the lease = %v, %v; want one entry", entryIDs(after), err)
	}
}

func checkConcurrentClaim(t *testing.T, s store.Store) {
	for i := range 20 {
		create(t, s, newCase(fmt.Sprintf("c%02d", i), "p1", t0), t0)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			got, err := s.Claim(context.Background(), t0, time.Minute, 20)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, e := range got {
				seen[e.ID]++
			}
		})
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Errorf("claimed %d distinct entries, want 20", len(seen))
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("entry %s was claimed %d times", id, n)
		}
	}
}

func checkReschedule(t *testing.T, s store.Store) {
	e := create(t, s, newCase("c1", "p1", t0), t0)
	ctx := t.Context()
	if _, err := s.Claim(ctx, t0, time.Hour, 10); err != nil {
		t.Fatal(err)
	}
	e.Desk, e.Attempts, e.LastError = 1, 3, "retryable"
	e.NextAttemptAt = t0.Add(time.Minute)
	if err := s.Reschedule(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, err := s.Claim(ctx, t0.Add(time.Minute), time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Claim after Reschedule = %v; want the entry, its lease released", entryIDs(got))
	}
	if g := got[0]; g.Desk != 1 || g.Attempts != 3 || g.LastError != "retryable" || !g.NextAttemptAt.Equal(e.NextAttemptAt) {
		t.Errorf("rescheduled entry = %+v", g)
	}
}

func checkDelivered(t *testing.T, s store.Store) {
	e := create(t, s, newCase("c1", "p1", t0), t0)
	d := supportcore.Delivery{Desk: "mail", TicketID: "SR-c1"}
	at := t0.Add(time.Minute)
	if err := s.Delivered(t.Context(), e.ID, d, at); err != nil {
		t.Fatal(err)
	}
	c, err := s.Case(t.Context(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Delivery != d || c.Status != store.StatusDelivered || !c.DeliveredAt.Equal(at) {
		t.Errorf("case after Delivered = %s", fields(c))
	}
	if got, err := s.Claim(t.Context(), t0.Add(time.Hour), time.Minute, 10); err != nil || len(got) != 0 {
		t.Errorf("Claim after Delivered = %v, %v; want the entry gone", entryIDs(got), err)
	}
	if err := s.Delivered(t.Context(), e.ID, d, at); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a second Delivered = %v, want ErrNotFound", err)
	}
}

func checkByPrincipal(t *testing.T, s store.Store) {
	for i, p := range []string{"p1", "p2", "p1", "", "p1"} {
		create(t, s, newCase(fmt.Sprintf("c%d", i), p, t0.Add(time.Duration(i)*time.Minute)), t0)
	}
	ctx := t.Context()
	got, err := s.ByPrincipal(ctx, "p1", store.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if ids := caseIDs(got); fmt.Sprint(ids) != "[c4 c2 c0]" {
		t.Errorf("ByPrincipal = %v, want p1's cases, newest first", ids)
	}
	got, err = s.ByPrincipal(ctx, "p1", store.Page{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ids := caseIDs(got); fmt.Sprint(ids) != "[c2]" {
		t.Errorf("ByPrincipal page 2 of size 1 = %v, want [c2]", ids)
	}
	if got, err := s.ByPrincipal(ctx, "", store.Page{}); err != nil || len(got) != 0 {
		t.Errorf("ByPrincipal of the empty principal = %v, %v; visitors have no list", caseIDs(got), err)
	}
}

func checkStatusDue(t *testing.T, s store.Store) {
	ctx := t.Context()
	deliver := func(id, desk string, checked time.Time, status store.Status) {
		e := create(t, s, newCase(id, "p1", t0), t0)
		if desk == "" {
			return
		}
		if err := s.Delivered(ctx, e.ID, supportcore.Delivery{Desk: desk, TicketID: id}, t0); err != nil {
			t.Fatal(err)
		}
		err := s.Update(ctx, id, func(c *store.Case) error {
			c.StatusCheckedAt = checked
			c.Status = status
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	deliver("new", "posthog", time.Time{}, store.StatusDelivered)
	deliver("old", "posthog", t0.Add(-time.Hour), store.StatusOpen)
	deliver("recent", "posthog", t0, store.StatusPending)
	deliver("closed", "posthog", t0.Add(-2*time.Hour), store.StatusClosed)
	deliver("mailed", "mail", time.Time{}, store.StatusDelivered)
	deliver("undelivered", "", time.Time{}, store.StatusReceived)
	got, err := s.StatusDue(ctx, []string{"posthog"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := caseIDs(got); fmt.Sprint(ids) != "[new old recent]" {
		t.Errorf("StatusDue = %v, want open posthog cases, least recently checked first", ids)
	}
	if got, err := s.StatusDue(ctx, []string{"posthog"}, 1); err != nil || len(got) != 1 {
		t.Errorf("StatusDue with limit 1 = %v, %v", caseIDs(got), err)
	}
}

func entryIDs(es []store.Entry) []string {
	ids := make([]string, len(es))
	for i, e := range es {
		ids[i] = e.ID
	}
	return ids
}

func caseIDs(cs []store.Case) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

// fields shows every field of c, for comparison and failure messages on
// test data. It spells out the requester, whose String method hides the
// address, and normalizes times to UTC.
func fields(c store.Case) string {
	r := c.Requester
	ts := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
	return fmt.Sprintf("id=%s ref=%s product=%s kind=%s title=%q body=%q page=%q replyWanted=%t shared=%t "+
		"principal=%s organization=%s replyTo=%s created=%s share=%s status=%s desk=%s ticket=%s deskURL=%s "+
		"delivered=%s lastReply=%s closed=%s checked=%s bodyCleared=%s replyToCleared=%s",
		c.ID, c.Reference, c.Product, c.Kind, c.Title, c.Body, c.Page, c.ReplyWanted, c.Shared,
		r.PrincipalID, r.OrganizationID, r.ReplyTo, ts(c.CreatedAt), c.ShareReason, c.Status, c.Delivery.Desk,
		c.Delivery.TicketID, c.DeskURL, ts(c.DeliveredAt), ts(c.LastReplyAt), ts(c.ClosedAt),
		ts(c.StatusCheckedAt), ts(c.BodyClearedAt), ts(c.ReplyToClearedAt))
}

func equal(a, b store.Case) bool {
	return fields(a) == fields(b)
}
