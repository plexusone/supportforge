# SupportForge

Support requests and feedback for Go applications: accept a request from a
signed-in user or a visitor, store it before anything is sent, and deliver it
to a help desk or a mailbox, with personal-data rules built in.

> **Status:** in development. Intake, the in-memory store, delivery with
> retries across desks, acknowledgments, the mail desk and the requester's
> list are built; data governance and the PostgreSQL store come next. See
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

## License

MIT
