// Package memstore is an in-memory [store.Store] for tests and for running
// without a database.
package memstore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	supportcore "github.com/plexusone/supportforge-core"
	"github.com/plexusone/supportforge/store"
)

const defaultLimit = 50

// Store is an in-memory [store.Store]. The zero value is not usable; call
// [New].
type Store struct {
	mu      sync.Mutex
	cases   map[string]*store.Case
	refs    map[string]string // reference to case ID
	entries map[string]*store.Entry
}

var _ store.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	return &Store{cases: map[string]*store.Case{}, refs: map[string]string{}, entries: map[string]*store.Entry{}}
}

// Create implements [store.Store].
func (s *Store) Create(ctx context.Context, c store.Case, e store.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cases[c.ID]; ok {
		return fmt.Errorf("case %s: %w", c.ID, store.ErrDuplicate)
	}
	if _, ok := s.refs[c.Reference]; ok {
		return fmt.Errorf("reference %s: %w", c.Reference, store.ErrDuplicate)
	}
	if _, ok := s.entries[e.ID]; ok {
		return fmt.Errorf("entry %s: %w", e.ID, store.ErrDuplicate)
	}
	s.cases[c.ID] = &c
	s.refs[c.Reference] = c.ID
	s.entries[e.ID] = &e
	return nil
}

// Case implements [store.Store].
func (s *Store) Case(ctx context.Context, id string) (store.Case, error) {
	if err := ctx.Err(); err != nil {
		return store.Case{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return store.Case{}, fmt.Errorf("case %s: %w", id, store.ErrNotFound)
	}
	return *c, nil
}

// CaseByReference implements [store.Store].
func (s *Store) CaseByReference(ctx context.Context, ref string) (store.Case, error) {
	if err := ctx.Err(); err != nil {
		return store.Case{}, err
	}
	s.mu.Lock()
	id, ok := s.refs[ref]
	s.mu.Unlock()
	if !ok {
		return store.Case{}, fmt.Errorf("reference %s: %w", ref, store.ErrNotFound)
	}
	return s.Case(ctx, id)
}

// Update implements [store.Store].
func (s *Store) Update(ctx context.Context, id string, fn func(*store.Case) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return fmt.Errorf("case %s: %w", id, store.ErrNotFound)
	}
	next := *c
	if err := fn(&next); err != nil {
		return err
	}
	if next.ID != c.ID || next.Reference != c.Reference {
		return fmt.Errorf("case %s: the ID and reference cannot change", id)
	}
	*c = next
	return nil
}

// Claim implements [store.Store].
func (s *Store) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]store.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []*store.Entry
	for _, e := range s.entries {
		if !e.NextAttemptAt.After(now) && !e.LeaseUntil.After(now) {
			due = append(due, e)
		}
	}
	slices.SortFunc(due, func(a, b *store.Entry) int {
		return cmp.Or(a.NextAttemptAt.Compare(b.NextAttemptAt), cmp.Compare(a.ID, b.ID))
	})
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]store.Entry, len(due))
	for i, e := range due {
		e.LeaseUntil = now.Add(lease)
		out[i] = *e
	}
	return out, nil
}

// Reschedule implements [store.Store].
func (s *Store) Reschedule(ctx context.Context, e store.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.entries[e.ID]
	if !ok {
		return fmt.Errorf("entry %s: %w", e.ID, store.ErrNotFound)
	}
	cur.Desk = e.Desk
	cur.Attempts = e.Attempts
	cur.NextAttemptAt = e.NextAttemptAt
	cur.LastError = e.LastError
	cur.LeaseUntil = time.Time{}
	return nil
}

// Delivered implements [store.Store].
func (s *Store) Delivered(ctx context.Context, entryID string, d supportcore.Delivery, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[entryID]
	if !ok {
		return fmt.Errorf("entry %s: %w", entryID, store.ErrNotFound)
	}
	c, ok := s.cases[e.CaseID]
	if !ok {
		return fmt.Errorf("case %s: %w", e.CaseID, store.ErrNotFound)
	}
	c.Delivery = d
	c.Status = store.StatusDelivered
	c.DeliveredAt = at
	delete(s.entries, entryID)
	return nil
}

// ByPrincipal implements [store.Store].
func (s *Store) ByPrincipal(ctx context.Context, principalID string, p store.Page) ([]store.Case, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Case
	for _, c := range s.cases {
		if principalID != "" && c.Requester.PrincipalID == principalID {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b store.Case) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return page(out, p), nil
}

// StatusDue implements [store.Store].
func (s *Store) StatusDue(ctx context.Context, desks []string, limit int) ([]store.Case, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Case
	for _, c := range s.cases {
		if !c.DeliveredAt.IsZero() && c.Status != store.StatusClosed && slices.Contains(desks, c.Delivery.Desk) {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b store.Case) int {
		return cmp.Or(a.StatusCheckedAt.Compare(b.StatusCheckedAt), cmp.Compare(a.ID, b.ID))
	})
	return page(out, store.Page{Limit: limit}), nil
}

func page(cs []store.Case, p store.Page) []store.Case {
	limit := p.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if p.Offset >= len(cs) {
		return nil
	}
	cs = cs[max(p.Offset, 0):]
	if len(cs) > limit {
		cs = cs[:limit]
	}
	return cs
}
