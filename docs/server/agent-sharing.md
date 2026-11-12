# Agent session sharing

This contract applies to web, Android and Apple clients. Sharing is a manager-owned,
read-only publication of sanitized, sealed logical messages. It is not a terminal
subscription, a transcript download, or an alternate login credential.

## Deployment and transport

Configure the hub's `sharing.public_origin` explicitly, for example
`http://share.example.test:18081` or `https://share.example.test`. Both HTTP and HTTPS
are supported. The origin must have no userinfo, path, query, fragment, or invalid
port and must differ from the authenticated application's configured origin.
The same host with a different port is an independent origin. Links never use
request Host or forwarded headers. HTTPS is recommended, not mandatory.

The independent viewer is the web build's `public-share/index.html` plus its
`share-assets/` graph, deployed beneath the normal web distribution. The hub
serves only `/share/{token}`, `/share-assets/…`, and the exact public GET API on
the configured sharing host. Every other path is a real 404, including the
application shell, admin, health, pairing, WebSocket, manifests and workers.
A missing independent build fails closed and is not replaced with the app shell;
creation is unavailable until the hub hands a configured origin to the manager.
The public origin must actually be routed to the hub by the deployment's listeners
or reverse proxy; configuring a URL alone does not open a listener.

### Independent HTTP ingress

For an unprivileged separate port without changing the hub's socket-activation
configuration, run the standalone public-only ingress from the installed binary:

```sh
personad share-http --listen :18081 \
  --upstream http://127.0.0.1:18080 \
  --public-origin http://share.example.test:18081 \
  --hub-config <private hub configuration>
```

Choose unused ports from the deployment's own configuration, not these examples.
Set `sharing.public_origin` to the same public origin. The hub loads it at startup;
`admin/reload` does not update sharing. When an existing automatic release process
is in use, wait for its normal release and verify the serving configuration before
starting the ingress. Do not restart, switch or redeploy the hub solely to open the
sharing port without separate authorization. A URL change by itself is not proof
that the running process has loaded it.

This command starts no hub, manager, runtime or database. It accepts only an
explicit HTTP origin with an unprivileged port and a literal-loopback HTTP upstream.
It rejects unexpected Host, nonpublic/encoded/absolute paths, bodies, upgrades and
non-GET methods before contacting the hub. The upstream Host is fixed to the
sharing authority, client credentials and forwarded headers are removed, and
upstream cookies, redirects, login challenges and compressed/oversized bodies fail
closed. Every response has the restrictive sharing headers; request paths and
capabilities are never logged. Only the viewer, isolated `.js`/`.css` assets and
exact anonymous capability API are eligible.

The ingress bounds accepted connections (64), simultaneous relays (16), request
and response headers (16 KiB), upstream requests (10 seconds) and fully buffered
responses (1 MiB). The upstream retains its own capability and token/IP limits.
The private Unix-owned mode0600 configuration must also contain
`sharing.ingress_key`: 32 independently random bytes encoded as 64 hexadecimal
characters, never copied into command arguments, URLs, logs or client state.
Configure the key and origin before the hub's normal configuration lifecycle;
this ingress reads the existing file without starting another hub.

The ingress MAC binds its actual TCP peer IP, timestamp and exact GET authority,
path and query. Only the exact anonymous capability API arriving on literal
loopback verifies that proof, within 30 seconds, and uses its canonical IP for the
existing IPv4 / IPv6 /64 budget. The key grants no account, management or content
access; the publication capability and ownership checks remain mandatory. Client
forwarding/proof headers are removed before the ingress creates its own proof.
No general X-Forwarded-For or proxy-protocol trust is enabled. The original direct
public ingress still budgets its actual peer when no authenticated proof exists.

Before opening its listener, the command makes one bounded anonymous probe and
requires the hub's proof acknowledgement. A legacy, unconfigured or wrongly keyed
upstream cannot open the sharing listener. Losing acknowledgement on an API
response blocks subsequent anonymous relays until the separate ingress service is
started again after verification; it never falls back to one shared loopback
budget. The acknowledgement and proof are never returned to readers. This
restores IP isolation, not a guarantee against distributed load; all resource and
per-token bounds remain in force.

Run it under a separate user service for persistence. Stop that service to close
only the sharing port; it never signals or changes the upstream hub. Removing the
sharing origin also requires the hub's normal configuration lifecycle. Use the
same origin again for existing links: their origin cannot be reconstructed or
retargeted from tokenless metadata. Check anonymous HTTP behavior at the configured
origin; local browser checks do not prove external-network reachability.

HTTP preview responses include `requires_insecure_transport_ack:true` and a
localized `transport_warning`: the bearer link and selected content cross the
network without encryption and may be obtained by a network observer. Creation
and widening require `acknowledgement.insecure_transport:true` on HTTP, in addition
to sensitivity confirmation. HTTPS previews return false and no transport warning.

All sharing responses, errors included, use no-store, nosniff, no-referrer,
noindex/noarchive, a strict self-only script/style/connect CSP, no workers, no
framing, no form submission, and same-origin resource policy. The sharing relay
has a positive response-header allowlist: managers cannot set cookies, redirect,
challenge for login, or supply arbitrary response headers. Public pages and APIs
never inherit app cookies, bearer credentials, manager selectors, or operation
receipts. Deployment proxy/access logs must mask both capability-bearing paths;
application logs mask them too and suppress sharing panic payloads.

## Trusted ownership

Owner identity is the pair of enrolling authority and random subject UUID, not a
device name, GitHub login, native role, session creator label, or first share.
The closed administrative API requires its existing persisted administrator
session, access scope, JSON write checks and `X-Persona-CSRF` protection.

| Administrative request | JSON fields / result |
| --- | --- |
| `GET /api/v1/admin/owner-subjects` | `{subjects:[{owner_subject,label,revision}]}` |
| `POST /api/v1/admin/owner-subjects` | `{label,reason}` → 201 `{owner_subject,revision,label}` |
| `GET /api/v1/admin/devices/{credential_id}/owner` | `{credential_id,owner_subject:null or UUID,binding_revision,revoked}` |
| `PUT /api/v1/admin/devices/{credential_id}/owner` | `{owner_subject,expected_revision,reason}` → binding |
| `DELETE /api/v1/admin/devices/{credential_id}/owner` | `{expected_revision,reason}` → retired binding; credential revoked atomically |
| `GET /api/v1/admin/sessions/{mid}/{sid}/ownership-inspection` | `{verification_id,verification_version,incarnation,owner_subject,owner_revision,source_kind,complete,message_count,preview,expires_at}` |
| `POST /api/v1/admin/sessions/{mid}/{sid}/owner` | `{verification_id,verification_version,owner_subject,expected_owner_revision:0,confirmed:true,reason}` → `{owner_subject,owner_revision,incarnation,audit_id}` |

Only an existing, live, ordinary full-role device credential may be enrolled.
Master, pairing, answer-only, Link, peer and extension credentials cannot enroll.
Initial binding revision is zero. Reassignment is not supported; identical
confirmation is an audited no-op. Withdrawal retains the binding and advances its
revision. Every existing SQL credential-revocation path retires its binding.
Replacement during token exchange never inherits the replaced credential's subject.
Multiple explicitly enrolled credentials can share a subject; revoking one does
not revoke the subject's publications or the other credentials.

Members refuse local enrollment and attribution. App management travels through
the existing member-to-center boundary; only the center produces trusted caller
context. Owner identities are not silently merged or imported after center changes.
Management admission reads live credential/binding rows, not the 30-second auth
cache, and holds the credential's shared Postgres row lock through the bounded
relay. Revocation takes the conflicting write lock. An operation completed before
committed revocation wins; already delivered content cannot be recalled.

## Creation receipts and source capability

The authenticated hub captures the original human submission **before** adding
project or to-do context. Its trusted `Caller` and `Submission` travel in the
existing `devicewire.NodeCall` or `assistantwire.HTTPRequest` envelope; client
headers and arbitrary node parameters cannot install them. Background/delegated
manager calls are system calls and borrow no human owner.

Sharing-authority handover follows the hub's trusted roster: its staged entries
and machines recorded as kept by their manager. Removing a kept machine from
hub.json does not remove its authority. The keeper/owner filter and live routing
generation validation still apply; reported-only machines receive no authority.
This does not change the separate handover of actual machine configuration.

The manager persists a creation intent and sends opaque creation/incarnation IDs
only to nodes advertising `creation_receipt.v1`. Terminal, asynchronous, runtime
launcher and engine-runtime start returns record those IDs in node state. The
manager decoder, database DTO, state SQL and load path retain them. A matching
**persisted** state receipt reconciles ownership independently of start-reply
arrival order; failed persistence leaves ownership unbound. Resume, import,
discovery, fork and copying never claim an existing session.

Managers advertise `agent_sharing.v1`; nodes advertise `share_projection.v1` and
`creation_receipt.v1`. Old peers remain connected but cannot publish unsupported
content. Source projection is `session.share_projection` with
`{session_id,incarnation,previous?}`; the trusted inspection variant can recover
an existing node incarnation without claiming it. Clients cannot supply file paths.

### Creation outcome diagnostics

Local `share_creation` lines contain only a JSON object with `session_id`, `step`
and an allowlisted `result`. UUID and bounded synthetic session IDs are accepted;
other identifiers become `invalid`, and a not-yet-known ID becomes `pending`.
Content, owner/credential identities, creation/incarnation IDs, capabilities,
paths, machine names and raw error strings are never included. These are local
logs, not public DTOs, permissions or new durable state.

| Step | What its result establishes |
| --- | --- |
| `manager_prepare` | The intent was created, or preparation skipped/failed because storage, caller, authority, resume or node capability did not qualify. Emitted after the start reply supplies its session ID. |
| `node_reply` | Returned creation identities match the manager's prepared identities, are missing, or differ. Values themselves are not logged. |
| `node_receipt` | The real node updated memory and invoked its persistence callback, lacked a callback, or skipped the receipt. |
| `node_notify` | The one-way write was sent without durable acknowledgement, lacked a peer, or failed. Existing reconnect/switch-state handling is preserved. |
| `manager_persist` | State arrived with/without both receipt fields, its database write committed, or storage/input failed. |

`sent_unacknowledged` is deliberately not called persisted. Likewise,
`committed_with_receipt` proves the state write succeeded, not that an unknown or
mismatched intent acquired ownership. Existing database reconciliation and catalog
ownership checks remain the authority. Counts may be obtained from these fixed
step/result codes; they never use session IDs as metric labels.

For a bounded synthetic reproduction, reduce only exact-session log lines to
step/result presence and counts. Do not publish its actual identifiers or inspect
unrelated conversation logs. Missing lines from an older node or unavailable log
stream are an observability limitation, not proof that a stage did not execute.

Supported complete sources:

- Native Claude JSONL, including runtime sessions such as Paseo that name a locally
  available Claude transcript. Branch evidence and complete records are retained.
- Codex append-only `event_msg`/`item_completed` rollout records with explicit
  task-start/task-complete boundaries. Duplicate `response_item` model context
  is never published.

Runtime Item-only streams without a complete native source/checkpoint are denied
as `source_unsupported`; this includes runtimes whose current bridge exports only
bounded display items. Codex `history_mutation` formats are denied until their
adapter can prove branch continuity. An unknown user/peer origin is omitted, not
promoted from `role=user`. Native explicit human provenance and sanitized,
successfully delivered app-submission receipts are eligible. A receipt is not
proof of model consumption or an assistant-parent association. An asynchronous
initial-prompt registration alone leaves its submission delivery unknown; it is
omitted unless authoritative native provenance resolves it. No echo/timestamp
heuristic authorizes an ambiguous source row.

Attribution seals a complete bounded source, binds administrator/source/
incarnation/assignment, and reopens and verifies the exact checkpoint before an
append-only attestation commits. Existing ownership is immutable. Replacement,
rewrite, changed authority, stale inspection or incomplete source returns 409.
This is an administrator's explicit ownership attestation, not inferred evidence.

## Selection and DTOs

All client IDs are opaque; RFC3339 timestamps are UTC. Public and editor IDs are
share-local, not native transcript, tool or catalog IDs. Integers are revisions,
not timestamps. Swift clients should model `Selection` as an enum with exactly
these mutually exclusive shapes, optional execution with a tagged enum, and
nullable cursors/reasons explicitly rather than as empty strings.

```text
Selection = {mode:"all"}
          | {mode:"from",from_message_id:string}
          | {mode:"individual",message_ids:string[]}

PublicMessage = {message_id,role:"user"|"assistant",text,execution?:Execution[]}
Execution = {kind:"text",text}
          | {kind:"tool",tool_id,name,arguments,results:{text}[]}
          | {kind:"agent",tool_id,text}

ShareMetadata = {
  share_id,revision,content_version,selection:{mode,count},
  include_future,show_tools,created_at,updated_at,expires_at,
  status:"active"|"revoked"|"expired",
  publication_status:"frozen"|"live"|"paused",pause_reason:string|null
}
```

One selection unit is a genuine human input or a completed assistant turn's final
/latest text. Intermediate narration and proven tool associations are execution,
initially collapsed by clients. Tools-off removes the entire execution field,
including names, arguments, results and intermediate text. Thinking, attachments,
images, active cards and runtime identity metadata are structurally excluded.
Full allowed logical text is normalized and sanitized before display clipping;
credential assignments, bearer tokens, credential-bearing URLs and complete or
unterminated private-key blocks are redacted. Unknown prose can still be sensitive:
preview plus explicit sensitivity acknowledgement remains mandatory.

Native reply timestamps and trusted submission receipt times are used only to
interleave displayed conversation content. They never prove provenance or future
eligibility and are not public DTO fields. If an adapter omits timestamps, its
native order is retained and receipt input follows it; clock skew can also affect
cross-source ordering.

## Management API

Use an enrolled ordinary device bearer header, explicit `X-Persona-Manager`, JSON
writes, and same-origin browser metadata. No cookies, URL tokens, capability-token
login, conflicting query manager, or silent default-manager fallback. Session
routes also require that the named manager currently owns the machine.

| Request | Response |
| --- | --- |
| `GET /api/v1/sessions/{mid}/{sid}/share-catalog?limit=100` | `{can_share,reason,can_include_future,snapshot_id,snapshot_version,expires_at,complete,total_messages,omitted_messages,omissions,messages:[{message_id,role,text,selectable}],next_cursor}` |
| Catalog continuation with `snapshot_id`, `cursor`, `limit` | Same sealed complete catalog; all/from resolve against the complete catalog, not loaded pages |
| `POST /api/v1/sessions/{mid}/{sid}/share-preview` | Exact proposed policy below → `{preview_id,expires_at,policy_version:1,selected_count,omitted_count,messages,next_cursor,requires_insecure_transport_ack,transport_warning}` |
| `GET /api/v1/sessions/{mid}/{sid}/share-preview/{preview_id}?cursor=&limit=100` | Same bound preview |
| `POST /api/v1/agent-shares` | Creation below → 201 `{share:ShareMetadata,public_url}`; raw link returned once |
| `GET /api/v1/agent-shares?machine=&session_id=` | `{shares:ShareMetadata[]}`; tokenless, same subject across devices |
| `GET /api/v1/agent-shares/{share_id}` | `{share,can_widen,reason}` |
| `GET /api/v1/agent-shares/{share_id}/messages?cursor=&limit=100` | `{revision,content_version,messages,next_cursor}` from sealed content, without reaching the node |
| `PATCH /api/v1/agent-shares/{share_id}` | Policy change below → `{share}` |
| `DELETE /api/v1/agent-shares/{share_id}` | `{expected_revision}` → `{share_id,revision,status:"revoked"}` |

A denied catalog contains only `{can_share:false,reason,can_include_future:false,
messages:[]}`, no content or handles. Capability reasons include `ownership_unbound`,
`not_owner`, `owner_enrollment_required`, `source_unsupported`, `source_incomplete`,
`source_limit`, `authority_unavailable` and `manager_unsupported`.

Create preview:

```json
{"operation":"create","snapshot_id":"<opaque>","snapshot_version":"<opaque>","selection":{"mode":"all"},"include_future":false,"show_tools":false,"expires_in_days":3}
```

Creation uses that exact policy:

```json
{"client_request_id":"<UUID>","machine":"<opaque>","session_id":"<opaque>","snapshot_id":"<opaque>","snapshot_version":"<opaque>","selection":{"mode":"all"},"include_future":false,"show_tools":false,"expires_in_days":3,"acknowledgement":{"sensitive_content":true,"policy_version":1,"preview_id":"<opaque>","insecure_transport":true}}
```

Future and tools default off; lifetime defaults to 3 days. Only 1, 3, 7 and 30 days
are accepted. Every later expiry is capped at creation plus 30 days. The capability
is canonical base64url of a 16-byte opaque roster routing handle plus a CSPRNG
32-byte secret (64 characters, 256 bits of secret entropy). Only SHA-256 of the
**complete token** is stored, never the raw token or a recoverable token secret.
An identical committed creation replay returns 409 `creation_already_committed`
with tokenless metadata; a changed request under the same UUID returns 409
`idempotency_conflict`. A lost link is recovered by list, revoke and recreate,
never by token reconstruction or automatic create retries.

Update preview uses `operation:"update"`, `share_id`, `expected_revision`,
`expected_content_version`, proposed optional `include_future`, `show_tools`,
`expires_at`, and optionally all three catalog/selection replacement fields.
Omitted policy fields preserve existing values. Without replacement, omit all
catalog/selection fields; the server uses the sealed selections.

Safe PATCH narrowing needs only `expected_revision`, optional
`remove_message_ids` (editor IDs), `show_tools:false`, `include_future:false`, or
a shorter `expires_at`. It works without the source or current source authority.
Replacement, tools-on, future-on/reactivation, or expiry extension needs
`expected_content_version` and an exact matching preview acknowledgement.
`remove_message_ids` and selection replacement are mutually exclusive.
Policy changes advance revision; public content changes advance content version.
Revoked and expired capabilities cannot be revived.

## Future publication and public read

The manager retains a durable incarnation, adapter version, opaque source/branch
identity, complete-record boundary, observed physical EOF, accepted-prefix hash,
partial-prefix evidence, submission sequence, first-seen identities, exclusions,
and enabled-window frontier. Every ingestion rehashes the accepted prefix even
when size/inode is unchanged. A row begun before enable-time EOF is old even if
its newline or final answer arrives later. Pending receipts/completions keep their
first-seen eligibility. Only finalized complete logical messages are appended.

Creation with future-on captures a fresh frontier separately from the historical
preview: content between snapshot and enable-time frontier is not silently added.
Reactivation similarly skips the disabled interval. Future-off freezes content.
Unverifiable continuity, rewind/rewrite, replacement or limits pause publication
and retain sealed history. A bounded durable scheduler visits up to 100 queued
shares per cycle, rotating by last ingestion time; ordinary cadence is 15 seconds,
not a latency guarantee under unavailable or slow nodes. It never changes Follow
state or opens an anonymous terminal subscription.

```http
GET /api/public/agent-shares/{token}?limit=100&cursor=<opaque>
```

```text
{revision,content_version,expires_at,publication_status,
 poll_after_seconds:15|30,messages:PublicMessage[],next_cursor:string|null}
```

Public reads consume prepared Postgres content only. The token selects one original
manager routing identity, with no fan-out/default fallback. Manager removal retires
its handle permanently; machine moves and remove/recreate advance assignment
identity. Every successful public response carries separate private authority
metadata in the bus envelope; the hub verifies it against the current authoritative
roster and removes it. Moving a machine back does not revive old capabilities.
Unknown/unavailable authority, invalid token, revoked or expired shares return the
same generic 404 before cursor details. Stale revision/content cursors return 409
`share_changed`. Cursor handles expire in five minutes, are reused for equivalent
positions, and have bounded retention.

The viewer uses escaped plain text and a separate emitted dependency graph without
authenticated bootstrap, PWA, telemetry or credential storage. Requests use
`credentials:"omit"`, `cache:"no-store"`, rejected redirects, one poll in flight,
15-second live or 30-second static validation, hidden-tab pause, stale-content
hiding before resume validation, local-expiry clearing, response fencing and
Retry-After/backoff. No token or shared content is persisted in the browser.

Budgets: 64 KiB write bodies; 1,000 explicit IDs; 20 active shares per subject per
manager store; 1,000 retained shares per subject. Quota counts use scalar indexed
state, and lists read only prepared metadata headers (32 KiB maximum per header),
never every retained publication body. The header, active flag and sealed body
commit together; each listed header is checked against current session ownership.
Complete source 16 MiB / 10,000
logical messages; complete component 1 MiB; displayed text 32 KiB after sanitizing;
public page 100 messages / 512 KiB; stored publication 16 MiB / 10,000 messages;
256 transient handles per actor/kind and 64 MiB transient actor data. Anonymous
admission is IP 60/min burst 20, token-hash 120/min burst 30, concurrency 32, with
bounded TTL limiter storage that evicts its least recently seen source rather
than refusing everyone. IPv6 sources count per /64; malformed capabilities spend
only the source budget. At most two owner-admitted management relays run at once
(others wait up to 3 seconds, then get 429 `busy`), preserving hub pool capacity.
Ownership checks within a share transaction reuse its connection, so readers
waiting for the writer fence never need an extra pool connection. Node projections
run outside that fence; commit compares revision, content version and frontier
hash. Committed creation retries and unchanged-future narrowing do not need a live
node. Limits fail closed; they are not hub-global owner
quotas across independent manager databases.

## Security verification checklist

Use synthetic sessions only. `spec/fixtures/agent-sharing.json` is the shared DTO
fixture; real database tests run through `dbtest.Main` against an isolated test
fixture, never the production database.

- OWASP API1/BOLA: two subjects cannot inspect, mutate or enumerate each other's
  shares; two explicitly enrolled devices of one subject can manage the same share.
- API2/authentication: live DB admission, every credential revocation path,
  cross-process admission lock, forged caller/submission, unbound legacy sessions.
- API3/property exposure: no native IDs, cookies, execution with tools off,
  hidden peer/child messages, thinking, attachments, credentials or private-key blocks.
- API4/resource use: source/component/body/page/storage/handle/owner quotas,
  limiter bursts, concurrency/deadlines and scheduler rotation.
- API5/function authorization: closed admin/CSRF boundary, ordinary enrolled device
  management only, exact anonymous exemption in direct and center dispatch.
- API6/sensitive flows: preview bound to policy/actor/incarnation/assignment/revisions,
  one-time capability, hash-only idempotency, no expiry/revoke revival.
- API8/configuration: independent HTTP or HTTPS origin, HTTP warning/ack, missing
  public build, strict CSP/security headers on success/error/panic, no app fallback.
- API9/inventory: explicit manager/node capabilities and honest source denials,
  consistent web/Android/Apple DTOs and opaque revision cursors.
- API10/unsafe upstream consumption: coherent complete source capture, rehash on
  same-length rewrite, partial EOF/first-seen frontiers, response-body bounds and
  positive relay-header policy. No arbitrary manager error text reaches public JSON.

Synthetic local tests are not evidence of a deployed commit, an installed client,
an anonymous production browser, or a delivered runtime prompt. Those checks
remain separate acceptance steps after local verification and merge.
