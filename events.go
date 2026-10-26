package supportforge

import "context"

// EventType names something that happened to a case.
type EventType string

const (
	EventCaseCreated      EventType = "case_created"
	EventDelivered        EventType = "delivered"
	EventDeliveryFailed   EventType = "delivery_failed"   // a desk failed; the case will be retried
	EventCredentialFailed EventType = "credential_failed" //nolint:gosec // an event name, not a credential
	EventDeskFailed       EventType = "desk_failed"       // a desk call other than delivery failed
)

// Event is a notice about a case. It carries identifiers and
// classifications only, never a title, body, address or ticket content, so
// a host can send it to logs, metrics, alerts or analytics.
type Event struct {
	Type      EventType
	CaseID    string
	Reference string
	Product   string
	Kind      string
	Desk      string // the desk involved, if any
	Class     string // the error class of a failure: retryable, credential, rejected, unsupported or unclassified
	Attempts  int    // attempts at the desk so far, for a failure
	LastDesk  bool   // the failure was at the last desk in the order
}

// Observer receives events. It must not block; a host that does slow work
// queues it.
type Observer interface {
	Observe(ctx context.Context, e Event)
}

// ObserverFunc adapts a function to [Observer].
type ObserverFunc func(ctx context.Context, e Event)

// Observe calls f(ctx, e).
func (f ObserverFunc) Observe(ctx context.Context, e Event) { f(ctx, e) }

type nopObserver struct{}

func (nopObserver) Observe(context.Context, Event) {}
