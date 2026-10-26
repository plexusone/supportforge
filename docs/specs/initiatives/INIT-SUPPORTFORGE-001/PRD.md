# supportforge: Product Requirements

**Initiative:** `INIT-SUPPORTFORGE-001`
**Repository:** `github.com/plexusone/supportforge` (with `github.com/plexusone/supportforge-core`)

## Problem

Every software-as-a-service application needs a way for its users to ask for
help and to say what they think, from the first day it is live. Small teams
building many applications meet the same gaps each time:

- **Nobody hears the users.** A new application ships without a contact path,
  so problems and opinions never reach the team, and early users, the ones
  most worth hearing, leave without saying why.
- **A chat widget promises what a small team cannot keep.** Nobody is online
  to answer in real time, and a silent chat is worse than an honest form.
- **Help-desk products are each a special case.** One has a free tier without
  an API, another an API without a free tier, another a different way to
  create a ticket. The application code ends up written against one vendor.
- **Messages get lost.** A vendor is down, an API allowance is spent, or a
  token expires, and a customer's message disappears with the failed call.
- **Support messages are personal data with no rules attached.** They hold
  names, addresses and free text, are copied into several systems, and are
  forgotten when someone asks for their data to be exported or erased.
- **Anyone can abuse a public form.** Without care, a contact form becomes a
  way to send spam from the application's own domain.

## Goals

1. **Every application can take a support request or feedback from day one,**
   from a signed-in user or a visitor, with no vendor account required.
2. **Asynchronous by design.** The requester is told a reply comes by email,
   later, and nothing suggests anyone is online.
3. **No request is lost.** A request is stored before any vendor is called,
   and delivered with retries.
4. **Vendor-neutral.** A help desk is an adapter behind one small interface,
   published in its own dependency-free module, so an adapter can live
   anywhere, including in a private repository.
5. **Personal data has rules.** Every stored field is classified, message
   text and reply addresses are kept only as long as delivery needs them,
   and a requester's records can be exported and erased, including at the
   help desk.
6. **Safe for anonymous use.** Rate limits, a bot challenge, and an
   acknowledgment that never repeats an anonymous visitor's text.
7. **Feedback is a first-class kind,** not a support category nobody reads.

## Non-goals

- **A ticket inbox and agent console.** Agents answer in the help desk the
  application chose, or in their mail client. A console is a later product
  built on this module.
- **Live chat and AI answers.** A live channel is added only when something
  can answer it. Knowledge and suggested answers belong to a separate
  knowledge module that this one can call.
- **A help center or knowledge base.** Same reason.
- **SLAs, routing rules and escalation.** These belong to the help desk.
- **Session recording or analytics.** An application may emit a
  "case created" event through a hook; the event carries no text.

## Users

| User | Needs |
| --- | --- |
| Requester (signed in) | Ask for help or give feedback without leaving the application, and get an honest promise of an email reply |
| Visitor | Contact the team from a public page without an account |
| Support responder | Every request in one inbox, with who sent it, which product and page, and what kind |
| Application developer | One call to accept a request, one place to choose the help desk, nothing vendor-specific in product code |
| Organization administrator | Know which support requests their organization has open, and read the ones that are about the organization, such as billing, or that a member chose to share |
| Privacy or legal manager | Requests included in data export and erasure, kept no longer than needed, and preserved under a legal hold |

## Requirements

### Intake

- A request has a kind (`question`, `problem`, `billing`, `feedback` or
  `other`), a title, a body, the product, and the page it came from (a path
  only). Feedback may say that no reply is wanted.
- For a signed-in requester, the principal, organization and reply address
  come from the session, never from the request body.
- A visitor supplies a reply address and passes a bot challenge.
- Size limits and per-requester rate limits apply.
- The response is a case reference and the promise of an email reply, with
  no time given.
- A signed-in requester may share a request with their organization's
  administrators. Sharing is off by default, except where the organization's
  policy shares every request or the kind is `billing`, and the form says
  which applies before anyone types.

### Organization view

- An organization's administrators can list its requests as an index: who
  filed, kind, product, status and dates, with no text.
- They can read the text of shared requests and of billing requests, until it
  is cleared.
- Members and collaborators see no one else's requests.

### Delivery

- The request is stored, then delivered by a worker with backoff and an
  attempt limit.
- After the attempt limit, the next desk in a configured order is tried, and
  the last desk is a mailbox, so delivery cannot fail for lack of a vendor.
- The first message to the requester is an acknowledgment from a template
  the application supplies. It repeats a signed-in requester's text and never
  repeats a visitor's.
- The context (product, kind, page, principal and organization) reaches the
  responder privately.

### Data

- Message body and reply address are cleared a configurable time after
  delivery (seven days by default). What remains is an index: references,
  title, kind, product, status and times.
- Export returns a requester's cases, and, where the desk supports it, the
  conversation from the desk.
- Erasure clears the requester's cases and asks the desk to delete its
  tickets, reporting what could not be erased and why.
- A legal hold stops clearing and erasure.

### Operation

- Works with no configuration beyond an outgoing mail sender.
- Alerts on failed deliveries, credential errors and an exhausted vendor
  allowance.
- No secret, message text or reply address in logs, errors or audit events.

## Success criteria

- An application built on the platform shows a working "Contact support" and
  "Send feedback" form with no help-desk account configured, and a test
  message reaches the operator's mailbox.
- A desk adapter written outside this repository passes the published
  conformance suite and is selected by configuration alone.
- With the desk returning errors for an hour, no request is lost, and each is
  delivered when the desk returns or through the mailbox.
- A request's body and reply address are gone from storage seven days after
  delivery, and an export and an erasure for a test requester include its
  cases.
- A visitor's acknowledgment never contains the text they submitted.

### Requester's list

- A signed-in requester can list their own requests: reference, kind,
  product, title, date and status, with a link to the desk where the desk has
  a page they can sign in to.
- The title is kept while the request is listed, so the requester can
  recognize it. The body and reply address are cleared as above.
- Visitors have no list; they follow their request by email.

### Drafted replies

- A responder, or an agent acting for one, can draft a reply to a request.
- A draft is sent only after a person other than its author approves that
  exact text, and it goes through the desk so it joins the conversation.
- Who drafted, who approved and when it was sent are recorded; the draft's
  text is cleared once sent or discarded.

## Open questions

- How long should the index be kept? This is a product policy, so the default
  needs a number the platform's retention rules can use.
