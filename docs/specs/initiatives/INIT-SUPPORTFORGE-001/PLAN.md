# supportforge: Plan

**Initiative:** `INIT-SUPPORTFORGE-001`
**Repository:** `github.com/plexusone/supportforge` (with `github.com/plexusone/supportforge-core`)

Implements the [PRD](PRD.md) and [TRD](TRD.md). The [roadmap](ROADMAP.md)
holds the work items.

## Approach

- **Interface first.** `supportforge-core` and its conformance suite come
  first. They are small, and an adapter written elsewhere can start as soon as
  they exist.
- **Working without a vendor.** The mailbox desk is built before any vendor
  adapter, so an application has support with only a mail sender.
- **Store first, always.** The case and its outbox entry are written in one
  transaction from the first version. No version sends before storing.
- **Personal data rules from the start.** The classification, the clear job
  and export and erasure are part of the first release, not added later.
- **Small surface.** No inbox, no chat and no knowledge base. They are later
  products that use this module.

## Phases

| Phase | Theme | Outcome |
| --- | --- | --- |
| 1 | Core interface | `supportforge-core` is published with its types, desk interface, error classes and conformance suite |
| 2 | Intake and delivery | Requests are validated, stored with an outbox entry, and delivered by a worker through an ordered list of desks, ending with the mailbox desk |
| 3 | Data governance | Fields are classified, text is cleared after delivery, holds are respected, and export and erasure include cases and tickets |
| 4 | Release | Docs, examples, a PostgreSQL store passing the conformance suite in CI, and tagged releases of both modules |

## Dependencies

- `omnimail` for the mailbox desk.
- `grokify/datacatalog` for field classification.
- A host that resolves the requester, verifies a visitor challenge, rate
  limits, and authorizes, audits and runs migrations. The first host is
  SystemForge.

## Risks

- **The interface is wrong for a real vendor.** Mitigation: write one vendor
  adapter against the core before tagging v1 of it, and keep extensions
  optional.
- **Mail is the single path when every vendor fails.** Mitigation: the
  mailbox desk alerts on failure, and the outbox keeps the case until mail
  succeeds.
- **Clearing too early loses context for a slow responder.** Mitigation: the
  ticket at the desk holds the conversation; the delay is configurable; and a
  held case is never cleared.
- **Spam through the visitor form.** Mitigation: the challenge, rate limits,
  and acknowledgments that do not repeat visitors' text.
