// Package store defines how supportforge keeps cases and their outbox
// entries. memstore is an in-memory implementation, and storetest checks
// that any implementation keeps the contract.
package store

import (
	"context"
	"errors"
	"time"

	supportcore "github.com/plexusone/supportforge-core"
)

var (
	// ErrNotFound: no case or entry has the given key.
	ErrNotFound = errors.New("store: not found")
	// ErrDuplicate: a case with the same ID or reference exists.
	ErrDuplicate = errors.New("store: duplicate")
)

// Status is where a case is, as the requester's list shows it.
type Status string

const (
	StatusReceived  Status = "received"  // stored, not yet delivered
	StatusDelivered Status = "delivered" // delivered; the desk reports no status
	StatusOpen      Status = Status(supportcore.StatusOpen)
	StatusPending   Status = Status(supportcore.StatusPending)
	StatusClosed    Status = Status(supportcore.StatusClosed)
)

// ShareReason is why a case is shared with the requester's organization
// administrators. It is empty when the case is not shared.
type ShareReason string

const (
	ShareRequester ShareReason = "requester" // the requester chose to share it
	SharePolicy    ShareReason = "policy"    // the organization shares every request
	ShareBilling   ShareReason = "billing"   // billing requests are always shared
)

// Case is a case as stored, with its delivery and lifecycle.
//
// It embeds [supportcore.Case], so it formats and logs without its text or
// address.
type Case struct {
	supportcore.Case
	ShareReason ShareReason
	Status      Status
	Delivery    supportcore.Delivery // zero until delivered
	DeskURL     string               // a page the requester can follow, if the desk has one

	DeliveredAt      time.Time
	LastReplyAt      time.Time // the last reply sent through supportforge
	ClosedAt         time.Time // when the desk reported the ticket closed
	StatusCheckedAt  time.Time // when the desk was last asked for status
	BodyClearedAt    time.Time
	ReplyToClearedAt time.Time
}

// Entry is an outbox entry: a reference to a case waiting for delivery. It
// holds no personal data.
type Entry struct {
	ID            string
	CaseID        string
	Desk          int // index of the desk being tried, in the configured order
	Attempts      int // attempts at that desk
	NextAttemptAt time.Time
	LeaseUntil    time.Time // claimed by a worker until then
	LastError     string    // the class of the last failure, never its text
}

// Page selects part of a list.
type Page struct {
	Limit  int // at most this many; zero means the store's default
	Offset int
}

// Store keeps cases and their outbox entries. Implementations are safe for
// concurrent use, including by several processes.
type Store interface {
	// Create stores c and its first outbox entry e in one transaction. It
	// returns ErrDuplicate if a case with c's ID or reference exists.
	Create(ctx context.Context, c Case, e Entry) error

	// Case returns the case with the given ID.
	Case(ctx context.Context, id string) (Case, error)

	// CaseByReference returns the case with the given reference.
	CaseByReference(ctx context.Context, ref string) (Case, error)

	// Update loads a case, applies fn and saves the result, atomically. An
	// error from fn is returned and nothing is saved.
	Update(ctx context.Context, id string, fn func(*Case) error) error

	// Claim leases up to limit entries that are due at now and not leased,
	// until now plus lease, oldest due first. Two concurrent claims never
	// return the same entry.
	Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Entry, error)

	// Reschedule saves e's desk, attempts, next attempt time and last error,
	// and releases its lease.
	Reschedule(ctx context.Context, e Entry) error

	// Delivered records d on the entry's case, sets its status to delivered
	// and its delivery time to at, and deletes the entry, in one transaction.
	Delivered(ctx context.Context, entryID string, d supportcore.Delivery, at time.Time) error

	// ByPrincipal lists a principal's cases, newest first.
	ByPrincipal(ctx context.Context, principalID string, p Page) ([]Case, error)

	// StatusDue lists delivered cases that are not closed and were delivered
	// to one of desks, least recently checked first.
	StatusDue(ctx context.Context, desks []string, limit int) ([]Case, error)
}
