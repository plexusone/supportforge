package supportforge_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plexusone/supportforge"
	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge-core/memdesk"
	"github.com/plexusone/supportforge/store"
	"github.com/plexusone/supportforge/store/memstore"
)

// clock is a fake clock that tests move forward.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recorder collects events.
type recorder struct {
	mu     sync.Mutex
	events []supportforge.Event
}

func (r *recorder) Observe(_ context.Context, e supportforge.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) types() []supportforge.EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]supportforge.EventType, len(r.events))
	for i, e := range r.events {
		out[i] = e.Type
	}
	return out
}

type challenge struct{ valid string }

func (c challenge) VerifyChallenge(_ context.Context, token string) error {
	if token != c.valid {
		return errors.New("bad token")
	}
	return nil
}

type harness struct {
	svc      *supportforge.Service
	store    *memstore.Store
	desks    []*memdesk.Desk
	clock    *clock
	recorder *recorder
}

func newHarness(t *testing.T, deskNames []string, mutate func(*supportforge.Config)) *harness {
	t.Helper()
	h := &harness{store: memstore.New(), clock: &clock{now: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}, recorder: &recorder{}}
	cfg := supportforge.Config{
		Store:     h.store,
		Challenge: challenge{valid: "ok"},
		Observer:  h.recorder,
		Now:       h.clock.Now,
		Random:    rand.NewChaCha8([32]byte{1}),
	}
	for _, name := range deskNames {
		d := memdesk.New(name)
		h.desks = append(h.desks, d)
		cfg.Desks = append(cfg.Desks, d)
	}
	if mutate != nil {
		mutate(&cfg)
	}
	svc, err := supportforge.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	return h
}

func signedIn() supportforge.Request {
	return supportforge.Request{
		Product: "app", Kind: supportcore.KindProblem, Title: "Export fails", Body: "It times out.",
		Page:      "https://app.example.com/export?token=secret#x",
		Requester: supportcore.Requester{PrincipalID: "p1", OrganizationID: "o1", ReplyTo: "ann@example.com"},
	}
}

func visitor() supportforge.Request {
	return supportforge.Request{
		Product: "app", Kind: supportcore.KindQuestion, Title: "visitor-title-x9", Body: "visitor-body-x9",
		Requester: supportcore.Requester{ReplyTo: "someone@example.com"}, Challenge: "ok", RateKey: "ip-hash",
	}
}

func (h *harness) submit(t *testing.T, r supportforge.Request) supportforge.Receipt {
	t.Helper()
	rc, err := h.svc.Submit(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func (h *harness) deliver(t *testing.T) {
	t.Helper()
	if _, err := h.svc.Deliver(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) caseOf(t *testing.T, rc supportforge.Receipt) store.Case {
	t.Helper()
	c, err := h.store.Case(t.Context(), rc.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSubmitStoresBeforeSending(t *testing.T) {
	h := newHarness(t, []string{"mail"}, nil)
	rc := h.submit(t, signedIn())
	if !strings.HasPrefix(rc.Reference, "SR-") || len(rc.Reference) != 9 {
		t.Errorf("Reference = %q", rc.Reference)
	}
	c := h.caseOf(t, rc)
	if c.Status != store.StatusReceived || c.Page != "/export" || !c.ReplyWanted {
		t.Errorf("stored case: status %s, page %q, reply wanted %t", c.Status, c.Page, c.ReplyWanted)
	}
	if h.desks[0].Len() != 0 {
		t.Error("Submit sent something; only the worker sends")
	}
	h.deliver(t)
	c = h.caseOf(t, rc)
	if c.Status != store.StatusDelivered || c.Delivery.Desk != "mail" || c.DeliveredAt.IsZero() {
		t.Errorf("after Deliver: %+v %+v", c.Status, c.Delivery)
	}
	if got := fmt.Sprint(h.recorder.types()); got != "[case_created delivered]" {
		t.Errorf("events = %s", got)
	}
}

func TestSubmitValidates(t *testing.T) {
	h := newHarness(t, []string{"mail"}, nil)
	long := strings.Repeat("a", supportforge.MaxTitle+1)
	tests := map[string]struct {
		mutate func(*supportforge.Request)
		field  string
	}{
		"kind":            {func(r *supportforge.Request) { r.Kind = "complaint" }, "kind"},
		"no product":      {func(r *supportforge.Request) { r.Product = " " }, "product"},
		"no title":        {func(r *supportforge.Request) { r.Title = "" }, "title"},
		"long title":      {func(r *supportforge.Request) { r.Title = long }, "title"},
		"two-line title":  {func(r *supportforge.Request) { r.Title = "a\nb" }, "title"},
		"no body":         {func(r *supportforge.Request) { r.Body = "\n" }, "body"},
		"control in body": {func(r *supportforge.Request) { r.Body = "a\x00b" }, "body"},
		"bad address":     {func(r *supportforge.Request) { r.Requester.ReplyTo = "not an address" }, "replyTo"},
		"display name":    {func(r *supportforge.Request) { r.Requester.ReplyTo = "Ann <ann@example.com>" }, "replyTo"},
		"no address":      {func(r *supportforge.Request) { r.Requester.ReplyTo = "" }, "replyTo"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := signedIn()
			tt.mutate(&r)
			_, err := h.svc.Submit(t.Context(), r)
			var ve *supportforge.ValidationError
			if !errors.As(err, &ve) || !errors.Is(err, supportforge.ErrInvalid) || ve.Field != tt.field {
				t.Errorf("Submit = %v, want a validation error on %s", err, tt.field)
			}
		})
	}
	t.Run("feedback without a reply", func(t *testing.T) {
		r := signedIn()
		r.Kind, r.ReplyWanted, r.Requester.ReplyTo = supportcore.KindFeedback, false, ""
		if _, err := h.svc.Submit(t.Context(), r); err != nil {
			t.Errorf("Submit = %v", err)
		}
	})
	t.Run("a non-feedback kind always wants a reply", func(t *testing.T) {
		r := signedIn()
		r.ReplyWanted = false
		rc := h.submit(t, r)
		if !h.caseOf(t, rc).ReplyWanted {
			t.Error("ReplyWanted is false for a problem")
		}
	})
}

func TestVisitors(t *testing.T) {
	limited := map[string]int{}
	h := newHarness(t, []string{"mail"}, func(c *supportforge.Config) {
		c.Limiter = limiterFunc(func(key string) error {
			limited[key]++
			if limited[key] > 1 {
				return supportforge.ErrRateLimited
			}
			return nil
		})
	})
	r := visitor()
	r.Requester.OrganizationID = "o1"
	r.Share = true
	rc := h.submit(t, r)
	c := h.caseOf(t, rc)
	if c.Requester.OrganizationID != "" || c.Shared {
		t.Errorf("a visitor's case has organization %q, shared %t", c.Requester.OrganizationID, c.Shared)
	}
	if limited["visitor:ip-hash"] != 1 {
		t.Errorf("rate-limit keys = %v", limited)
	}
	if _, err := h.svc.Submit(t.Context(), visitor()); !errors.Is(err, supportforge.ErrRateLimited) {
		t.Errorf("second visitor request = %v, want ErrRateLimited", err)
	}
	bad := visitor()
	bad.RateKey, bad.Challenge = "other", "forged"
	if _, err := h.svc.Submit(t.Context(), bad); !errors.Is(err, supportforge.ErrChallenge) {
		t.Errorf("forged challenge = %v, want ErrChallenge", err)
	}
	none := visitor()
	none.RateKey, none.Challenge = "third", ""
	if _, err := h.svc.Submit(t.Context(), none); !errors.Is(err, supportforge.ErrChallenge) {
		t.Errorf("no challenge = %v, want ErrChallenge", err)
	}
}

type limiterFunc func(key string) error

func (f limiterFunc) Allow(_ context.Context, key string) error { return f(key) }

type sharesAll bool

func (s sharesAll) SharesAll(context.Context, string) (bool, error) { return bool(s), nil }

func TestSharing(t *testing.T) {
	billing := signedIn()
	billing.Kind = supportcore.KindBilling
	chosen := signedIn()
	chosen.Share = true
	tests := []struct {
		name   string
		policy bool
		req    supportforge.Request
		want   store.ShareReason
	}{
		{"private by default", false, signedIn(), ""},
		{"chosen", false, chosen, store.ShareRequester},
		{"billing", false, billing, store.ShareBilling},
		{"policy", true, signedIn(), store.SharePolicy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, []string{"mail"}, func(c *supportforge.Config) { c.Sharing = sharesAll(tt.policy) })
			rc := h.submit(t, tt.req)
			if rc.ShareReason != tt.want || rc.Shared != (tt.want != "") || h.caseOf(t, rc).ShareReason != tt.want {
				t.Errorf("receipt %+v, want reason %q", rc, tt.want)
			}
		})
	}
}

func TestAcknowledgmentsNeverEchoVisitors(t *testing.T) {
	tmpl, err := supportforge.NewTemplates(map[supportforge.TemplateKey]supportforge.Template{
		{Visitor: true}: {Subject: "{{.Title}}", Body: "You wrote {{.Title}}: {{.Body}}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, []string{"mail"}, func(c *supportforge.Config) { c.Templates = tmpl })
	rc := h.submit(t, visitor())
	h.deliver(t)
	tk, ok := h.desks[0].Ticket(h.caseOf(t, rc).Delivery.TicketID)
	if !ok {
		t.Fatal("no ticket")
	}
	for _, e := range tk.Sent {
		if strings.Contains(e.Subject+e.Body, "x9") {
			t.Errorf("a visitor was sent their own text: %+v", e)
		}
	}
}

func TestAcknowledgmentTemplates(t *testing.T) {
	tmpl, err := supportforge.NewTemplates(map[supportforge.TemplateKey]supportforge.Template{
		{Product: "app"}: {Subject: "app {{.Reference}}", Body: "app"},
		{Product: "app", Kind: supportcore.KindBilling}: {Subject: "billing", Body: "billing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := supportcore.Case{Reference: "SR-1", Product: "app", Kind: supportcore.KindProblem, Title: "T", Body: "line1\nline2",
		Requester: supportcore.Requester{PrincipalID: "p1"}}
	if ack, err := tmpl.Render(c); err != nil || ack.Subject != "app SR-1" {
		t.Errorf("product template: %+v, %v", ack, err)
	}
	c.Kind = supportcore.KindBilling
	if ack, err := tmpl.Render(c); err != nil || ack.Subject != "billing" {
		t.Errorf("product and kind template: %+v, %v", ack, err)
	}
	c.Product, c.Kind = "other", supportcore.KindProblem
	ack, err := tmpl.Render(c)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Subject != "We received your report (SR-1)" || !strings.Contains(ack.Body, "> line1\n> line2") {
		t.Errorf("default signed-in acknowledgment = %+v", ack)
	}
	if _, err := supportforge.NewTemplates(map[supportforge.TemplateKey]supportforge.Template{{}: {Subject: "{{"}}); err == nil {
		t.Error("a broken template was accepted")
	}
}

func TestWorkerMovesThroughTheDeskOrder(t *testing.T) {
	h := newHarness(t, []string{"vendor", "mail"}, func(c *supportforge.Config) { c.MaxAttempts = 2 })
	vendor := h.desks[0]
	vendor.FailNext(supportcore.Retry(errors.New("503"), 0))
	vendor.FailNext(supportcore.Retry(errors.New("503"), 0))
	rc := h.submit(t, signedIn())
	h.deliver(t)
	h.clock.Advance(time.Hour)
	h.deliver(t) // second retryable failure: move to mail
	h.deliver(t) // mail, at once
	c := h.caseOf(t, rc)
	if c.Delivery.Desk != "mail" {
		t.Errorf("delivered to %q, want mail after the vendor's attempts ran out", c.Delivery.Desk)
	}
}

func TestWorkerErrorClasses(t *testing.T) {
	for _, tt := range []struct {
		name  string
		err   error
		event supportforge.EventType
	}{
		{"rejected", supportcore.ErrRejected, supportforge.EventDeliveryFailed},
		{"credential", supportcore.ErrCredential, supportforge.EventCredentialFailed},
		{"unsupported", supportcore.ErrUnsupported, supportforge.EventDeliveryFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, []string{"vendor", "mail"}, nil)
			h.desks[0].FailNext(fmt.Errorf("vendor: %w", tt.err))
			rc := h.submit(t, signedIn())
			h.deliver(t)
			h.deliver(t) // the next desk is due at once, with no backoff
			if c := h.caseOf(t, rc); c.Delivery.Desk != "mail" {
				t.Errorf("delivered to %q, want mail", c.Delivery.Desk)
			}
			if got := h.recorder.types(); len(got) < 2 || got[1] != tt.event {
				t.Errorf("events = %v, want %s second", got, tt.event)
			}
		})
	}
}

func TestLastDeskNeverGivesUp(t *testing.T) {
	h := newHarness(t, []string{"mail"}, func(c *supportforge.Config) { c.MaxAttempts = 1 })
	for range 10 {
		h.desks[0].FailNext(supportcore.ErrRejected)
	}
	rc := h.submit(t, signedIn())
	for range 11 {
		h.deliver(t)
		h.clock.Advance(2 * time.Hour)
	}
	if c := h.caseOf(t, rc); c.Status != store.StatusDelivered {
		t.Errorf("status %s after the last desk recovered, want delivered", c.Status)
	}
}

func TestBackoffHonorsRetryAfter(t *testing.T) {
	h := newHarness(t, []string{"mail"}, nil)
	h.desks[0].FailNext(supportcore.Retry(errors.New("429"), 45*time.Minute))
	rc := h.submit(t, signedIn())
	h.deliver(t)
	h.clock.Advance(44 * time.Minute)
	h.deliver(t)
	if h.caseOf(t, rc).Status != store.StatusReceived {
		t.Fatal("redelivered before the desk's retry-after")
	}
	h.clock.Advance(2 * time.Minute)
	h.deliver(t)
	if h.caseOf(t, rc).Status != store.StatusDelivered {
		t.Error("not delivered after the retry-after")
	}
}

// TestNoCaseIsLost runs many cases through desks that fail at random, and
// checks every case ends up delivered exactly once.
func TestNoCaseIsLost(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7)) //nolint:gosec // a reproducible test sequence
	h := newHarness(t, []string{"vendor", "backup", "mail"}, func(c *supportforge.Config) { c.MaxAttempts = 3 })
	classes := []error{
		supportcore.Retry(errors.New("503"), 0), supportcore.ErrRejected, supportcore.ErrCredential,
		supportcore.ErrUnsupported, errors.New("unclassified"),
	}
	for _, d := range h.desks {
		for range 60 {
			d.FailNext(classes[r.IntN(len(classes))])
		}
	}
	var receipts []supportforge.Receipt
	for i := range 25 {
		req := signedIn()
		req.Title = fmt.Sprintf("case %d", i)
		receipts = append(receipts, h.submit(t, req))
	}
	for range 400 {
		if _, err := h.svc.Deliver(t.Context()); err != nil {
			t.Fatal(err)
		}
		h.clock.Advance(time.Hour + time.Minute)
	}
	failures := 0
	for _, typ := range h.recorder.types() {
		if typ == supportforge.EventDeliveryFailed || typ == supportforge.EventCredentialFailed {
			failures++
		}
	}
	if failures < 100 {
		t.Errorf("only %d failed attempts; the test is not exercising the desk order", failures)
	}
	t.Logf("%d failed attempts before every case was delivered", failures)
	tickets := 0
	for _, d := range h.desks {
		tickets += d.Len()
	}
	if tickets != len(receipts) {
		t.Errorf("%d tickets for %d cases", tickets, len(receipts))
	}
	for _, rc := range receipts {
		if c := h.caseOf(t, rc); c.Status != store.StatusDelivered {
			t.Errorf("case %s is %s", rc.Reference, c.Status)
		}
	}
}

func TestEventsCarryNoText(t *testing.T) {
	h := newHarness(t, []string{"vendor", "mail"}, nil)
	h.desks[0].FailNext(supportcore.ErrCredential)
	h.submit(t, visitor())
	h.deliver(t)
	h.deliver(t)
	for _, e := range h.recorder.events {
		if s := fmt.Sprintf("%+v", e); strings.Contains(s, "x9") || strings.Contains(s, "@") {
			t.Errorf("event carries personal data: %s", s)
		}
	}
}

type contextSource map[string]string

func (c contextSource) ResponderContext(context.Context, supportcore.Case) (supportcore.Context, error) {
	return supportcore.Context(c), nil
}

func TestResponderContext(t *testing.T) {
	h := newHarness(t, []string{"mail"}, func(c *supportforge.Config) {
		c.Context = contextSource{"plan": "pro", "principal": "spoofed"}
	})
	rc := h.submit(t, signedIn())
	h.deliver(t)
	tk, ok := h.desks[0].Ticket(h.caseOf(t, rc).Delivery.TicketID)
	if !ok {
		t.Fatal("no ticket")
	}
	note := tk.Transcript[1].Body
	for _, want := range []string{"plan: pro", "principal: p1", "page: /export", "requester: signed in", "organization: o1"} {
		if !strings.Contains(note, want) {
			t.Errorf("responder context lacks %q:\n%s", want, note)
		}
	}
}

func TestRequesterList(t *testing.T) {
	h := newHarness(t, []string{"vendor", "mail"}, nil)
	h.desks[0].SetPortal("https://help.example.com/t/")
	first := h.submit(t, signedIn())
	h.clock.Advance(time.Minute)
	other := signedIn()
	other.Requester.PrincipalID = "p2"
	h.submit(t, other)
	h.clock.Advance(time.Minute)
	second := h.submit(t, signedIn())
	h.deliver(t)

	list, err := h.svc.RequesterCases(t.Context(), "p1", store.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Reference != second.Reference || list[1].Reference != first.Reference {
		t.Fatalf("list = %+v", list)
	}
	e := list[0]
	if e.Title != "Export fails" || e.Status != store.StatusDelivered || !strings.HasPrefix(e.DeskURL, "https://help.example.com/t/") {
		t.Errorf("entry = %+v", e)
	}
	if _, err := h.svc.RequesterCase(t.Context(), "p2", first.Reference); !errors.Is(err, supportforge.ErrNotFound) {
		t.Errorf("another principal's case = %v, want ErrNotFound", err)
	}
	if got, err := h.svc.RequesterCase(t.Context(), "p1", first.Reference); err != nil || got.Reference != first.Reference {
		t.Errorf("own case = %+v, %v", got, err)
	}
	if _, err := h.svc.RequesterCases(t.Context(), "", store.Page{}); !errors.Is(err, supportforge.ErrInvalid) {
		t.Errorf("list without a principal = %v", err)
	}
}

func TestRefreshStatuses(t *testing.T) {
	h := newHarness(t, []string{"vendor", "mail"}, nil)
	rc := h.submit(t, signedIn())
	h.deliver(t)
	ticket := h.caseOf(t, rc).Delivery.TicketID

	if n, err := h.svc.RefreshStatuses(t.Context(), 10); err != nil || n != 1 {
		t.Fatalf("RefreshStatuses = %d, %v", n, err)
	}
	if c := h.caseOf(t, rc); c.Status != store.StatusOpen {
		t.Errorf("status %s, want open", c.Status)
	}
	h.desks[0].SetStatus(ticket, supportcore.StatusClosed)
	h.clock.Advance(time.Hour)
	if _, err := h.svc.RefreshStatuses(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	c := h.caseOf(t, rc)
	if c.Status != store.StatusClosed || !c.ClosedAt.Equal(h.clock.Now()) {
		t.Errorf("status %s closed at %v, want closed now", c.Status, c.ClosedAt)
	}
	if n, err := h.svc.RefreshStatuses(t.Context(), 10); err != nil || n != 0 {
		t.Errorf("closed cases are checked again: %d, %v", n, err)
	}
	h.desks[0].FailNext(supportcore.ErrCredential)
	rc2 := h.submit(t, signedIn())
	h.deliver(t) // the vendor fails; the case moves to mail
	h.deliver(t)
	if h.caseOf(t, rc2).Delivery.Desk != "mail" {
		t.Fatal("expected delivery by mail")
	}
}

func TestRunStopsWithContext(t *testing.T) {
	h := newHarness(t, []string{"mail"}, nil)
	rc := h.submit(t, signedIn())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- h.svc.Run(ctx, time.Millisecond, func(err error) { t.Error(err) }) }()
	deadline := time.After(5 * time.Second)
	for h.caseOf(t, rc).Status != store.StatusDelivered {
		select {
		case <-deadline:
			t.Fatal("Run did not deliver")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	d := memdesk.New("mail")
	for name, cfg := range map[string]supportforge.Config{
		"no store":   {Desks: []supportcore.Desk{d}},
		"no desks":   {Store: memstore.New()},
		"same names": {Store: memstore.New(), Desks: []supportcore.Desk{d, memdesk.New("mail")}},
	} {
		if _, err := supportforge.New(cfg); err == nil {
			t.Errorf("%s: New succeeded", name)
		}
	}
}
