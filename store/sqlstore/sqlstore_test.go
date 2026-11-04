package sqlstore_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
	supportcore "github.com/plexusone/supportforge-core"

	"github.com/plexusone/supportforge/store"
	"github.com/plexusone/supportforge/store/sqlstore"
	"github.com/plexusone/supportforge/store/storetest"
)

// envPostgres names an admin DSN that may create databases. The PostgreSQL
// tests are skipped when it is unset.
const envPostgres = "SUPPORTFORGE_TEST_PG_DSN"

// envPostgresRequired makes a missing DSN a failure instead of a skip, so
// CI cannot pass without running the PostgreSQL tests.
const envPostgresRequired = "SUPPORTFORGE_TEST_PG_REQUIRED"

func randomName(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return prefix + hex.EncodeToString(b)
}

// newSQLite returns a migrated store over a private in-memory database.
func newSQLite(t *testing.T) *sqlstore.Store {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", randomName("sf"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // SQLite has one writer; this serializes transactions
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing the database: %v", err)
		}
	})
	return migrated(t, db, sqlstore.SQLite)
}

func migrated(t *testing.T, db *sql.DB, dialect string) *sqlstore.Store {
	t.Helper()
	s, err := sqlstore.New(db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLiteConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return newSQLite(t) })
}

// TestPostgresConformance runs the suite in a scratch database the way a
// shared server runs it: the owner migrates and grants, and the store
// connects as an app role that owns nothing. Tables are emptied before each
// check.
func TestPostgresConformance(t *testing.T) {
	sc := scratchPostgres(t)
	owner := migrated(t, sc.owner, sqlstore.Postgres)
	if err := owner.GrantTo(t.Context(), sc.appRole); err != nil {
		t.Fatal(err)
	}
	app, err := sqlstore.New(sc.app, sqlstore.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		if _, err := sc.owner.ExecContext(t.Context(), "TRUNCATE supportforge_outbox, supportforge_cases"); err != nil {
			t.Fatal(err)
		}
		return app
	})
	// The app role can use the tables but not change them.
	if _, err := sc.app.ExecContext(t.Context(), "ALTER TABLE supportforge_cases ADD COLUMN x int"); err == nil {
		t.Error("the app role altered a table")
	}
	if _, err := sc.app.ExecContext(t.Context(), "CREATE TABLE supportforge_other (id int)"); err == nil {
		t.Error("the app role created a table")
	}
}

func TestGrantToRejectsBadInput(t *testing.T) {
	if err := newSQLite(t).GrantTo(t.Context(), "app"); err == nil {
		t.Error("GrantTo on SQLite succeeded")
	}
	sc := scratchPostgres(t)
	s := migrated(t, sc.owner, sqlstore.Postgres)
	for _, role := range []string{"", "App", `x"; DROP TABLE supportforge_cases; --`} {
		if err := s.GrantTo(t.Context(), role); err == nil {
			t.Errorf("GrantTo(%q) succeeded", role)
		}
	}
}

// scratch is a throwaway PostgreSQL database with an app role.
type scratch struct {
	owner   *sql.DB // the admin, standing in for the owner role
	app     *sql.DB // the app role, which owns nothing
	appRole string
}

// scratchPostgres creates a database and a login role for the test, and
// drops both afterwards. It skips when no server is configured.
func scratchPostgres(t *testing.T) scratch {
	t.Helper()
	adminDSN := os.Getenv(envPostgres)
	if adminDSN == "" {
		if os.Getenv(envPostgresRequired) != "" {
			t.Fatalf("%s is set but %s is not", envPostgresRequired, envPostgres)
		}
		t.Skipf("%s is not set", envPostgres)
	}
	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	name := randomName("supportforge_test_")
	role := name + "_app"
	password := randomName("p")
	t.Cleanup(func() {
		for _, stmt := range []string{
			"DROP DATABASE IF EXISTS " + name + " WITH (FORCE)",
			"DROP ROLE IF EXISTS " + role,
		} {
			if _, err := adminDB.Exec(stmt); err != nil {
				t.Errorf("%s: %v", stmt, err)
			}
		}
		if err := adminDB.Close(); err != nil {
			t.Errorf("closing the admin connection: %v", err)
		}
	})
	for _, stmt := range []string{
		"CREATE DATABASE " + name,
		"CREATE ROLE " + role + " LOGIN PASSWORD '" + password + "'",
	} {
		if _, err := adminDB.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	owner := open(t, u.String())
	// PostgreSQL 15 and later already deny it; older servers allow it.
	if _, err := owner.ExecContext(t.Context(), "REVOKE CREATE ON SCHEMA public FROM PUBLIC"); err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, password)
	return scratch{owner: owner, app: open(t, u.String()), appRole: role}
}

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing a scratch connection: %v", err)
		}
	})
	return db
}

func TestNewRejectsBadArguments(t *testing.T) {
	if _, err := sqlstore.New(nil, sqlstore.Postgres); err == nil {
		t.Error("New(nil) succeeded")
	}
	db, err := sql.Open("sqlite3", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // an unused in-memory database
	if _, err := sqlstore.New(db, "mysql"); err == nil {
		t.Error("New with an unsupported dialect succeeded")
	}
}

func TestMigrateIsRepeatable(t *testing.T) {
	s := newSQLite(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("a second Migrate: %v", err)
	}
}

func TestCreateRejectsAnEntryForAnotherCase(t *testing.T) {
	s := newSQLite(t)
	c := store.Case{Case: supportcore.Case{ID: "c1", Reference: "SR-c1", CreatedAt: time.Now()}}
	if err := s.Create(t.Context(), c, store.Entry{ID: "e1", CaseID: "c2"}); err == nil {
		t.Fatal("Create with a mismatched entry succeeded")
	}
	if _, err := s.Case(t.Context(), "c1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the rejected case was stored: %v", err)
	}
}

func TestUpdateKeepsTheIDAndReference(t *testing.T) {
	s := newSQLite(t)
	c := store.Case{Case: supportcore.Case{ID: "c1", Reference: "SR-c1", CreatedAt: time.Now()}}
	if err := s.Create(t.Context(), c, store.Entry{ID: "e1", CaseID: "c1"}); err != nil {
		t.Fatal(err)
	}
	err := s.Update(t.Context(), "c1", func(c *store.Case) error {
		c.Reference = "SR-other"
		return nil
	})
	if err == nil {
		t.Fatal("Update changed the reference")
	}
	got, err := s.CaseByReference(t.Context(), "SR-c1")
	if err != nil || got.ID != "c1" {
		t.Errorf("CaseByReference after a refused update = %v, %v", got.ID, err)
	}
}
