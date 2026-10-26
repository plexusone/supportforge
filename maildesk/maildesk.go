// Package maildesk is a desk that delivers cases by email through omnimail:
// the case to the operator's support address, and the acknowledgment to the
// requester. It needs no help-desk account, so it is the default and the
// last desk in every order.
package maildesk

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/plexusone/omnimail"
	supportcore "github.com/plexusone/supportforge-core"
)

// ReferenceHeader carries the case reference on every message the desk
// sends, so mail rules and tests can group a conversation.
const ReferenceHeader = "X-Support-Reference"

// Config configures a [Desk].
type Config struct {
	Name     string           // default "mail"
	Sender   omnimail.Sender  // required
	From     omnimail.Address // the address messages come from; required
	Operator omnimail.Address // the support inbox; required
}

// Desk is the mail desk. It implements [supportcore.Desk] and
// [supportcore.TicketReplier].
type Desk struct {
	name     string
	sender   omnimail.Sender
	from     omnimail.Address
	operator omnimail.Address
}

var (
	_ supportcore.Desk          = (*Desk)(nil)
	_ supportcore.TicketReplier = (*Desk)(nil)
)

// New returns a mail desk, or an error if cfg is incomplete.
func New(cfg Config) (*Desk, error) {
	if cfg.Sender == nil {
		return nil, errors.New("maildesk: Config.Sender is required")
	}
	if err := cfg.From.Validate(); err != nil {
		return nil, fmt.Errorf("maildesk: Config.From: %w", err)
	}
	if err := cfg.Operator.Validate(); err != nil {
		return nil, fmt.Errorf("maildesk: Config.Operator: %w", err)
	}
	if cfg.Name == "" {
		cfg.Name = "mail"
	}
	return &Desk{name: cfg.Name, sender: cfg.Sender, from: cfg.From, operator: cfg.Operator}, nil
}

// Name implements [supportcore.Desk].
func (d *Desk) Name() string { return d.name }

// Deliver implements [supportcore.Desk]. It sends the case to the operator
// with Reply-To set to the requester, so answering from a mail client
// reaches them, then sends the acknowledgment to the requester with
// Reply-To set to the operator. The ticket ID is the case's reference.
//
// If the acknowledgment fails after the operator's message was sent, the
// retry sends the operator's message again; each message carries an
// idempotency key for providers that honor one.
func (d *Desk) Deliver(ctx context.Context, c supportcore.Case, ack supportcore.Acknowledgment, private supportcore.Context) (supportcore.Delivery, error) {
	if err := ctx.Err(); err != nil {
		return supportcore.Delivery{}, supportcore.Retry(err, 0)
	}
	if c.Reference == "" {
		return supportcore.Delivery{}, fmt.Errorf("maildesk: case %s has no reference: %w", c.ID, supportcore.ErrRejected)
	}
	toOperator := &omnimail.Message{
		From:           d.from,
		To:             []omnimail.Address{d.operator},
		Subject:        oneLine(fmt.Sprintf("[%s] %s: %s", c.Reference, c.Kind, c.Title)),
		Text:           operatorText(c, private),
		Headers:        map[string]string{ReferenceHeader: c.Reference},
		IdempotencyKey: c.ID + "/case",
	}
	if c.Requester.ReplyTo != "" {
		toOperator.ReplyTo = []omnimail.Address{{Email: c.Requester.ReplyTo}}
	}
	if err := d.send(ctx, toOperator); err != nil {
		return supportcore.Delivery{}, err
	}
	if c.Requester.ReplyTo != "" {
		toRequester := &omnimail.Message{
			From:           d.from,
			To:             []omnimail.Address{{Email: c.Requester.ReplyTo}},
			ReplyTo:        []omnimail.Address{d.operator},
			Subject:        oneLine(ack.Subject),
			Text:           ack.Body,
			Headers:        map[string]string{ReferenceHeader: c.Reference},
			IdempotencyKey: c.ID + "/ack",
		}
		if err := d.send(ctx, toRequester); err != nil {
			return supportcore.Delivery{}, err
		}
	}
	return supportcore.Delivery{Desk: d.name, TicketID: c.Reference}, nil
}

// Reply implements [supportcore.TicketReplier]. It sends body to the case's
// reply address and a blind copy to the operator, so the inbox keeps the
// conversation. The delivery must be this case's.
func (d *Desk) Reply(ctx context.Context, del supportcore.Delivery, c supportcore.Case, body string) error {
	if err := ctx.Err(); err != nil {
		return supportcore.Retry(err, 0)
	}
	if del.TicketID == "" || del.TicketID != c.Reference {
		return fmt.Errorf("maildesk: ticket %q is not case %s: %w", del.TicketID, c.Reference, supportcore.ErrRejected)
	}
	if c.Requester.ReplyTo == "" {
		return fmt.Errorf("maildesk: case %s has no reply address: %w", c.Reference, supportcore.ErrRejected)
	}
	return d.send(ctx, &omnimail.Message{
		From:    d.from,
		To:      []omnimail.Address{{Email: c.Requester.ReplyTo}},
		Bcc:     []omnimail.Address{d.operator},
		ReplyTo: []omnimail.Address{d.operator},
		Subject: oneLine("Re: your request " + c.Reference),
		Text:    body,
		Headers: map[string]string{ReferenceHeader: c.Reference},
	})
}

func (d *Desk) send(ctx context.Context, msg *omnimail.Message) error {
	if _, err := d.sender.Send(ctx, msg); err != nil {
		return classify(err)
	}
	return nil
}

// classify maps an omnimail failure to a supportcore error class.
func classify(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return supportcore.Retry(err, 0)
	}
	var mailErr *omnimail.Error
	var retryAfter time.Duration
	if errors.As(err, &mailErr) {
		retryAfter = mailErr.RetryAfter
	}
	switch omnimail.KindOf(err) {
	case omnimail.KindAuth:
		return fmt.Errorf("maildesk: %w: %w", supportcore.ErrCredential, err)
	case omnimail.KindInvalidMessage, omnimail.KindInvalidAddress, omnimail.KindRejected:
		return fmt.Errorf("maildesk: %w: %w", supportcore.ErrRejected, err)
	default: // throttled, transient or unknown
		return supportcore.Retry(fmt.Errorf("maildesk: %w", err), retryAfter)
	}
}

// operatorText is the message responders read: the request, then the facts
// and context.
func operatorText(c supportcore.Case, private supportcore.Context) string {
	var b strings.Builder
	b.WriteString(c.Title + "\n\n" + c.Body + "\n\n---\n")
	reply := c.Requester.ReplyTo
	if reply == "" {
		reply = "(no reply wanted)"
	}
	fmt.Fprintf(&b, "Reference: %s\nReply to: %s\n", c.Reference, reply)
	for _, k := range slices.Sorted(maps.Keys(private)) {
		fmt.Fprintf(&b, "%s: %s\n", k, private[k])
	}
	return b.String()
}

// oneLine replaces control characters, including line breaks, with spaces,
// so a value can be a subject.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsControl), " ")
}
