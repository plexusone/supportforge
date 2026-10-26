# ROADMAP — supportforge: Support Requests and Feedback

**Initiative:** `INIT-SUPPORTFORGE-001`
**Repository:** `github.com/plexusone/supportforge` (with `github.com/plexusone/supportforge-core`)

Specs: [PRD](PRD.md), [TRD](TRD.md), [PLAN](PLAN.md). Work items carry
stable IDs (`RMI-SUPPORTFORGE-NNN`), are reviewed and executed by phase, and
commits implementing one carry the trailer `Refs: RMI-SUPPORTFORGE-<NNN>`.
Items marked `supportforge-core` are implemented in that repository.

Later, not scheduled: a requester's list of their own cases, suggested answers before
submitting from a knowledge module, an agent console, and a live channel.

## Phase 1 — Core interface

**Theme:** Any help desk can be connected through one small, stable interface, and an adapter can prove it behaves.

- [x] `RMI-SUPPORTFORGE-001` `supportforge-core`: module, CI, and the `Case`, `Requester`, `Acknowledgment`, `Context` and `Delivery` types
- [x] `RMI-SUPPORTFORGE-002` `supportforge-core`: the `Desk` interface, the `TicketEraser` and `TicketExporter` extensions, and the error classes
  - Depends on: `RMI-SUPPORTFORGE-001`
- [x] `RMI-SUPPORTFORGE-003` `supportforge-core`: the `desktest` conformance suite and an in-memory desk that passes it
  - Depends on: `RMI-SUPPORTFORGE-002`
- [ ] `RMI-SUPPORTFORGE-004` One vendor adapter, written outside this repository, passes the suite before `supportforge-core` v1
  - Depends on: `RMI-SUPPORTFORGE-003`

## Phase 2 — Intake and delivery

**Theme:** A request is accepted, stored before anything is sent, and delivered through an ordered list of desks that ends with mail.

- [ ] `RMI-SUPPORTFORGE-005` Intake: validation, the visitor rules, and the `ChallengeVerifier`, `RateLimiter` and `Observer` ports
  - Depends on: `RMI-SUPPORTFORGE-002`
- [ ] `RMI-SUPPORTFORGE-006` Stores: the case and outbox model, the in-memory store, and the store conformance suite
  - Depends on: `RMI-SUPPORTFORGE-005`
- [ ] `RMI-SUPPORTFORGE-007` Worker: claiming, backoff, the attempt limit, the desk order, and error-class handling
  - Depends on: `RMI-SUPPORTFORGE-006`
- [ ] `RMI-SUPPORTFORGE-008` Acknowledgment templates, with the rule that a visitor's text is never repeated
  - Depends on: `RMI-SUPPORTFORGE-005`
- [ ] `RMI-SUPPORTFORGE-009` The mailbox desk on `omnimail`, passing the desk conformance suite
  - Depends on: `RMI-SUPPORTFORGE-003`, `RMI-SUPPORTFORGE-008`

## Phase 3 — Data governance

**Theme:** Support messages are personal data with rules: classified, kept briefly, held when required, and exportable and erasable at the desk too.

- [ ] `RMI-SUPPORTFORGE-010` Field classification in the `grokify/datacatalog` model, with a test that fails on an unclassified field
  - Depends on: `RMI-SUPPORTFORGE-006`
- [ ] `RMI-SUPPORTFORGE-011` Clear job and the `HoldChecker` port; undelivered and held cases are never cleared
  - Depends on: `RMI-SUPPORTFORGE-010`
- [ ] `RMI-SUPPORTFORGE-012` Export and erasure, including desk transcripts and ticket erasure, and the report of what was retained
  - Depends on: `RMI-SUPPORTFORGE-010`
- [ ] `RMI-SUPPORTFORGE-013` Optional sealing of text and address through a `Cryptographer` port
  - Depends on: `RMI-SUPPORTFORGE-010`
- [ ] `RMI-SUPPORTFORGE-014` Log and error redaction tests: no title, body, address or ticket content anywhere
  - Depends on: `RMI-SUPPORTFORGE-007`
- [ ] `RMI-SUPPORTFORGE-018` Organization view: the `Shared` flag, the `SharingPolicy` port, billing requests shared, the text-free index, and unsharing
  - Depends on: `RMI-SUPPORTFORGE-006`, `RMI-SUPPORTFORGE-010`

## Phase 4 — Release

**Theme:** Both modules are documented, tested against PostgreSQL, and tagged.

- [ ] `RMI-SUPPORTFORGE-015` Ent `sqlstore` with `supportforge_` tables, passing the store conformance suite on PostgreSQL in CI
  - Depends on: `RMI-SUPPORTFORGE-006`, `RMI-SUPPORTFORGE-010`
- [ ] `RMI-SUPPORTFORGE-016` Docs site, README and an example host
  - Depends on: `RMI-SUPPORTFORGE-009`, `RMI-SUPPORTFORGE-012`
- [ ] `RMI-SUPPORTFORGE-017` Changelogs and tagged releases of `supportforge-core` and `supportforge` after CI passes
  - Depends on: `RMI-SUPPORTFORGE-004`, `RMI-SUPPORTFORGE-015`, `RMI-SUPPORTFORGE-016`
