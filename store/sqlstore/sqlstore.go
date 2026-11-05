// Package sqlstore is a [store.Store] on PostgreSQL or SQLite, built with
// Ent. Its tables are supportforge_cases and supportforge_outbox, so it can
// share a database with the host application.
//
// PostgreSQL is the production database: several processes can run the
// worker, and a claim locks its rows with FOR UPDATE SKIP LOCKED. SQLite is
// for development and tests. It allows one writer at a time, so open it
// with foreign keys on and, for concurrent use, a single connection or
// immediate transactions:
//
//	file:support.db?_fk=1&_txlock=immediate
//
// On a shared PostgreSQL server, run [Store.Migrate] once per deploy as the
// database's owner role, then [Store.GrantTo] for the role the application
// connects as, which owns nothing and gets only the rights it needs.
//
// Times are stored in UTC. PostgreSQL keeps microseconds, so a time read
// back may be truncated below that.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	supportcore "github.com/plexusone/supportforge-core"

	"github.com/plexusone/supportforge/store"
	"github.com/plexusone/supportforge/store/sqlstore/internal/ent"
	"github.com/plexusone/supportforge/store/sqlstore/internal/ent/outboxentry"
	"github.com/plexusone/supportforge/store/sqlstore/internal/ent/supportcase"
)

// Dialects the store supports, as Ent names them.
const (
	Postgres = dialect.Postgres
	SQLite   = dialect.SQLite
)

const defaultLimit = 50

// Store is a [store.Store] over a SQL database.
type Store struct {
	db      *sql.DB
	client  *ent.Client
	dialect string
}

var _ store.Store = (*Store)(nil)

// New returns a store over db, which the caller opens and closes. The
// dialect is [Postgres] or [SQLite]. Call [Store.Migrate] before first use.
func New(db *sql.DB, dialectName string) (*Store, error) {
	if db == nil {
		return nil, errors.New("sqlstore: nil database")
	}
	switch dialectName {
	case Postgres, SQLite:
	default:
		return nil, fmt.Errorf("sqlstore: unsupported dialect %q", dialectName)
	}
	drv := entsql.OpenDB(dialectName, db)
	return &Store{db: db, client: ent.NewClient(ent.Driver(drv)), dialect: dialectName}, nil
}

// Migrate creates the store's tables and indexes, or adds what a newer
// version needs. It touches no other table.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.client.Schema.Create(ctx); err != nil {
		return fmt.Errorf("sqlstore: migrate: %w", err)
	}
	return nil
}

// tables are the store's tables, which [Store.GrantTo] grants on.
var tables = []string{CasesTable, OutboxTable}

// roleName is a PostgreSQL role name the store accepts: unquoted-identifier
// characters only, so it is safe to quote into a statement.
var roleName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// GrantTo gives role SELECT, INSERT, UPDATE and DELETE on the store's
// tables, and nothing else: no DDL, no other table. Run it as the owner
// after [Store.Migrate]. It is safe to repeat. PostgreSQL only.
func (s *Store) GrantTo(ctx context.Context, role string) error {
	if s.dialect != Postgres {
		return fmt.Errorf("sqlstore: grants need PostgreSQL, not %s", s.dialect)
	}
	if !roleName.MatchString(role) {
		return fmt.Errorf("sqlstore: invalid role name %q", role)
	}
	for _, t := range tables {
		stmt := fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON %s TO "%s"`, t, role) //nolint:gosec // G201: GRANT takes no parameters; the table is a constant and the role matches roleName
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlstore: grant on %s to %s: %w", t, role, err)
		}
	}
	return nil
}

// Create implements [store.Store].
func (s *Store) Create(ctx context.Context, c store.Case, e store.Entry) error {
	if e.CaseID != c.ID {
		return fmt.Errorf("sqlstore: entry %s is for case %s, not %s", e.ID, e.CaseID, c.ID)
	}
	return s.inTx(ctx, func(tx *ent.Tx) error {
		create := tx.SupportCase.Create().
			SetID(c.ID).
			SetReference(c.Reference).
			SetProduct(c.Product).
			SetKind(string(c.Kind)).
			SetTitle(c.Title).
			SetBody(c.Body).
			SetPage(c.Page).
			SetReplyWanted(c.ReplyWanted).
			SetShared(c.Shared).
			SetPrincipalID(c.Requester.PrincipalID).
			SetOrganizationID(c.Requester.OrganizationID).
			SetReplyTo(c.Requester.ReplyTo).
			SetCreatedAt(c.CreatedAt.UTC()).
			SetShareReason(string(c.ShareReason)).
			SetStatus(string(c.Status)).
			SetDeliveryDesk(c.Delivery.Desk).
			SetDeliveryTicketID(c.Delivery.TicketID).
			SetDeskURL(c.DeskURL).
			SetNillableDeliveredAt(nullable(c.DeliveredAt)).
			SetNillableLastReplyAt(nullable(c.LastReplyAt)).
			SetNillableClosedAt(nullable(c.ClosedAt)).
			SetNillableStatusCheckedAt(nullable(c.StatusCheckedAt)).
			SetNillableBodyClearedAt(nullable(c.BodyClearedAt)).
			SetNillableReplyToClearedAt(nullable(c.ReplyToClearedAt))
		if err := create.Exec(ctx); err != nil {
			return writeErr("case "+c.ID, err)
		}
		err := tx.OutboxEntry.Create().
			SetID(e.ID).
			SetCaseID(c.ID).
			SetDesk(e.Desk).
			SetAttempts(e.Attempts).
			SetNextAttemptAt(e.NextAttemptAt.UTC()).
			SetNillableLeaseUntil(nullable(e.LeaseUntil)).
			SetLastError(e.LastError).
			Exec(ctx)
		if err != nil {
			return writeErr("entry "+e.ID, err)
		}
		return nil
	})
}

// Case implements [store.Store].
func (s *Store) Case(ctx context.Context, id string) (store.Case, error) {
	row, err := s.client.SupportCase.Get(ctx, id)
	if err != nil {
		return store.Case{}, readErr("case "+id, err)
	}
	return toCase(row), nil
}

// CaseByReference implements [store.Store].
func (s *Store) CaseByReference(ctx context.Context, ref string) (store.Case, error) {
	row, err := s.client.SupportCase.Query().Where(supportcase.Reference(ref)).Only(ctx)
	if err != nil {
		return store.Case{}, readErr("reference "+ref, err)
	}
	return toCase(row), nil
}

// Update implements [store.Store].
func (s *Store) Update(ctx context.Context, id string, fn func(*store.Case) error) error {
	return s.inTx(ctx, func(tx *ent.Tx) error {
		q := tx.SupportCase.Query().Where(supportcase.ID(id))
		if s.dialect == Postgres {
			q = q.ForUpdate()
		}
		row, err := q.Only(ctx)
		if err != nil {
			return readErr("case "+id, err)
		}
		cur := toCase(row)
		next := cur
		if err := fn(&next); err != nil {
			return err
		}
		if next.ID != cur.ID || next.Reference != cur.Reference {
			return fmt.Errorf("sqlstore: case %s: the ID and reference cannot change", id)
		}
		u := tx.SupportCase.UpdateOneID(id).
			SetProduct(next.Product).
			SetKind(string(next.Kind)).
			SetTitle(next.Title).
			SetBody(next.Body).
			SetPage(next.Page).
			SetReplyWanted(next.ReplyWanted).
			SetShared(next.Shared).
			SetPrincipalID(next.Requester.PrincipalID).
			SetOrganizationID(next.Requester.OrganizationID).
			SetReplyTo(next.Requester.ReplyTo).
			SetCreatedAt(next.CreatedAt.UTC()).
			SetShareReason(string(next.ShareReason)).
			SetStatus(string(next.Status)).
			SetDeliveryDesk(next.Delivery.Desk).
			SetDeliveryTicketID(next.Delivery.TicketID).
			SetDeskURL(next.DeskURL)
		setTime(next.DeliveredAt, u.SetDeliveredAt, u.ClearDeliveredAt)
		setTime(next.LastReplyAt, u.SetLastReplyAt, u.ClearLastReplyAt)
		setTime(next.ClosedAt, u.SetClosedAt, u.ClearClosedAt)
		setTime(next.StatusCheckedAt, u.SetStatusCheckedAt, u.ClearStatusCheckedAt)
		setTime(next.BodyClearedAt, u.SetBodyClearedAt, u.ClearBodyClearedAt)
		setTime(next.ReplyToClearedAt, u.SetReplyToClearedAt, u.ClearReplyToClearedAt)
		if err := u.Exec(ctx); err != nil {
			return fmt.Errorf("sqlstore: update case %s: %w", id, err)
		}
		return nil
	})
}

// Claim implements [store.Store].
func (s *Store) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]store.Entry, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	now = now.UTC()
	var out []store.Entry
	err := s.inTx(ctx, func(tx *ent.Tx) error {
		q := tx.OutboxEntry.Query().
			Where(
				outboxentry.NextAttemptAtLTE(now),
				outboxentry.Or(outboxentry.LeaseUntilIsNil(), outboxentry.LeaseUntilLTE(now)),
			).
			Order(outboxentry.ByNextAttemptAt(), outboxentry.ByID()).
			Limit(limit)
		if s.dialect == Postgres {
			q = q.ForUpdate(entsql.WithLockAction(entsql.SkipLocked))
		}
		rows, err := q.All(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: claim: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		until := now.Add(lease)
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		if err := tx.OutboxEntry.Update().Where(outboxentry.IDIn(ids...)).SetLeaseUntil(until).Exec(ctx); err != nil {
			return fmt.Errorf("sqlstore: claim: %w", err)
		}
		out = make([]store.Entry, len(rows))
		for i, r := range rows {
			out[i] = toEntry(r)
			out[i].LeaseUntil = until
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Reschedule implements [store.Store].
func (s *Store) Reschedule(ctx context.Context, e store.Entry) error {
	err := s.client.OutboxEntry.UpdateOneID(e.ID).
		SetDesk(e.Desk).
		SetAttempts(e.Attempts).
		SetNextAttemptAt(e.NextAttemptAt.UTC()).
		SetLastError(e.LastError).
		ClearLeaseUntil().
		Exec(ctx)
	if err != nil {
		return readErr("entry "+e.ID, err)
	}
	return nil
}

// Delivered implements [store.Store].
func (s *Store) Delivered(ctx context.Context, entryID string, d supportcore.Delivery, at time.Time) error {
	return s.inTx(ctx, func(tx *ent.Tx) error {
		q := tx.OutboxEntry.Query().Where(outboxentry.ID(entryID))
		if s.dialect == Postgres {
			q = q.ForUpdate()
		}
		e, err := q.Only(ctx)
		if err != nil {
			return readErr("entry "+entryID, err)
		}
		n, err := tx.SupportCase.Update().
			Where(supportcase.ID(e.CaseID)).
			SetDeliveryDesk(d.Desk).
			SetDeliveryTicketID(d.TicketID).
			SetStatus(string(store.StatusDelivered)).
			SetDeliveredAt(at.UTC()).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("sqlstore: deliver case %s: %w", e.CaseID, err)
		}
		if n == 0 {
			return fmt.Errorf("case %s: %w", e.CaseID, store.ErrNotFound)
		}
		if err := tx.OutboxEntry.DeleteOneID(entryID).Exec(ctx); err != nil {
			return fmt.Errorf("sqlstore: delete entry %s: %w", entryID, err)
		}
		return nil
	})
}

// ByPrincipal implements [store.Store].
func (s *Store) ByPrincipal(ctx context.Context, principalID string, p store.Page) ([]store.Case, error) {
	if principalID == "" {
		return nil, nil // visitors have no list
	}
	limit := p.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	rows, err := s.client.SupportCase.Query().
		Where(supportcase.PrincipalID(principalID)).
		Order(supportcase.ByCreatedAt(entsql.OrderDesc()), supportcase.ByID()).
		Offset(max(p.Offset, 0)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list cases: %w", err)
	}
	return toCases(rows), nil
}

// StatusDue implements [store.Store].
func (s *Store) StatusDue(ctx context.Context, desks []string, limit int) ([]store.Case, error) {
	if len(desks) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	rows, err := s.client.SupportCase.Query().
		Where(
			supportcase.DeliveredAtNotNil(),
			supportcase.StatusNEQ(string(store.StatusClosed)),
			supportcase.DeliveryDeskIn(desks...),
		).
		Order(supportcase.ByStatusCheckedAt(entsql.OrderNullsFirst()), supportcase.ByID()).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: status due: %w", err)
	}
	return toCases(rows), nil
}

// inTx runs fn in a transaction, committing if it returns nil.
func (s *Store) inTx(ctx context.Context, fn func(*ent.Tx) error) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("sqlstore: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil {
			return errors.Join(err, fmt.Errorf("sqlstore: rollback: %w", rerr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlstore: commit: %w", err)
	}
	return nil
}

func readErr(what string, err error) error {
	if ent.IsNotFound(err) {
		return fmt.Errorf("%s: %w", what, store.ErrNotFound)
	}
	return fmt.Errorf("sqlstore: %s: %w", what, err)
}

func writeErr(what string, err error) error {
	if ent.IsConstraintError(err) {
		return fmt.Errorf("%s: %w", what, store.ErrDuplicate)
	}
	return fmt.Errorf("sqlstore: create %s: %w", what, err)
}

// nullable is nil for the zero time, so that it is stored as NULL.
func nullable(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func setTime[U any](t time.Time, set func(time.Time) U, clear func() U) {
	if t.IsZero() {
		clear()
		return
	}
	set(t.UTC())
}

func value(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func toCase(r *ent.SupportCase) store.Case {
	return store.Case{
		Case: supportcore.Case{
			ID:          r.ID,
			Reference:   r.Reference,
			Product:     r.Product,
			Kind:        supportcore.Kind(r.Kind),
			Title:       r.Title,
			Body:        r.Body,
			Page:        r.Page,
			ReplyWanted: r.ReplyWanted,
			Shared:      r.Shared,
			Requester: supportcore.Requester{
				PrincipalID:    r.PrincipalID,
				OrganizationID: r.OrganizationID,
				ReplyTo:        r.ReplyTo,
			},
			CreatedAt: r.CreatedAt.UTC(),
		},
		ShareReason:      store.ShareReason(r.ShareReason),
		Status:           store.Status(r.Status),
		Delivery:         supportcore.Delivery{Desk: r.DeliveryDesk, TicketID: r.DeliveryTicketID},
		DeskURL:          r.DeskURL,
		DeliveredAt:      value(r.DeliveredAt),
		LastReplyAt:      value(r.LastReplyAt),
		ClosedAt:         value(r.ClosedAt),
		StatusCheckedAt:  value(r.StatusCheckedAt),
		BodyClearedAt:    value(r.BodyClearedAt),
		ReplyToClearedAt: value(r.ReplyToClearedAt),
	}
}

func toCases(rows []*ent.SupportCase) []store.Case {
	out := make([]store.Case, len(rows))
	for i, r := range rows {
		out[i] = toCase(r)
	}
	return out
}

func toEntry(r *ent.OutboxEntry) store.Entry {
	return store.Entry{
		ID:            r.ID,
		CaseID:        r.CaseID,
		Desk:          r.Desk,
		Attempts:      r.Attempts,
		NextAttemptAt: r.NextAttemptAt.UTC(),
		LeaseUntil:    value(r.LeaseUntil),
		LastError:     r.LastError,
	}
}
