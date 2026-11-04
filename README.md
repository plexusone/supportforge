# SupportForge

Support requests and feedback for Go applications: accept a request from a
signed-in user or a visitor, store it before anything is sent, and deliver it
to a help desk or a mailbox, with personal-data rules built in.

> **Status:** in development. Intake, the in-memory and SQL stores, delivery
> with retries across desks, acknowledgments, the mail desk and the
> requester's list are built; data governance comes next. See
> the [specifications](docs/specs/initiatives/INIT-SUPPORTFORGE-001/PRD.md).

## What it does

- **Asynchronous support.** The requester is told a reply comes by email. No
  chat and no "online" indicator.
- **No lost requests.** A request and its outbox entry are written in one
  transaction, then a worker delivers it with retries through an ordered list
  of desks, ending with a mailbox.
- **Vendor-neutral.** A help desk is an adapter for the small `Desk`
  interface in [`supportforge-core`](https://github.com/plexusone/supportforge-core),
  which has no dependencies, so an adapter can live anywhere.
- **Personal data with rules.** Every stored field is classified. Message text
  and reply addresses are cleared after delivery. Requests are included in data
  export and erasure, including at the help desk, and a legal hold stops both.
- **Safe for visitors.** Rate limits, a bot challenge, and acknowledgments that
  never repeat a visitor's text.

## Modules

| Module | Purpose |
| --- | --- |
| `github.com/plexusone/supportforge-core` | Types, the `Desk` interface, error classes, and the desk conformance suite |
| `github.com/plexusone/supportforge` | Intake, stores, outbox and worker, retention, export and erasure, and the mailbox desk |

## Stores

| Package | Use |
| --- | --- |
| `store/sqlstore` | Production. PostgreSQL (or SQLite for development), built with Ent, in tables named `supportforge_*` so it can share the host's database. Several processes can run the worker: a claim locks its rows with `FOR UPDATE SKIP LOCKED` |
| `store/memstore` | Tests, and running without a database |

On a shared PostgreSQL server, migrate once per deploy as the database's
owner role, and grant the role the application connects as only what it
needs:

```go
// Deploy step, connected as the owner.
owner, err := sqlstore.New(ownerDB, sqlstore.Postgres)
if err != nil {
	return err
}
if err := owner.Migrate(ctx); err != nil { // creates or updates only supportforge_* tables
	return err
}
if err := owner.GrantTo(ctx, "app_role"); err != nil { // SELECT, INSERT, UPDATE, DELETE
	return err
}

// The application, connected as the app role.
st, err := sqlstore.New(appDB, sqlstore.Postgres)
if err != nil {
	return err
}
svc, err := supportforge.New(supportforge.Config{Store: st /* , ... */})
```

Every store passes the same conformance suite, `store/storetest`. The
PostgreSQL run needs a server: set `SUPPORTFORGE_TEST_PG_DSN` to an admin
DSN that may create databases and roles. It runs the suite as an app role
that owns nothing.

## License

MIT
