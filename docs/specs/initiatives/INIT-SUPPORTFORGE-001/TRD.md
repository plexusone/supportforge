# supportforge: Technical Requirements

**Initiative:** `INIT-SUPPORTFORGE-001`
**Repository:** `github.com/plexusone/supportforge` (with `github.com/plexusone/supportforge-core`)

Implements the [PRD](PRD.md). The [roadmap](ROADMAP.md) holds the work items.

## Two modules

| Module | Holds | Imports |
| --- | --- | --- |
| `supportforge-core` | The case types, the `Desk` interface and its optional extensions, error classes, and the desk conformance suite | The standard library only |
| `supportforge` | Intake, the stores, the outbox and worker, the retention job, export and erasure, the mailbox desk, and the data classification | `supportforge-core`, `omnimail`, Ent, `grokify/datacatalog` |

A desk adapter imports only `supportforge-core`, so it adds no database or
mail dependency to whatever module holds it. An adapter can live in a vendor
SDK's repository, in an application, or in a private repository. This
repository ships only the mailbox desk and an in-memory desk for tests.

```text
application ──► host platform (session, authorization, audit, governance)
                       │
                       ▼
                 supportforge ──► store (cases, outbox)
                       │
                       ▼ worker
                 supportforge-core.Desk
                 ├── mailbox desk (omnimail)        in this repository
                 └── vendor desks                    elsewhere
```

## supportforge-core

```go
package supportcore

type Kind string // question, problem, billing, feedback, other

type Requester struct {
    PrincipalID    string // empty for a visitor
    OrganizationID string // empty for a visitor
    ReplyTo        string // email address; may be empty for feedback with no reply wanted
}

type Case struct {
    ID          string
    Reference   string // short, shown to the requester
    Product     string
    Kind        Kind
    Title       string
    Body        string
    Page        string // a path only
    ReplyWanted bool
    Shared      bool // visible in full to the requester's organization administrators
    Requester   Requester
    CreatedAt   time.Time
}

// Acknowledgment is the first message to the requester, rendered by
// supportforge from the application's template.
type Acknowledgment struct {
    Subject string
    Body    string // Markdown; never contains a visitor's text
}

// Context is shown to responders only.
type Context map[string]string

type Delivery struct {
    Desk     string // the desk's name
    TicketID string // the desk's own identifier
}

type Desk interface {
    Name() string
    Deliver(ctx context.Context, c Case, ack Acknowledgment, private Context) (Delivery, error)
}

// Optional extensions, discovered by type assertion.
type TicketEraser interface {
    EraseTicket(ctx context.Context, d Delivery) error
}
type TicketExporter interface {
    ExportTicket(ctx context.Context, d Delivery) (Transcript, error)
}
```

- **Transcript.** `ExportTicket` returns a `Transcript`: the `Delivery` and
  its messages, each with an author (`requester`, `responder` or `note`), a
  body and a time.
- **Error classes.** A desk returns errors wrapping `ErrRetryable` (try
  again later; `Retry(err, after)` carries a vendor's retry-after),
  `ErrCredential` (stop and alert), `ErrRejected` (this case cannot be
  delivered here; move to the next desk) or `ErrUnsupported`. `Classify`
  returns an error's class for the worker.
- **No leaks through formatting.** `Case` and `Requester` format and log
  without the title, body or reply address.
- **Conformance suite.** `desktest.Run` runs any `Desk` against a test double
  of its vendor, which reports what was sent to the requester and what
  responders see, and simulates vendor failures. It checks:
  - delivery returns this desk's name and a distinct ticket ID;
  - the requester is sent the acknowledgment given;
  - the requester is sent nothing else: not the title, body or context, for
    a visitor or a signed-in requester;
  - responders see the title, body and context;
  - with no reply address, nothing is sent, or the case is rejected;
  - vendor failures and a canceled context are classified;
  - erasure, when offered, removes the ticket and can be repeated;
  - export, when offered, returns the conversation.
- **Requester-facing extensions,** also optional and discovered by type
  assertion:

  ```go
  type TicketLinker interface {
      // TicketURL returns a page the requester can sign in to, or "" if none.
      TicketURL(ctx context.Context, d Delivery) (string, error)
  }
  type TicketStatuser interface {
      TicketStatus(ctx context.Context, d Delivery) (Status, error) // open, pending or closed
  }
  ```

  A third, `TicketReplier`, sends a reply into the ticket's conversation:

  ```go
  type TicketReplier interface {
      Reply(ctx context.Context, d Delivery, body string) error
  }
  ```

  The suite checks them where implemented. A desk without `TicketLinker`
  gives no link; one without `TicketStatuser` leaves the status at
  `delivered`.
- **In-memory desk.** `memdesk` passes the suite and is the desk the host's
  own tests use.
- **Stability.** The module is versioned on its own and changes rarely. A
  breaking change is a new major version.

## supportforge

### Intake

```go
type Service struct { /* stores, desks, ports */ }

func (s *Service) Submit(ctx context.Context, r Request) (Receipt, error)
```

- The host passes a `Request` with the requester already resolved: from the
  session for a signed-in user, or from the form plus a verified challenge for
  a visitor. supportforge does not read sessions.
- Validation:
  - kind is one of the five;
  - the title is at most 200 characters, the body at most 10,000;
  - the page is a path, with any query string or fragment removed;
  - a visitor needs a reply address unless the kind is `feedback` with no
    reply wanted.
- Ports the host implements:
  - `ChallengeVerifier`, for visitors;
  - `RateLimiter`, keyed by principal, or by challenge token for visitors;
  - `Observer`, which receives a `case_created` notice with kind and
    product, and no text.
- `Submit` writes the case and an outbox entry in one transaction and returns
  a `Receipt`, which holds the reference. Nothing is sent before the commit.

### Organization view

```go
func (s *Service) OrganizationIndex(ctx context.Context, org string, page Page) ([]IndexEntry, error)
func (s *Service) OrganizationCase(ctx context.Context, org, ref string) (Case, error)
```

- **The index** carries the reference, requester principal, kind, product,
  status and times, and never the title, body or reply address.
- **The full case** is returned only when the case is shared, or when its
  kind is `billing`. Otherwise it returns `ErrNotShared`.
- **`Shared` is set at intake.** It comes from the requester's choice, from a
  `SharingPolicy` port (the host's organization setting), or from the kind
  `billing`, and is stored so the rule that applied can be shown later. A
  requester may unshare their own request, but not a billing one.
- **Authorization is the host's.** supportforge does not decide who is an
  organization administrator; the host calls these methods only for owners
  and admins.

### Requester's list

```go
func (s *Service) RequesterCases(ctx context.Context, principal string, page Page) ([]ListEntry, error)
```

- Returns the principal's cases with reference, kind, product, title,
  status, times and the desk link.
- **Status** is `received` before delivery and `delivered` after. A status
  job asks desks that implement `TicketStatuser` for `open`, `pending` or
  `closed`, at a bounded rate, and stores the answer; the list never calls a
  desk.
- **The desk link** is fetched once after delivery from a `TicketLinker` and
  stored.

### Drafted replies

```go
func (s *Service) DraftReply(ctx context.Context, ref, author, body string) (Draft, error)
func (s *Service) ApproveReply(ctx context.Context, draftID, approver string, version int) error
```

- A draft holds the case, its author, the text and a version that changes
  with every edit. Its text is classified like the case's body.
- **Approval names the version,** so an edit after approval needs a new
  one. The approver must differ from the author. Whether the approver may
  approve is the host's decision; supportforge only records it.
- **Approval queues the send** through the outbox, which calls the desk's
  `TicketReplier`. A desk without one gets the draft as a note where it can,
  and the draft is marked for a person to send at the desk.
- The draft's text is cleared once it is sent or discarded. The record of
  author, approver and times stays with the case.
- **The mail desk** replies to the case's reply address. Each reply resets
  the clock on that address, so it is kept while the conversation continues.
  Once it is cleared, a later reply goes out from the operator's mailbox.

### Outbox and worker

- The outbox holds references only: `kind` (`support.deliver`), the case
  ID, attempts, the next attempt time, and the last error class. It holds no
  personal data, so it needs no erasure rules.
- The worker claims due entries with `FOR UPDATE SKIP LOCKED`, so several
  processes can run it.
- Backoff is exponential with jitter, capped at one hour.
- **The desk order is configured, for example `vendor, mailbox`.**
  - `ErrRetryable` retries the same desk until the attempt limit, then
    moves on.
  - `ErrRejected` moves to the next desk at once.
  - `ErrCredential` alerts, then moves on.
  - The last desk is always the mailbox.
- Success records the `Delivery` on the case and removes the outbox entry.
- The outbox is a port (`Queue`). The default is supportforge's own table. A
  host with a general outbox can supply its own.

### The mailbox desk

- It sends the acknowledgment to the requester, and the case with its context
  to the operator's support address, through an `omnimail.Sender`.
- The operator's message sets `Reply-To` to the requester, so replying from a
  mail client answers them.
- It needs only a configured sender and an address. It is the default, so an
  application has working support without a vendor account.

### Acknowledgments

- Templates per product and kind, supplied by the application. Defaults are
  provided.
- **Signed-in requester:** the acknowledgment quotes their title and body.
- **Visitor:** it quotes neither. Their text reaches responders only as
  context. This keeps the form from being a relay for arbitrary mail from
  the operator's domain.
- Every acknowledgment states that the reply comes by email and promises no
  time.

### Storage

Tables are prefixed `supportforge_`, defined with Ent, and migrated by the
host's migration command, as with other platform modules.

`supportforge_cases`, classified field by field in the `grokify/datacatalog`
model:

| Field | Visibility | Tags | Erasure | Retention |
| --- | --- | --- | --- | --- |
| `id`, `reference` | internal | | retain | with the row |
| `principal_id` | internal | pii | tombstone | with the row |
| `organization_id` | internal | | retain | with the row |
| `product`, `kind`, `status`, `reply_wanted`, `shared` | public | | retain | with the row |
| `page` | internal | | retain | with the row |
| `title` | public | pii | clear | with the row; cleared on erasure |
| `body` | public | pii | clear | cleared after delivery plus the clear delay |
| `reply_to` | public | pii | clear | cleared once the conversation is quiet: see the clear job |
| `desk`, `ticket_id`, `desk_url` | internal | | retain | with the row |
| `created_at`, `delivered_at`, `last_reply_at`, `closed_at`, `cleared_at` | internal | | retain | with the row |

The entity's subject key is `principal_id`. Rows are purged after the index
retention, which is the host's policy input. A visitor's row has no subject
key, and once cleared it no longer identifies anyone.

`supportforge_outbox` holds references only, and is not in privacy scope.

The `title`, `body` and `reply_to` columns can be sealed with a host-supplied
`Cryptographer` port, under one purpose, so a database copy does not expose
message text. Clearing then deletes the ciphertext.

### Retention, export and erasure

- **Clear job.** Runs on delivered cases only; undelivered cases are never
  cleared. The clear delay defaults to seven days.
  - **`body`** is cleared the clear delay after delivery.
  - **`reply_to`** is kept while the conversation may continue, and cleared
    the clear delay after the latest of:
    - delivery;
    - the last reply sent through supportforge (`last_reply_at`);
    - closure, for a desk that reports status (`closed_at`). While such a
      desk reports the ticket open, the address is kept.
  - **A ceiling** clears `reply_to` once the case has had no activity for the
    maximum hold (default 90 days), even if the desk still reports it open.
  - **`title`** stays with the row so the requester's list can show it.
- **Holds.** A `HoldChecker` port is consulted before clearing and erasing. A
  held subject's cases are skipped and reported.
- **Export.** `Export(subject)` returns the subject's cases, plus each
  ticket's transcript where the desk implements `TicketExporter`.
- **Erasure.** `Erase(subject)` tombstones `principal_id` and clears the text
  of the subject's cases, and calls `EraseTicket` where the desk implements
  it. Desks without it are reported as retained at the desk, with the desk's
  name, so the operator can act there.
- **Visitor requests.** A request from a visitor to erase their data is
  matched by reply address, for cases not yet cleared, through an
  operator-only call.

### Logging and errors

Logs, errors and audit events carry the case ID, reference, kind, product and
desk, and never title, body, reply address or ticket content.

## Testing

- Unit tests with the in-memory store, the in-memory desk and a fake clock.
- The store conformance suite runs against memory and against PostgreSQL in
  CI.
- The mailbox desk passes the desk conformance suite.
- Property tests:
  - no case is lost under any sequence of desk errors;
  - a visitor's text never reaches an acknowledgment;
  - clearing never touches an undelivered or held case.

## Security

- The module holds no vendor credentials; desks receive theirs from the host,
  through its connection or secret mechanism.
- Every operation is attributed to a principal by the host, and the host
  authorizes it.
- Inputs are bounded in size, and pages are reduced to paths.
