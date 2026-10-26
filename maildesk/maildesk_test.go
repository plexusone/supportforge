package maildesk_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/plexusone/omnimail"
	"github.com/plexusone/omnimail/memsender"
	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge-core/desktest"
	"github.com/plexusone/supportforge/maildesk"
)

var (
	from     = omnimail.Address{Name: "Example Support", Email: "support@example.com"}
	operator = omnimail.Address{Email: "inbox@example.com"}
)

// vendor reads what the mail desk sent through a memsender.
type vendor struct{ sender *memsender.Sender }

func (v vendor) Ticket(d supportcore.Delivery) (desktest.Ticket, bool) {
	var tk desktest.Ticket
	found := false
	for _, m := range v.sender.Messages() {
		if m.Headers[maildesk.ReferenceHeader] != d.TicketID {
			continue
		}
		found = true
		recipients := m.Recipients()
		if slices.Contains(recipients, operator) {
			tk.Responders = append(tk.Responders, m.Subject, m.Text)
		}
		if slices.ContainsFunc(recipients, func(a omnimail.Address) bool { return a != operator }) {
			tk.Sent = append(tk.Sent, m.Subject, m.Text)
		}
	}
	return tk, found
}

func (v vendor) Fail(f desktest.Failure) bool {
	kinds := map[desktest.Failure]omnimail.Kind{
		desktest.Unavailable:  omnimail.KindTransient,
		desktest.RateLimited:  omnimail.KindThrottled,
		desktest.Unauthorized: omnimail.KindAuth,
		desktest.Invalid:      omnimail.KindRejected,
	}
	kind, ok := kinds[f]
	if ok {
		v.sender.FailNext(omnimail.NewError(kind, memsender.ProviderName, errors.New(f.String())))
	}
	return ok
}

func newDesk(t *testing.T) (*maildesk.Desk, *memsender.Sender) {
	t.Helper()
	s := memsender.New()
	d, err := maildesk.New(maildesk.Config{Sender: s, From: from, Operator: operator})
	if err != nil {
		t.Fatal(err)
	}
	return d, s
}

func TestConformance(t *testing.T) {
	desktest.Run(t, func(t *testing.T) (supportcore.Desk, desktest.Vendor) {
		d, s := newDesk(t)
		return d, vendor{s}
	})
}

func testCase() supportcore.Case {
	return supportcore.Case{
		ID: "c1", Reference: "SR-ABC123", Product: "app", Kind: supportcore.KindProblem,
		Title: "Export fails", Body: "It times out.\nEvery time.", Page: "/export", ReplyWanted: true,
		Requester: supportcore.Requester{PrincipalID: "p1", ReplyTo: "ann@example.com"},
	}
}

func TestDeliverAddressesBothMessages(t *testing.T) {
	d, s := newDesk(t)
	del, err := d.Deliver(t.Context(), testCase(), supportcore.Acknowledgment{Subject: "Got it", Body: "We will reply."}, supportcore.Context{"plan": "free"})
	if err != nil {
		t.Fatal(err)
	}
	if del != (supportcore.Delivery{Desk: "mail", TicketID: "SR-ABC123"}) {
		t.Errorf("Delivery = %+v", del)
	}
	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("sent %d messages, want 2", len(msgs))
	}
	op, ack := msgs[0], msgs[1]
	if op.To[0] != operator || op.ReplyTo[0].Email != "ann@example.com" {
		t.Errorf("operator message To %v, Reply-To %v", op.To, op.ReplyTo)
	}
	if op.Subject != "[SR-ABC123] problem: Export fails" {
		t.Errorf("operator subject = %q", op.Subject)
	}
	for _, want := range []string{"It times out.\nEvery time.", "Reply to: ann@example.com", "plan: free"} {
		if !strings.Contains(op.Text, want) {
			t.Errorf("operator text lacks %q:\n%s", want, op.Text)
		}
	}
	if ack.To[0].Email != "ann@example.com" || ack.ReplyTo[0] != operator || ack.Subject != "Got it" || ack.Text != "We will reply." {
		t.Errorf("acknowledgment = %+v", ack)
	}
	if op.IdempotencyKey == ack.IdempotencyKey || op.IdempotencyKey == "" {
		t.Errorf("idempotency keys %q and %q", op.IdempotencyKey, ack.IdempotencyKey)
	}
}

func TestDeliverWithoutReplyAddress(t *testing.T) {
	d, s := newDesk(t)
	c := testCase()
	c.Kind, c.ReplyWanted, c.Requester = supportcore.KindFeedback, false, supportcore.Requester{}
	if _, err := d.Deliver(t.Context(), c, supportcore.Acknowledgment{Subject: "s", Body: "b"}, nil); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 || len(s.Last().ReplyTo) != 0 || !strings.Contains(s.Last().Text, "(no reply wanted)") {
		t.Errorf("messages = %+v", s.Messages())
	}
}

func TestSubjectIsOneLine(t *testing.T) {
	d, s := newDesk(t)
	c := testCase()
	c.Title = "two\nlines"
	if _, err := d.Deliver(t.Context(), c, supportcore.Acknowledgment{Subject: "a\r\nb", Body: "b"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range s.Messages() {
		if strings.ContainsAny(m.Subject, "\r\n") {
			t.Errorf("subject %q has a line break", m.Subject)
		}
	}
}

func TestReply(t *testing.T) {
	d, s := newDesk(t)
	c := testCase()
	del := supportcore.Delivery{Desk: "mail", TicketID: c.Reference}
	if err := d.Reply(t.Context(), del, c, "Try again now."); err != nil {
		t.Fatal(err)
	}
	m := s.Last()
	if m.To[0].Email != "ann@example.com" || m.Bcc[0] != operator || m.ReplyTo[0] != operator || m.Text != "Try again now." {
		t.Errorf("reply = %+v", m)
	}
	cleared := c
	cleared.Requester.ReplyTo = ""
	if err := d.Reply(t.Context(), del, cleared, "hi"); !errors.Is(err, supportcore.ErrRejected) {
		t.Errorf("Reply after the address was cleared = %v, want ErrRejected", err)
	}
}

func TestClassify(t *testing.T) {
	d, s := newDesk(t)
	throttled := omnimail.NewError(omnimail.KindThrottled, "x", nil)
	throttled.RetryAfter = 2 * time.Minute
	s.FailNext(throttled)
	_, err := d.Deliver(t.Context(), testCase(), supportcore.Acknowledgment{Subject: "s", Body: "b"}, nil)
	if after, ok := supportcore.RetryAfter(err); !ok || after != 2*time.Minute {
		t.Errorf("RetryAfter = %v, %v; want the provider's delay", after, ok)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := d.Reply(ctx, supportcore.Delivery{TicketID: "SR-ABC123"}, testCase(), "x"); supportcore.Classify(err) != supportcore.ErrRetryable {
		t.Errorf("Reply with a canceled context = %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	s := memsender.New()
	for name, cfg := range map[string]maildesk.Config{
		"no sender":   {From: from, Operator: operator},
		"no from":     {Sender: s, Operator: operator},
		"no operator": {Sender: s, From: from},
	} {
		if _, err := maildesk.New(cfg); err == nil {
			t.Errorf("%s: New succeeded", name)
		}
	}
}
