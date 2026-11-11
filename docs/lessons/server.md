# Lessons: hub / node and networking

## Virtual-file sizes and EOF (#4425)

Some Linux sysfs attributes report a stable page-sized length even when their readable contents are shorter. A short
read must end pagination only after a real EOF, filesystem identification on the opened descriptor, and checks that
the opened object and guarded path still have the same identity and metadata. An ordinary file truncated during a read
and a non-EOF I/O failure must remain errors; a zero-byte device must not be read as an open-ended virtual file. The
size and modification-time revision cannot detect virtual values that change without metadata changing, or a writer
that restores metadata, so these checks do not make pages from separate requests an atomic snapshot.

## A shared package that imports a manager's drags it into the hub (2026-11-11, #4371, #4446)

`persona-hub` links every package a shared one (`depguard.Shared`) imports, so one import of a manager-side package in
a shared package, even for a small pure function, puts that package in the hub's program and
`TestPersonaHubDoesNotLinkTheManager` fails. `transcript.AskOf` called `closeout.WaitsOnNobody` that way. A rule that
only the node and the manager apply goes in their package, which imports the shared one (`closeout.AskOf` over
`transcript.AskOf`), not the other way round. Listing the manager's package in `depguard.Shared` (as #4395 did for a
while) only hides it. A new package the hub links and the manager does not, such as what the hub and a helper of its
own say to each other (`relayproto` with the push relay, `shareproof` with the sharing ingress, #4446), goes in
`depguard.HubOwn`, which also keeps it out of the manager; `Shared` is for what both programs link. After adding an
import to a shared package or a new package the hub links, run `go test ./cmd/persona-hub` (a few seconds).

## Receipt integration fixtures need independent identities (#4433)

A registered manager bus connection is not the same as its normal device-state
readiness report. Run that report through the fixture's actual RPC path before
starting a session; do not relax the production readiness check. A state.load RPC
on the same stream fences the preceding durable state.put without a fixed sleep.

Creation IDs are immutable once stored. Reusing one machine/session across wrong
and matching receipt cases can make the matching load retain the earlier identity,
creating a false production-defect report. Give every case its own credential,
machine and persistent record; keep exact creation/incarnation equality assertions.
A local authenticated HTTP/manager/node-state round trip proves those boundaries,
not a real runtime reply or production ownership: retain the separate live evidence.

## A list read before a locked write must not undo it (2026-11-11, #4372)

The manager's agent archive restores a session when its node's list shows new activity (busy, or a newer update time). Readers decorated the node's list first and only then took the archive's lock, so a reader that read the list while an agent still waited on the user, and got the lock after a cancelled task's agent was closed and archived, took that old busy row for new activity and restored the archive for good. The log still said "closed and archived". It showed up as `TestCancelStopsAndArchivesAgents` failing about 1 run in 100, and only with `-cpu` above 1, once the fake answered `session.close` with the session already offline (#3184): the archive then followed the close at once while the test kept reading the list. Weigh a snapshot only against the state that was already there when it was read: `archivedSessions` takes the archive's write count from before its list was read (`archiveMark`), and a record written after that ignores what the list shows (`agentArchive.active`). A counter, not a time: two reads in one clock tick, or a clock stepped back, must not decide it. The web's card snapshots had the same shape (`refresh()` restoring just-resolved cards, `docs/lessons/web.md`).

## Sharing peer budgets need an authenticated local ingress (#4391)

Keeping X-Forwarded-For untrusted is necessary, but a loopback reverse proxy then
collapses every anonymous reader into one upstream IP budget. One reader can crowd
out unrelated readers even with different capability tokens. Bind the actual TCP
peer and exact anonymous GET to a short-lived sharing-only MAC; accept it only on
literal loopback and keep the original capability/ownership checks. Verify the
upstream's acknowledgement before opening, strip the proof/acknowledgement from
public responses, and fail closed when authentication is lost. Never use this key
or header as general proxy trust, a login credential or a management authorization.

## Public sharing ingress needs a fixed authority (#4358)

An independent browser port does not constrain an HTTP client's Host header. A
plain TCP forward or unrestricted reverse proxy can turn the sharing listener
into an application entry point when a client sends the application's Host.
Reject nonpublic paths before proxying and pin the upstream sharing authority;
strip credentials in both directions and protect local gateway errors too.
Changing `sharing.public_origin` on disk does not hot-reload the running hub.
Use its normal release/configuration lifecycle, not an unrelated reload endpoint.
A loopback proxy also shares the upstream's aggregate IP budget unless a separately
reviewed trusted-address protocol is in place; do not silently trust X-Forwarded-For.

## Shared web fixtures belong in the Docker build context (2026-11-11, #4260)

The web production build typechecks test files. A JSON fixture imported from `spec/fixtures/` needs both a
`.dockerignore` exception and a `COPY` into the web stage at the same relative path. A native build sees the
whole checkout and can pass while the Docker build fails with TS2307. The Docker entrypoint suite now checks
shared JSON imports, including globs, against both files, catching new fixture groups before an image build.

## Sharing frontiers and migration lanes (#3604)

- Migration numbers are global across the hub and manager sets, even though their
  files live in separate directories. Inventory both sets in every worktree;
  distinct filenames with the same numeric prefix still collide. Renumber only
  unpublished migrations, never an applied migration or the guard.
- Future publication needs an immutable enable-window frontier **and** a moving
  verified scan checkpoint. Using the previous scan's EOF as the window boundary
  drops a new pending turn when it completes on a later scan. Preserve first-seen
  eligibility and partial-record EOF evidence while advancing the checkpoint.
- A raw asynchronous start registration is not successful prompt delivery. Keep
  the sanitized submission unknown until authoritative evidence resolves it; never
  publish the original app input as delivered solely because a session ID returned.
- A database-backed fence does not make nested pool queries safe. Waiting
  transactions can occupy every connection while the lock holder asks for one more;
  read ownership through the transaction's connection. Keep node RPCs outside the
  fence and reject stale results with revision, content-version and frontier CAS.
  Check creation receipts before fresh RPCs, and do not recapture an unchanged live
  future setting when applying offline narrowing.
- A per-publication body limit does not bound an owner list. Never collect all
  retained sealed bodies to count quotas or render metadata: 1,000 individually
  valid large rows can exceed process memory. Persist a bounded prepared header
  and active scalar with the body, count scalars and list headers, and keep the
  current ownership check on that path.
- Numeric credentials are still credentials. Exempt only explicit count/flag keys
  from redaction; `password=123456` is not the same as `max_tokens=4096`. Preserve
  string quoting when sanitizing serialized arguments.

## Bubblewrap masks are mount views, not path-denial rules (#2977)

A default-allow agent profile cannot reuse the extension sandbox's whitelist: bind the host root read-write, add
`--dev-bind /dev /dev` (a plain root bind left `/dev/null` unusable for git, ssh and curl), and mask only the data
directory with a view that binds back allowed entries. A bind follows the source's final symlink: an innocent-looking
entry can import a secret target. Recreate ordinary links with `--symlink` so they resolve through the masked view, and
close links for special read-only files or partially closed directories. Keep the view root read-only so later secret
backups cannot be created. File binds still pin their inode and the top-level entry list: a replaced `node.sock` stays
old, so a node restart requires an agent restart. Do not permanently cache probes or preflight success; bwrap/PATH and
namespace availability can change, and full per-cwd profiles make an unbounded key space. A tmpfs mounted directly on a symlink follows it and masks the target; give an alias its own view, but do not
claim this protects the link from unlink. Stock bwrap 0.8 has no path-aware unlink/rename filter, so protecting the link
while keeping its parent writable needs another mechanism. It also keeps a supervisor even without `--unshare-pid` or
with `--as-pid-1`: verify the agent's `PPID` in the host pid namespace, not equal pids, and use `--die-with-parent` for
lifecycle cleanup.

## Runtime policy and publisher-owned node packages (#2566)

A persisted node-update choice must override startup configuration everywhere:
API readback, manager handshakes, downloads and busy checks. Preserve an explicit
false with a nullable stored value; publish a changed choice only after the
database accepts it. Package activation is not consumer notification. A repeated
explicit enable can request the existing package-check RPC after configuration,
without pretending that a deployment has finished.

Treat a node binary directory symlink as publisher-owned. Swapping files or
removing version markers through it corrupts the immutable release manifest.
Protect both enabled installs and disabled-policy cleanup, and let the publisher
activate its verified directory after Hub health confirmation.

## Durable storage receipts do not extend authority (#1187)

Check current declared keys and queue/blob flags before replay lookup, including
queue operations inside an atomic transaction. A prior success receipt cannot
bypass a withdrawn flag. A claim receipt describes its original lease: replay
must reject it after expiry, retry, completion or worker replacement, rather than
returning an old `leased` result as fresh work. Time-relative admission checks for
a new retry belong after replay lookup so a committed retry can still recover a
lost reply after its next-attempt deadline passes.

Carry the process's captured installation epoch and grants into the transaction.
Re-resolving a replacement after an initial memory fence can lend an old callback
the new scope; the persisted row lock must compare the original capture. Cancel
attempt-bound callbacks before child cleanup, not after waiting for a dead child.
Postgres owns mutation/receipt atomicity and uninstall retention; managers own
business validation, so the DB package must not import a manager implementation.
A recreated pool is not a database-server restart, and a canceled precommit update
is not an injected unknown commit. Name and report those tests accurately.

Do not pre-truncate every database fixture's timestamps: that masks an actual
install's nanosecond registry record failing authorization against Postgres's
microsecond `timestamptz`. Compare at the persisted precision in start, restore
replay and uninstall, and test that a one-microsecond epoch/revision difference
still rejects the stale operation. Real installed-source acceptance catches this
edge even when synthetic transport fixtures all pass.

A successful owner restore must restart the extension under the manager's context,
not the HTTP request's. The committed data can be correct while a lazy runtime is
canceled as soon as the 200 response returns, making its next page time out. Drive
restore through the real API and open a fresh page after the response; DB-only
restore tests do not exercise this lifetime boundary.

## The first gh on PATH may not be the GitHub CLI (#2081)

Some Linux hosts have an unrelated program named `gh` in `/usr/local/bin`, and `New` puts that directory ahead of
most of the node's PATH. `exec.LookPath("gh")` then found it, `gh auth token` printed something that was not a
token, and the GitHub page said the machine was not signed in although the GitHub CLI there was. A service's PATH
also misses Homebrew on Linux (`/home/linuxbrew/.linuxbrew/bin`), where that CLI usually is. The node now asks each
`gh` on PATH, then in Homebrew's Linux prefixes, for `--version` and runs the first that answers as the GitHub CLI
(`server/internal/node/ghpath.go`). Check what a binary is before trusting its name on PATH.

## A credential bound to an id outlives the entry (#1869)

Deleting a Link computer in admin removed its hub.json entry but left its credential and unused pairing codes valid: the link only failed while no entry had that id, so adding the id again, for any computer, let the old one back in. Now deleting a computer in admin, rejecting it, and a pending one expiring revoke what is bound to its id (`device_tokens.machine_id`, `pair_tokens.machine_id`), and so does adding a computer under an id not in use, from the admin console, an app or `personad hub pair --add`. An entry dropped another way (a manager's `drop_machines`, a hand edit of hub.json) is not revoked then, only when its id is added again through the hub; one written back into hub.json by hand is not covered at all. The revocation filters on the role it means, because a peer hub's credential binds its hub id in the same column.

## Out of hub.json is not removed (2026-11-09)

Once a manager keeps its own list (`machines_kept.go`), its computers move out of hub.json, which can be left as `"machines": []` while every one of them still runs. The admin machines page still read "not in hub.json" as "removed, gone at the next restart" and showed each such computer grayed out as pending removal: `pendingRestart` had learned `keptBy` in the change that moved them, `adminMachines` had not. Anything that decides from hub.json's machines alone that a computer is gone or new has to go by hub.json plus the kept ones (`knownEntry` / `knownEntries`, `keptBy`). When state moves out of a file, check every reader of that file (`diskConfig`, `cfg.Machines`), not only the ones the change touched.

## Finish a revocation's activity push before forgetting its target (#1864)

Push registration IDs differ from credential IDs: select the registrations bound
to the revoked credential, not just a row whose device ID happens to match. A call
to `sendLiveActivity` only queues a goroutine; direct and relay transports recheck
the registration before dispatch. Calling `end` before `signedOut` is therefore
insufficient unless the revocation waits for the end attempts to finish. Send empty
content with immediate dismissal, even for a foreground app or disabled activity
preference, without resetting the fleet state or activities of other credentials.
Database revocation already removes the device and its activity rows, so capture
the live tokens from memory and do not try to persist them back after retirement.
Transport acceptance is not proof that the phone dismissed the activity; the
Apple-side rejected-credential fallback remains separate work.

Database fixtures must also survive `go test -count`: `setupTestDB` does not clear
`admin_sessions`. Give test sessions unique IDs and delete their own rows with
`t.Cleanup` before the store closes; a fixed ID, even `t.Name()`, repeats within one process.

## Retained extension lists need installed managers (#1918)

A relay can retain an offline manager's GET answer while the merged list drops it before the relay is called. For merged retained GET routes, enumerate installed bus identities as well as live managers; keep ordinary lists such as inbox and todos online-only. Never discover owners by scanning cache keys: a removed manager can still have cached answers. Fail closed when the installed roster cannot be read, and test the real relay warm-up followed by disconnect, missing cache, revocation, and an offline default. In external mode even the identity named `assistant` is removable: only seed it when `builtinManager` says it is built in, and return unavailable for an empty roster rather than reviving a cached default.

## Update checks must preserve installation state (#1912)

The hub's `running` flag belongs to the installing process; it is false in the new
process while that process confirms the update. Guard check-driven state changes
with both install and confirmation status, under the same mutex as the write.
Check again after the manifest request: an install can start while it is in flight.
Successful checks may refresh release metadata and failed checks may advance the
last-check time, but neither may replace an active update's state, progress or error.
Apply the guard in the shared check path used by manual, startup and hourly checks,
and keep automatic installation and held-release recording out of confirmation.
Regression tests must observe the state while the request is blocked as well as
after success or failure; checking only the final state misses a transient
`checking` event that already hid the progress in every client.

In timer tests, wait on a request channel with a timeout and assert the request's
timestamp, then use `synctest.Wait` before inspecting the completed result. This
synchronizes the observation without fixed sleeps or a shared request counter.
Even inside `synctest`, new `time.Sleep` calls fail the repository's sleep ratchet;
run `scripts/ci/guards.sh` as well as the server build and race checks.

## Model connection tests must match the runtime (#1732)

A successful connection probe can still hide a request the model server will reject: exercise the same output-token field and its narrowly scoped fallback as the runtime. Parse SSE events rather than individual `data:` lines, including multiline events and CRLF, and test empty, truncated, refused, and errored replies separately.

A working inference base need not expose model discovery beneath it: DeepSeek documents Messages under `/anthropic/v1/messages` but discovery at the same origin's `/models`. Keep endpoint selection separate without rewriting the saved inference base, stripping unknown gateway prefixes or forwarding credentials to another authority. A discovery 404 needs its own explanation and a manual-model fallback, not advice to change a working Messages address.

## Battery investigations need attributed transport counts (#1633)

A slow/error request log is not an access log, and successful Live Activity updates
were silent while start logs recorded only intent. Do not turn missing records into
zero requests, acceptances into device deliveries, or a normal close handshake into
a proven initiator. The systemd edge can retain an older binary while a new slot
serves requests: establish the serving revision from health and the deployment
receipt, not the edge's MainPID alone. APNs registration IDs differ from credential
IDs; join the persisted credential binding, and keep same-name registrations
ambiguous instead of pruning or merging them. Minute-level, route-template counters
with opaque IDs avoid logging every request or exposing payloads; label application
bytes separately from compressed wire bytes and logical pushes separately from retries.
A missing client header does not identify a platform: infer only bounded UA/header
markers plus enrollment evidence, keep the inference basis, and separate diagnostic
clients sharing a phone credential. Native-default App labels are not proof that a
widget extension did not use the same UA. Never log raw UAs to solve attribution.

Foreground suppression must cover the push-to-start branch as well as updates,
without consuming the start episode before the phone goes to the background.
Preference refreshes should compare the affected device's projected activity state:
a same-value retry or a notification-only setting must not force all devices to
refresh. A metrics-only foreground broadcast must use the newest fleet snapshot,
not the last APNs-flushed snapshot while ordinary updates are coalescing.

## Push retirement needs installation proof and a late-write fence (#1663)

Registration IDs and credential IDs are different. Sharing a name or owner is not
proof that two APNs targets belong to the same installation: also require topic,
environment and a matching nonempty encryption public key or exact APNs token.
Use a true enrollment timestamp, not the timestamp rewritten by prefs or grant
maintenance. Protect active owners and coalesce APNs acceptance timestamps; lack
of pre-migration delivery history needs observation grace, not immediate expiry.

Publish a registration only after its ownership check and retirement commit.
A BEFORE INSERT check must allow a prefs upsert that retains the old owner in its
ON CONFLICT UPDATE; check the actual updated owner before rejecting it. Targeted
lifecycle tests do not replace the full device/prefs contract suite.
A process mutex cannot fence another blue-green slot: use a Postgres retirement
ledger and serialized old-writer triggers. A late APNs gone response must compare
against the persisted registration, not just one process's memory. Fence all
retirement reasons; fresh authenticated re-pairing needs a live new owner plus
scoped installation proof, never an old save. Historical metadata cannot prove
which cached ID the current app uses: allow a fresh post-retirement, same-owner,
proven enrollment to replace the canonical target atomically, carrying preferences
and the longest start deadline. Delayed enrollment timestamps remain fenced.
If acceptance fails to flush, skip
inactivity; skip busy transports so completion cannot race the expiry snapshot.
Remove activity state and transfer the
longest persisted start deadline before deleting a duplicate. Lock the device
parent before its activity/budget, matching budget claim and revocation order.
Test two database pools, late legacy writes, failure rollback and physical-install
boundaries; never treat acceptance as a delivered notification or battery result.

## Push-to-start budgets belong to delivery, not episodes (#1662)

An episode counter allows an empty/active loop to start an activity every time and
forgets all spending on hub restart. Client `started_at` is activity age, not a
server push budget; registration and token rotation must not clear spending. Keep
an independent device-FK budget and atomically claim it in Postgres before dispatch,
including across overlapping blue-green slots. A failed or ambiguous claim fails
closed; a successful claim stays spent even if APNs fails or the process exits.
The ten-minute rolling cooldown applies only to starts, never ordinary alerts or
urgent activity updates. Defer a wake time, not a payload: at expiry read the latest
fleet, preferences, foreground state and tokens, and recheck after a slow claim.
Empty/done, an open phone or a fresh update token suppresses a queued start. Restart
reconstructs its deadline from the durable budget and the current manager snapshot.

## A custom HTTPS dial must opt into HTTP/2 (#1620)

A custom `http.Transport.DialContext` conservatively disables Go's automatic HTTP/2 negotiation. Keep the checked-address dial callback and explicitly enable `ForceAttemptHTTP2`; do not replace it with a default dialer or let an upstream proxy resolve the name again. Verify real local TLS HTTP/2 negotiation, HTTP/1.1 fallback and request cancellation. A successful transport test is not evidence that a particular upstream challenge disappeared: verify the installed source separately, without raising budgets or substituting host-downloaded content.

## A failed swap must not look like a new install

`update.SwapProgram` recorded `Prev: ""` when copying the old program to `.prev` or putting the new build in place failed, and `Prev: ""` is how a swap says the program was new, so going back deleted a program the failed swap had never touched.
A failing step reports what it actually changed: `copyFile` writes `.new` and renames, so a failed swap changed nothing (the zero `Swapped`, which `RestoreAll` skips) or only wrote `.prev` (putting that back is harmless).
Don't add a `pending.json` field for it: older edges and watchdogs read the same file and know only `Prev == ""`.

## Test a staged node before stopping the resident one (#5097)

A completed gzip stream and `chmod 755` prove that bytes arrived, not that the target machine can execute them. The SSH deploy therefore runs the staged binary's `version` command before the atomic rename and before `pkill`: an `exec format error` or another failed self-test removes `.new`, preserves the installed binary, and leaves the working node running. Package selection still validates GOOS / GOARCH through Go build info; the remote self-test is the final target-side guard, not a duplicate package check. Exercise this boundary through the real login-shell deploy script with an isolated HOME and a fake `pkill`, never by replacing or stopping a development machine's resident node.

## Migrating a long-poll channel without duplicate consumers (#2020)

A connector switch alone does not free a built-in extension ID for installation: the registry must explicitly
opt into `Registry.Yields` for the approved handoff. Keep sealed connector state under the unchanged connector
ID and finish stopping the old process before starting the replacement; two long-pollers with the same token
can consume each other's messages. Publish the extension and signed catalog, verify an isolated hub with
synthetic state, and approve the production handoff before removing the built-in. Never copy production
tokens into a test hub. Retired artifact-name parsers may remain for old releases, but a new manifest must
not republish a retired program merely because its old artifact was left in the build directory.

## Extension public identity is not storage or token authority (#1493)

- An ID rename is not uninstall/reinstall: deleting the old row cascades secrets and node data. Keep the original storage primary key and directories, and commit public names, aliases, version and previous provenance atomically with compare-and-swap guards.
- Source declarations reserve no names by themselves. Only an actual same-market, explicitly confirmed transition creates historical aliases. Validate the complete catalog graph, not a filtered list of declarations; a predecessor still present as a current entry is a collision even without a cycle.
- Human compatibility lookup and program authentication differ. Resolve old owner-facing requests to the committed installation, but authenticate callbacks/tokens against exact public identity and current runtime generation. Internal model purposes and knowledge owners need the stable key too; do not fix only data and secrets.
- Rollback keeps historical aliases; a successor alias is not a currently installed successor. Catalog presentation uses exact public IDs, while final conflict guards allow an alias only when it belongs to the same stable slot. Test the full rename → rollback → confirmed re-upgrade path, not just first migration.
- Probe capabilities before sending identity-changing node messages. An old node can silently ignore extra fields and delete the absent old ID's data. New nodes retain slot/revision tombstones for stale messages, while authenticated fresh reinstall must establish its new binding rather than be permanently rejected.
- Node identity revisions and installation generations must use Postgres timestamp precision before the first bind. Raw Go nanoseconds are truncated to microseconds on storage; after a reload, a node would reject that lower revision or mistake the installation time for a new generation. Normalize both fields and test before/after database precision, not only whole-second fixtures.
- Persistent hook ownership must remain recognizable to the rollback binary. Stop and join the old process's heartbeat and cleanup before starting the replacement; a late cleanup can remove the new hook even when the database migration is correct.
- A guard over the machine IDs a user picked must tell three cases apart (#2510): an ID the fleet no longer has, a computer that is offline, and a node that is too old. One shared "update this computer's node" refusal blocked a real rename on an ID left from before the hub moved, which no update could fix. Skip IDs that are not computers, and name the computers in each refusal.
- The catalog lists a renamed extension only under its new ID, while the installed extension and its `upgrade.id` keep the old one (#2511). Every entry that goes from an installed extension to its market page (upgrade rows, Check for updates, CLI hints, deep links) must use `rename_to` / `rename_from`, or fall back to `replaces`. The hub accepting the old ID in the preview does not help a client that looks the item up in the catalog itself.


## Private frames cannot carry a manager's resume cursor (#2354)

Targeted extension UI frames bypass the event log, so the broadcast sanitizer never sees them. Strip the top-level `event_seq` at that relay boundary too; nested page data remains the source's. Test actual delivery and recipient restrictions together, including authenticated manager attribution, and keep private frames out of the global log.

## Mail account writes publish only committed candidates (#1302)

Whole-account snapshots need one guard covering candidate capture, storage and publication across adds, OAuth
upserts, imports, deletes and refresh-token rotation. Publishing first and rolling back later lets an unrelated
successful write persist rejected credentials or a failed deletion. Copy nested OAuth credentials, keep network
verification outside that guard, and retire health and cached connections only after the write succeeds. Concurrency
regressions should park a second writer at the guard and check restart agreement, rather than require it to enter storage.

## Distant spreadsheet dates exceed time.Duration (#1061)

`time.Time.Sub` saturates at roughly 290 years, so XLSX dates after the duration limit all became the same serial. Subtract Unix seconds for parsed calendar dates, retaining the existing wall-clock normalization. Verify the emitted worksheet XML, including distant leap years, time fractions, offsets and text cells; a valid ZIP/XML structure alone does not prove date values are correct.

The 1900 date system also has a calendar boundary (#1310): January 1 is serial 1, February 28 is 59, and March 1 is 61 because Excel reserves 60 for a fictitious leap day. Apply that adjustment only to January/February 1900, and leave earlier dates as text. Check the range after wall-clock normalization, so an offset cannot move a written date across the boundary.

## Installed provenance is not a mutable source identity (#1227)

- A marketplace ID survives address moves. Its newly verified official catalog cannot authorize previously installed unsigned code, including code absent from that catalog. Capture an unforgeable entry-verification snapshot before downloading, bind it to the unchanged manifest and package fields, and save the verified key with the installed row in the same transaction. Clear it on unsigned upgrades; never backfill it from the current market. Owner sandbox choices remain independent.
- A successful catalog mirror has not synchronized a git checkout. Fetch repository-relative packages lazily from the original git source/ref, keeping credential restrictions and time budgets; do not clone for OCI or HTTPS package downloads. Package hashes still check the downloaded bytes.
- Recursive JSON duplicate checks must distinguish case-sensitive maps from protocol structs. Compare arbitrary map keys exactly; reject known struct-field aliases using Unicode simple folding (`strings.EqualFold`, as in `encoding/json`). Lowercasing every object both rejects valid configuration keys and misses long-s signature aliases.
- Address migration must repeat target uniqueness checks after network reads under the final state lock, including the one official identity across branch and transport aliases. This was a pre-existing migration defect, not an embedded-signature regression.

## User completion is not a recommendation (#3038)

A `needs_user` task's direct completion is the existing to-do `PATCH {"done":true}`,
not its ask's Accept route: Accept can send the recommendation and resume a closed
Agent. Keep user completion's empty `done_by`; assigning `review` would allow later
Agent activity to reopen it. A task snapshot read before a slow session lookup cannot
authorize automatic rework or recommendation delivery after completion or reopening.
Recheck the task immediately before dispatch, including inside a queued offline-send
closure. Test the actual lookup boundary with a blocking fake RPC, not a fixed sleep;
direct user messages still deliberately retain their separate reuse semantics.

## Completion reads must belong to the current turn (#6525)

A model judgment can return after a resumed turn has already stopped again. Checking only the current
idle status misses that race: compare the work generation before and after slow reads, and serialize
the final commit with observing new work. A tool-result tail is not final-answer evidence; retry a held
receipt when the answer arrives without another working tick. Likewise, an initial reconnect RPC must
not replace a live session notification received while that RPC was in flight. A connection identity
and a local session revision protect that boundary without changing the node protocol.

Pitfalls, odd behavior, and the trade-offs made at the time (formerly "Lessons" in `CLAUDE.md`; index in `docs/lessons/README.md`). Add new lessons to the relevant section, or create one if none fits.

## Usage scans must wait for launcher state (#2220)

- Transcript scans consume file offsets and reply deduplication IDs. Looking up launcher ownership before persisted session state is available permanently assigns those replies to the unknown launcher; later state loading cannot repair the consumed history.
- Start the first full usage scan only after a successful state load, not merely after the load attempt finishes. On a failed load, preserve the hub's previous window rather than publishing unknown attribution: the meter waits until a later try on the same connection (`reloadState`) loads the state, or the connection ends. Cancellation must release the waiting meter.
- Regressions use synthetic transcripts and a captured RPC writer, with historical and current replies in the first report; no real transcript directories or hub database are needed.

## One silent address must not take the whole dial (2026-11-08, #1181)

A name with several addresses, dialed one at a time with one long timeout, fails as soon as the first address drops SYNs: the attempt sits until the
caller's budget runs out and the working addresses are never tried. curl and browsers move on, so the same URL "works everywhere else". On one host a
news site's first IPv4 addresses often never answered while its IPv6 one always did, and most reads through the source runtime broker failed at the
extension's own 15-second request budget. Race the already checked addresses (`dialRace`): interleave families, a short attempt delay, bounded
attempts, losers closed. Do not reach for `net.Dialer.FallbackDelay` here: it resolves the name itself, which would dial addresses the egress checks
never saw. Making room by canceling an old attempt must give its address another turn later, or the only address that answers slowly is lost. A real
dialer's own timeout at the request's deadline can arrive before the context reports it, as an "i/o timeout" that is not `context.DeadlineExceeded`:
decide by the deadline, not by the attempts' errors. Test the scheduling with a fake dialer in a `testing/synctest` bubble, whose clock makes the
order and timing of attempts exact; it proves nothing about real sockets' error identities, so keep a real-socket test for those (a loopback listener
with a full accept queue never answers a SYN on Linux).

## A deploy that rewrites a unit decides which program the service runs (2026-11-08)

`scripts/deploy/hub-systemd.sh` writes the hub's unit on every deploy, and for a unit that differs from its own text it adds a drop-in that overrides `ExecStart`. So once a unit can run another program (`persona-hub run` instead of `personad hub`), a script that writes the old `ExecStart` quietly switches the install back, and nothing in its output says so. Whatever writes a service file has to read which program the service runs now (the last `ExecStart` of the unit and its drop-ins, unquoted as systemd reads it, compared by real path) and keep it; only the owner's own step (`hub setup`) moves an install from one program to another. The same holds for the launchd branch, which looks the agent up by its program. Every reader of that question (the deploy script, the hub's update check) has to read it the same way, so run them over one table of the forms systemd accepts (`%h`, single quotes, spaces around `=`): a form one reader missed moved units back silently. A unit that names the program in a form the reader cannot resolve is left to its owner with an error, not rewritten. A command's default can move an install too: `hub switch` without `--binary` once followed the program that ran it, so `persona-hub switch` (which messages told people to run) moved a `personad` hub onto `persona-hub`. It goes by the program the hub runs now, and with nothing serving keeps its old default, `personad`. Check what the edge accepts before calling such a default a way out: while it backs off a failing slot it refuses any switch, so the default only reaches a switch it takes once a slot is up again, and there it moves a hub that runs `persona-hub` to `personad` until its service restarts (give `--binary`).

## Approved official-market pins outlive built-in constants (#1157)

Changing a compiled public key does not update a pin already saved in Postgres, and the registration marker can hide that difference. Migrate only explicitly recognized retired official pins, including every recognized repository or HTTPS alias; never infer trust from a downloaded catalog or replace an unknown owner choice. Save the legacy registration marker before migration so a removed market stays removed. A failed pin write must keep loading retryable.

For embedded signatures, retain the complete envelope and verify the exact decoded payload bytes on restart. Postgres JSONB may normalize the outer envelope without changing those bytes; compare the decoded payload, not the envelope's formatting. A legacy detached payload or stale cached signed flag must not authorize installation. Cache quarantine can legitimately write the old record before pin migration: inject failure specifically on the new-pin write, rather than claiming a generic cache-write error proves migration retry. Keep every HTTP fixture local, including automatic refreshes of quarantined caches. Observe successful catalog delivery in rejection tests as well as `errors.Is(err, extension.ErrCatalogUnverified)`. `ErrCatalogUnavailable` must not lift a rotated cache's quarantine, and it must preserve the new key's catalog once that envelope has genuinely verified. Never infer these categories from localized message text.

## A socket systemd passes reaches every child unless it is taken first (2026-11-08)

Sockets passed by socket activation (`LISTEN_FDS`, descriptors 3 and up) are not close-on-exec, and Go's `os/exec` does not close what is not: every process started before the hub marks them is handed the hub's listening port, and hands it on to everything it starts in turn. The hub without its edge marked them only when it first listened, and it could start the built-in manager before that; the manager, the local node, extension programs and the agents the node started then all held the hub's listening socket (several pids on `ss -tlnp` for the hub's port). Take what a process was handed first thing, before anything can start a child (`claimInherited`, `osproc.SealInherited`); hand a child a descriptor only through `exec.Cmd.ExtraFiles`. Never seal in `main` of a binary whose helper commands hand descriptors on by exec (the sandbox's launcher passes its mount data to bwrap that way).

## A process that execs itself inherits its own children (2026-11-10, #2978)

`syscall.Exec` keeps the pid, so every child of the old image (the update watchdog, the herdr server, bridges, the hub's manager) is the new image's child, and no `exec.Cmd` there knows it. Once one exits it stays a zombie until the process ends; `init: true` in Docker does not help, since the zombie's parent is personad, not init. The resident node, the hub and a manager all replace themselves this way, so each calls `osproc.ReapInherited` next to `osproc.SealInherited`, before it starts any child: it waits for each pid listed as its child then, never for -1, which could take a child an `exec.Cmd` waits for.

## A fixed name in the temp folder is shared on Linux (2026-11-07, #66)

`os.TempDir()` is the per-user `$TMPDIR` on macOS but the shared `/tmp` on Linux, so a fixed name there (`persona-herdr-server.log`, `persona-orca-images`) looks private on a Mac and is not on a Linux node. Another local user can create the name first: a symlink makes the next `O_CREATE|O_TRUNC` open empty and write into a file of the node's user, and a world-writable folder receives every file written into it, since `MkdirAll` succeeds on an existing folder whatever its owner or mode. Keep such files under `privateCacheDir` (`internal/node/cachedir.go`: the user's cache folder, checked to be a private folder of this user; a per-process `os.MkdirTemp` folder when there is none).

## An expiry written by one clock and checked by another (2026-11-07, #6543, #6553)

Revocation ended pairing codes with `expires_at = now()` (Postgres's clock) while the hub judged them by its own `time.Now()`: with Postgres in a VM whose clock runs ahead (a macOS hub with Docker Desktop or colima), a revoked code kept working for the difference. When a time is written on one side and compared on the other, make it past by both (`LEAST(expires_at, now(), $hub_now)`, `endedNow`). A time the hub hands out has the same problem even when every check is in SQL: a voided manager install code still showed a future expiry in the roster (#6553). Tests that compare a database-written time with `time.Now()` fail on such a machine, while Linux CI, with Postgres on the same kernel clock, never shows it.

## Failed settings loads must remain retryable (#5827)

- A loaded flag means a complete successful read, including an absent row. Decode into temporary state and publish it only after success; a canceled or failed read must not leave partial settings cached. Require a successful load before changing and saving settings, so a transient database failure cannot erase update history or bad builds. Keep rollback markers until their history and bad-build entries are persisted, and gate automatic update decisions on loaded settings.

## A settings read error is not an absent choice (#5827)

- Return read errors separately from a missing setting before applying defaults or migrating a client's choice. A failed conditional write must neither overwrite the saved choice nor broadcast a replacement. Test a failed read followed by a successful retry, including zero writes and broadcasts during the failure.
- A GET that returns an empty preference document on a failed read can erase saved data indirectly: clients accept the empty copy and send it back in their next full-document PUT (#1865). Check the read's success flag at the HTTP boundary, even when manager events already check it. Serve a successfully cached copy or return an error; test cold-read failure, recovery, all preference fields, and cache retention separately from a genuinely absent setting.

## Registration success requires persistence (#5827)

- Return device and Live Activity save errors to HTTP callers so they can retry. Keep logging in shared persistence helpers; registration and grant renewal publish only committed candidates, while failed maintenance retains retry state. Bound request writes by both the request context and the existing 5-second device-store timeout; test cancellation and the inherited deadline with a waiting store stub. Use typed `i18n.Err` values for localized HTTP summaries: wrapping translated text in a plain error lets the shared API error mapping replace it with a generic diagnostic message.

## Rolling-upgrade identity contracts (#6223)

- Treat a rename as an ABI change wherever a byte reaches another process, persisted state, a cryptographic domain or an operating-system registration. Use an explicit authenticated feature, an old-safe default and exact aliases; display names and build strings do not prove compatibility. Push-to-start needs a persisted registration capability, independent of the socket's XML capability.
- Connection capability changes must invalidate unused warm processes (#3269). The devices manager can reconnect to another hub while retaining the node connection, so a Claude spare can carry the previous MCP namespace policy. Record the policy at process start and require it to match before adopting or reusing the spare. With a capable hub, assistant resumes can also use only `persona`: a real Claude Code 2.1.322 transcript containing a legacy tool call accepted the next request after the alias was removed. Ordinary Agents' shared generated MCP files still serve automatic resumes without a per-session decision and cannot be narrowed using only the current hub capability.
- Measure a headless assistant with its actual environment and registered conversation identity (#4238). `system/init` under ToolSearch, or a manually spawned MCP process whose session was not discovered as the assistant, exposes a smaller tool set than the real request. Compare the request's `api-request-shape` and first assistant usage together; an init tool count or a standalone `tools/list` alone is not the fixed-context baseline.
- A deterministic receipt or thread seed, HPKE info or grant AAD is an invariant, not copy. Keep the original output bytes; decoding can accept both explicitly known domains. A dual-key JSON payload cannot repair encryption the old receiver cannot open. Never rotate a key, token or database to hide incompatibility.
- Normalize typed protocol fields only. Generic recursive replacements can change unrelated event sources, user text and large opaque JSON numbers. Preserve field presence (including false and null), use exact aliases, and project detached manifest validation values without rewriting signed package bytes. Both copies of forwarded headers must be removed together, or the legacy copy can resurrect a forwarding loop.
- Keep custom JSON methods off an anonymously embedded DTO. A method promoted from the embedded launcher swallowed its surrounding health and agent-type fields on both encode and decode. Use an explicit root transport envelope and test that embedding the plain DTO preserves sibling fields.
- Child-process environment updates must mirror both names with current-name presence taking priority. Updating only one test database or manager root variable leaves old child binaries attached to the parent's previous value. Real node verification belongs in isolated PID/network/home containers: a node scans process environments and must not inspect other agents on the development host.
- Preserve an existing service, executable and data store instead of creating a second identity. Refuse independent installations; permit verified aliases. Atomic binary replacement must target the real file so both executable names remain attached. A discovery goodbye removes one service record, not a hub still advertised by the other service.
- A Compose fallback between two spellings takes no `?` check in its default (2026-11-07): Compose before 5.0.2 (2.x included) evaluates `${NEW-${OLD:?…}}`'s default even when `NEW` is set, so installs that set only the new name failed. Leave the default empty and let Compose's own error for a missing value stop it (an empty image: "neither an image nor a build context"). `scripts/test/test_docker_entrypoint.py` rejects a nested check.

## Retired look IDs are still reserved (#6219)

- Removing a built-in drawing does not make its ID available to extensions. A saved setting can outlive that drawing; letting a new package reclaim the ID would silently change the user's choice. Share reserved-ID validation between hub disk packs and extension manifests, test old manager reports as well as new installs, and always send a valid `current.effective` fallback. Keep the raw choice and timestamp so temporary extension unavailability retains its existing restore behavior.

## Legacy component-page UI versions (#6305)

- Preserve the HTTP caller's UI version when mounting a component-page session. Fitting a modern tree afterward cannot recover a version-aware callback's in-page navigation: forcing version 6 makes it choose push/pop, which older clients correctly omit. Source context and final snapshot fitting have distinct responsibilities; keep both the requested context and unsupported-effect gates, with regressions for old in-page state transitions and modern native navigation. The manager's private callback tree must still be fitted to the canonical version: downgrading that internal tree strips required keys and rejects the session. Copy render parameters for canonical fitting without changing the source's mount context.

## A gate and a connector must read a call's field names the same way (#5278)

- The write gate reads a call's arguments into a map and compares field names exactly. A connector that decodes the same bytes into a struct with `encoding/json` matches a name without regard to its case (Unicode simple folding), and a field given more than once does not simply resolve to its last value: a `null` is ignored, and an object or an array is decoded onto what the earlier one left. A rule judged on the gate's reading can therefore be wrong about what the connector will do, so the two have to compare names, and resolve repeats, the same way.
- When a decision is made on arguments that another decoder reads later, compare names the way that decoder does, and refuse the call where the readings can still differ rather than ranking them rule by rule. `connector.Decide` refuses a call that gives a field more than once, the same name or one that folds to it (`repeatedField`, top level only), and a deny or confirm compares names without regard to capitals (`matchFieldsAny`) while an allow keeps the exact reading, so a rule that lets a call through is never wider than one that refuses or asks. Test the folding against `encoding/json` itself (`TestTwoSpellingsAreWhatTheDecoderFolds`), not against a list of cases. A gate that matches a body before some other service decodes it (the node's Feishu gate) depends on that service's rules and is not covered.

## Bracketed registry hosts (#1324)

Splitting a registry at its first colon reads `[::1]:5000` as `[`, selecting HTTPS for a local HTTP registry. Extract the hostname with `url.URL.Hostname` and compare IPv6 addresses with `net.IP.Equal`; keep the original registry spelling and port in the request. Test compressed/expanded loopback, a real TCP6 request, and controls retaining external HTTPS and existing IPv4 behavior.

## Scrubbing patterns: Go tries regexp alternatives in order

- Go's `regexp` is leftmost-first: at a position, the first alternative that matches wins, not the longest. The error-report scrubber listed the five-group IPv6 form before the `::` forms, so `2001:db8:1:2:3::5` became `<ip>::5`. Its `::` form also took one group after the `::` and needed a word boundary at the end, so `[2001:db8::c3d4:e5f6:7890]:443` kept `:e5f6:7890` and `2001:db8:1:2::/64` stayed whole. Put the longer forms first, and test a scrubbing pattern with every shape of what it hides (compressed, ending in `::`, bracketed with a port, with a zone), not only the full form.

## Lost initial native snapshots (#6251)

- A successful open can allocate a page before its first snapshot reaches the client. Retrying that request after disconnect must reuse the same owner-bound allocation, not reserve another quota slot. Store only a request ID, a bounded-body digest and a completion channel on each live session; concurrent duplicates wait outside the session mutex. Changed payloads and live-connection theft remain rejected. A disconnected retry uses the existing authorized snapshot-first resume path. Independent page instances need independent open IDs; legacy HTTP checkpoints do not share this identity.
- Recovery must distinguish an expired allocation from a transient inactive/busy request. Emit structured codes only from known manager-owned catalog errors, not localized text or guest messages. An `expired` lifecycle reply may remount; `inactive`, `invalid` or generic `rejected` must not blindly allocate a replacement. Display text remains localized and is never authorization.

## Database-move diagnostic timestamps (#6212)

- A persisted move time is an absolute instant. Formatting it with `.Local()` and a minute-only layout makes operational errors depend on the hub machine's zone and loses the offset. Use explicit UTC RFC 3339 for these database-move diagnostics; normal user-facing calendars and schedules still use the configured user zone. Test equivalent UTC, positive-offset and negative-offset inputs, and keep the time-zone guard unchanged.

## Native stream capability reporting (#6186)

- Implementing a manager's stream RPC and accepting a client's opt-in does not advertise server support. Both real client adapters fell back because the authenticated hello lacked `extension_ui_stream_v1`. Report the manager-owned transport capability through the existing extension report and hub feature aggregation, independently of installed pages. This is not a renderer, source-runtime availability, permission or resource-enforcement claim: client opt-in, native context/vocabulary checks and all activation/authorization gates still apply.

## Equivalent cached results on source UI resume (#5956)

- Resuming hooks can read the same cached data and still publish a fresh wrapper, unnecessarily changing the render version and invalidating a displayed legacy callback. Preserve equivalent results using their service/request identity, cached timestamps, item references and missed/error semantics, while still publishing genuine changes and new requests. A bundled Program regression plus actual HTTP callback/navigation tests catches this; merely checking that a fitted View loads does not.

## Finite source-page snapshots (#1184)

- Mount → snapshot → suspend can cancel every asynchronous load before it publishes, even though native open-session tests pass. Mark finite loads explicitly with `useLoadEffect`, negotiate the bundled readiness contract and select snapshot transport from the manager's legacy session, never UI version. Do not await arbitrary subscriptions or raise deadlines. Count fetches, cancellations and rendered content across cold mount/resume turns.
- Keep raw namespace params and page cancellation as independent dispatch inputs. Both fixes changed the same internal third argument; composing them must retain original frame text and a separate scoped signal. Test them on one connection with a legal large int64, a rejected decimal and a canceled page callback while its service sibling completes.
- Install callback guards before invoking asynchronous work, recheck abort/visibility/generation after synchronous flushing, and release finite-load admission per completed task rather than imposing a lifetime ceiling. Recovery cannot turn suspension into success. Late work from a retired allocation has no authority, even though independent allocations are not globally serialized.
- Required source-page integration tests need the official bundler on their server shard too. Installing Bun only in automatic/runtime CI does not provide it to a separate Go runner. Pin the same version before the `other` shard checks, and verify setup order and package selection with a regression; do not hide missing build dependencies by skipping the new contract checks.

## Copied SDK commands need their development inputs (#6132)

- Copying a package's scripts without their referenced configs, helper scripts and typecheck fixtures leaves a plausible-looking but unusable SDK. Keep the official TypeScript copy complete, including its dependency lockfile; exclude installed dependencies and build output. Test a fresh destination, not only the source checkout, and verify selective copying leaves the other SDK untouched. Schema-conformance fixtures must also be collected from their canonical source into the SDK package and archive; tests cannot depend on an outer repository schema directory or silently skip missing fixtures. Installing these declared development dependencies is a build-time operation, never source-runtime dependency resolution.

## Exact initialization metadata belongs at the initializer (#1777)

- Namespace raw params cannot recover configuration tokens already lost before `Program.init`. Use the explicit initializer opt-in on the original `initialize.params` frame; keep parsed defaults and ordinary two-argument calls unchanged. Direct authoring probes have no raw frame and must not invent one by serializing parsed values. Raw initialization may contain credentials: never log it.
- Preserve the initializer receiver, single getter lookup and single-flight/retry behavior. Only an own data marker opts in; inherited/accessor markers do not. Use the function's actual invocation rather than its replaceable `call` property. Resolve the initializer symbol lazily so unrelated source imports gain no new top-level side effect.
- Optional invocation skips only null/undefined, not falsy non-callables such as false, zero or an empty string. Preserve rejection and failed-init retry for own and inherited members when factoring invocation into a helper. Run the complete portable/source-authoring gate as well as Bun tests: a browser-bundled Node fixture can pin an existing failure contract that a focused new suite misses.

## SDK transport shutdown and error identity (2026-11-07, #6125)

- At input EOF, feed the final unterminated frame, then call `connect().shutdown()`: stop input, reject unreachable outbound hub calls, and only then drain accepted request answers. Draining first deadlocks a handler awaiting a hub reply; an idle runtime can instead exit early and hide the missing answer. Keep the writer open through shutdown and flush it before closing. `drain()` alone remains nonclosing, and outbound `close()` does not change inbound initialization checks. Arbitrary handler work and event lifetimes still need runner-owned bounds.
- Separately bundled SDK classes have different constructor identities. SDK public errors carry a shared symbol marker so code/data survive that boundary; native errors and unmarked lookalikes remain message-only. The marker is a serialization convention, not authorization or a security boundary. Both SDK copies must be updated; unchanged extension copies need the official SDK resync and rebuild.
- Handler completion does not flush piped stdout. A delayed reader reproduced a truncated 4 MiB EOF answer even with exit code 0; on Bun, an empty-write callback did not fix it. After shutdown, await `process.stdout.end()`'s completion before forcing exit (not in HTTP-only mode). Guard error-brand and field access against throwing getters/proxies, and normalize the accepted legacy `code: null` hub reply to an absent code so its public data survives reserialization.

## SDK runtime boundaries (2026-11-07, #6082)

- Bound array length before allocating or scanning it, including keyed grid/carousel tiles. `map` skips sparse holes, so per-item key and prop checks alone do not bound allocation or reject empty tile slots. Capture the checked length and visit numeric slots: an overridden array iterator can omit holes or reorder tiles. Ordinary JSX child arrays already have a separate traversal budget.
- A fetch effect runs after snapshot publication. Tag saved results with the current request identity and derive loading/idle for changed URL/dependencies during render, without writing state there. Rotate identity even when a URL returns while effect capacity delays work; dependency equality alone can resurrect an earlier result. Use a primitive identity across the state adapter boundary, since valid adapters can clone stored values.
- JSON parsing does not validate a protocol envelope: `null` parses successfully. Reject nonobjects and invalid dispatch/error field types before routing or consuming a pending reply, while retaining valid legacy process frames and routing precedence. A reply must contain a result (including explicit null) or a valid error; an id alone must not consume a pending call.

## A child that dies at once fails the write before its exit is seen (2026-11-06, #6009)

- The node writes `initialize` to `lcu`'s stdin right after starting it. If the process has already ended, the write fails with "broken pipe", or with "file already closed" once `cmd.Wait` has closed the pipe. That can happen before the goroutine that waits for the process reports the exit. So the caller got a raw pipe error or "the process ended", depending on which came first. `TestLCUProcessThatDiesOnStartFailsAtOnce` failed this way in 16 of 500 runs with `-race`.
- Classify the write error instead of waiting for the exit (`writeFailed`). A closed pipe (`EPIPE`, `os.ErrClosed`, `io.ErrClosedPipe`) is the process's end: mark the session failed, so the next call starts a new process, and return the end at once, wrapping the write's error. Any other write error stays itself.
- Waiting for the exit is a trap. A child of `lcu` can keep its stdout open, so the exit isn't seen for as long as the child runs. A first version that waited up to 2 s held the call lock that long and still returned the raw error, and never restarted `lcu`. The tests now cover that case and a write error that isn't a closed pipe. Tests compare with `errors.Is`, so an error that always says "ended" can't pass them.

## LCU release layout is part of its integrity check (#4435)

- A release bundle records regular-file modes and symbolic-link targets as well as hashes. Creating every file as 0644 or omitting links breaks that layout; even a restrictive process umask must not change the recorded executable modes. One valid release link is initially dangling and becomes usable only when its in-tree target exists.
- Create ordinary files before links in an empty private staging directory, then validate the complete relative-link graph against the staging filesystem after creating links. A lexical check of each target alone misses an escape through another link followed by `..`, and a case-sensitive map misses aliases on a case-insensitive filesystem. Reject collisions, cycles, privileged permission bits, group- or other-writable files and unsupported archive entry types. Link mode bits do not grant file write permission. A synthetic manifest and a read-only comparison with the public archive exercise the layout without running its installer.
- A shell wrapper may forward the runtime-only argument to an adjacent installer without containing that literal itself. The supported wrapper is pinned to its reviewed v0.13.6 bytes; its adjacent regular installer is checked for the expected tokenized runtime-only option, argument binding and return before setup, so some unrelated target changes do not require a new hash. The bounded token check ignores comments and string contents and fails closed on slash operators, including division and regular-expression literals. It cannot prove arbitrary JavaScript control flow or the behavior of imports. The fixed invocation still supplies runtime-only and yes, but a same-origin release checksum is not an independent signature, and the legacy direct-script marker is a heuristic too. Verify both forms with synthetic, unexecuted installers and compare the public wrapper and target without invoking them.

## Separately bundled component pages and callback results (#6129)

- A page bundle and the embedded transport are distinct SDK module instances. A Deno fixture failed with “only SDK elements are allowed”; changing only the element/native/callback symbols then failed with “hooks require a mounted function component”. Capture Renderer construction in `componentPage` itself, so its JSX, native symbols and hook stack all come from the same module. The trusted transport still supplies invocation-ticket authorization; the factory is not user approval.
- Awaiting `Renderer.event` without retaining its return value silently dropped native toast/navigation/review results. Detach and bound presentation JSON before caching the callback promise, validate its schema and native push/URL gates on the manager, and retain original result bytes in receipts. Clients apply the consumed-version fence before presenting and deduplicate by session/epoch/event ID, not request correlation. HTTP callbacks return the same native presentation subset and reject stale checkpoint retries. No result can supply another tree, HTML, executable code or authority.

## Native keyed UI trees must retain source presence (#5956)

- Generated Go `Component` uses legacy `omitempty`: decoding then re-encoding drops optional `false`, zero, empty strings and arrays. Stream wrappers restore required fields only. Keep validated raw JSON as the canonical tree; generated DTO accessors are detached projections, not transport serialization. `wire.CheckViewJSON` checks native semantics without that lossy round trip.
- `encoding/json.DisallowUnknownFields` accepts case-insensitive aliases. Strict native streams check exact generated JSON tags as well as the source schema before normalization. Bound byte size, depth and numeric conversion before decoding; a small operation can still make the combined serialized tree exceed its budget.
- Stage a batch privately, validate the whole resulting tree, then publish. Count root, sections, components and tiles in one key namespace; tiles initially change only through their containing grid/carousel. Callback IDs are saved payload, never regenerated while diffing unchanged keys.
- Protocol 1 now permits an empty delta to advance exactly one revision as an accepted-event fence (#5956). Every invoked or ambiguous callback consumes a wire revision, including no-UI writes; prune receipts only behind that fence. Source render revisions are independent of coalesced wire revisions. Never hold session locks over process calls or source notifications, and enqueue the recovery snapshot before allowing deltas.
- Native fitting uses detached DTOs for the existing downgrade rules but restores unchanged source properties by stable key and component kind. Validate the original source `UIPageSnapshot` before decoding or fitting: otherwise malformed UTF-8 or lone surrogates can normalize into apparently valid native keys/text. Stateless legacy HTTP callers of JSX pages use owner-bound server sessions with opaque version checkpoints; suspend UI work after every HTTP response, without stopping extension service work.
- Source-reader notices must only validate and coalesce; run native fitting and diffing outside the global session mutex in bounded per-session workers with a manager-wide concurrency ceiling. A full fitting queue must not prevent the source reader from consuming RPC replies or acknowledging suspension.
- Bind the initialized process generation before dispatching a root factory, and dispose canceled mounts after their RPC dispatch finishes. Recovery must collect effect renders that precede its RPC reply, but a newer suspension always wins; use a visibility revision rather than clearing suspension unconditionally. Legacy HTTP ownership lasts through its deferred suspension acknowledgment. An expired checkpoint may remount only on a read-only redraw, never on an action; an explicitly empty stream component list is not the unrestricted legacy list.
- Stream callback validation rejects disabled controls without blocking another action on the same row or tile. Credential-free HTTPS checks also cover code `line_url` templates; the legacy line-link semantic check permits user information.
- Close/dispose must cancel an outstanding mount before joining its RPC call; waiting for a root factory's response can otherwise consume the full mount timeout before disposal reaches the source. Keep HTTP checkpoints isolated from app-socket recovery, and check component-page support before interpreting an opaque legacy state prefix. Socket routing failures retain both the request ID and selected manager.
- Component-page native receipts need no more than the manager's 64-entry replay window; larger caches retain presentation results that the wire revision fence already makes unreachable. Restore fitted keyed arrays through a single previous-key index rather than a repeated sibling scan.

## Cleanup after a change races work that read the state before it (2026-11-06, #5932)

- Every registry change starts a sync of every machine in the background. A sync reads the registry once, then builds packages and records machine states for what it read. `drop`, which runs after an upgrade or an uninstall, removed the old version's packages and states, but a sync that had read the registry before the change rebuilt or recorded them afterwards. Nothing removed them again. This was the flaky `TestNodeProgramsForgotten`: 30 of 300 runs failed, and none did with the background syncs turned off.
- Such a write has to check that its version is still the installed one, under the same lock as the cleanup (`keepNodeState`, `nodePackage`). A check made before taking the lock, like the old `reg.Get(id) != nil` in front of `set`, still leaves a gap. With the check under the lock, a write either lands before the cleanup, which then removes it, or is refused.
- A test that asserts the cleanup on disk first has to wait for the work in flight. A refused sync can still create a file and remove it a moment later, and a glob can catch it in between. Running a sync first does the waiting, because syncs share a lock per machine.
- Keep a test that reaches the check that decides. An early check of the same condition outside the lock (here, before building the package) refused every stale snapshot first. With the locked check deleted, the test still passed 1,000 of 1,000. Without the early check, deleting the locked one fails the test 1,000 of 1,000. An optimisation for a rare race is not worth hiding the check that matters.

## Shared bot identities across managers (2026-11-06, #5826)

- Compare each manager's extension report before merging away ownership. Ignore repeated identities within one manager, empty identities, and managers removed from the roster. Offline reports describe the last known configuration, not confirmed live connections.
- WeChat's displayed account label may be localized or truncated. Compare the stable `user_id` in its encrypted binding instead, and refresh the report when binding secrets are saved or deleted as well as when the channel is enabled or disabled. Never report the credential object or access token.
- The same secret store also saves WeChat polling cursors and context tokens. Compare the account identity before rebuilding an extension report; an unchanged account does not need to rebuild its look assets on every poll.
- Identity changes must send the existing `managers` event; otherwise the warning stays stale while Settings is open. Keep raw identities out of the roster response: the platform and matching manager IDs are enough for the warning.

## Permission disclosures and legacy UI fallbacks must use the response language (#5057)

- Keep compatibility-origin error resources separate from polished response summaries: changing the English resource used by `ErrEnglish.Error()` also changes legacy clients without a language header. Project explicit-language HTTP responses and old flattened status copies onto the new summary key instead.
- Generate permission titles and descriptions from the manifest and the requested language; never reverse-translate extension-authored names, secret titles, comments, or view text. Scope IDs, consent digests, and package fingerprints stay language-independent.
- A staged upgrade preview captures its old manifest. Return a presentation copy with regenerated permission lines, including bundled previews, rather than mutating the stored preview or using whatever version happens to be installed later.
- UI downgrade captions, default loading labels, file badges, and review actions are hub-owned text too. Pass the UI locale through all fitting paths, including widgets and shortcuts; absent body locale retains the user's legacy language. Widget cache keys already include locale.

## Large Agents lists have two different collections (#5929)

- Claude Code / Codex runtime sessions come from `/api/v1/sessions`, not `/api/v1/agents/sessions`; the latter belongs to extension personal-agent bridges, and `/api/v1/agents` is source/setup status. Check client callers before optimizing the similarly named endpoint.
- HTTP pagination alone does not bound startup traffic: the existing `sessions_delta` socket still sends a whole initial snapshot. Opt into `sessions_list` for lightweight invalidations and HTTP revision recovery; never silently change delivery for legacy Android, which still needs complete lists.
- Missing task arrays in a row summary do not mean “nothing running.” Preserve bounded progress/wake fields and use the scalar `waiting`/running-count summaries; do not overwrite complete open-chat detail with a list-row projection.
- A cursor freezes order and target revision, whereas a delta checkpoint advances only after all delta pages are applied. Expired cursors/checkpoints must return an explicit reset error, not an apparently successful empty page.
- Exact active-chat detail needs its own `sessions_lookup` feature gate (#6054): older bounded-list hubs ignore an unknown `session_id` and return up to 50 unrelated rows. Resolve the composite identity before row filtering/counting, reuse the full collection DTO, and never replace chat-only task/configuration fields with a summary. Exact baselines reject `since`/`cursor`; coalesced invalidations trigger a new exact read without scanning the change journal.
- Measure cold initialization separately from warm first-page requests, and distinguish allocation churn, retained heap and process peak RSS. A transport index trades bounded retained memory for far smaller repeat requests; a warm-cache benchmark alone is not a cold-start measurement.
- A small assistant overview can still traverse the entire runtime inventory (#739). Select headless conversations before decorating rows, and use typed in-process data instead of a local RPC JSON round trip. With 8,200 synthetic sessions, the old overview took 450–464 ms and allocated 205–219 MB; early selection took 5.84–6.67 ms and allocated 12.25 MB. Partial decoration must not prune other sessions' state marks or hook flags; only a complete inventory can establish that a session disappeared.
- Audit neighboring entry requests too (#1075): `readinessMain` still serialized the full inventory to find one main conversation after the overview was fixed. A verified headless identity can use exact local lookup and the same partial decorator, with the legacy/unusual-session fallback preserved. The identical 8,200-row benchmark went from 479–596 ms to 2.71–3.26 ms. Keep first-use history parsing and large initial feed responses separate from warm switching claims.

## Extension version notices must preserve localization through the response chain (#3598)

- `engines.persona` supports both `>=N` lower bounds and `<N` upper bounds. Suggest updating the hub only when below the lower bound; reaching the upper bound is also rejected and must not use the same update advice.
- `CheckEngines` errors return directly from installation APIs and are also stored by the registry for lists and enabled-extension details. Merely switching to `i18n.Err` is insufficient: converting to a `Problem` string loses the translation key and arguments. Preserve the original error and localize when generating responses in the request language; localize market `problem` values too.
- The extension manager receives the request language through hub forwarding, but synchronous `apierr` responses also require the response writer to implement `i18n.Carrier`. Checking only asynchronous errors misses synchronous installation previews. Preserve `Unwrap` when wrapping writers so streaming responses and WebSockets still reach the underlying writer.
- The CLI manages extensions through the same HTTP APIs and must explicitly send its default language, or errors follow the most recent client language. Test request-language switching and the CLI's fixed language, not only translation entries.

## Collections can't simulate transactions with individual installs and uninstalls (#2466)

- `seen` for repeated dependencies may stop traversal, but mustn't skip stricter version or market constraints from later branches. Select the full reachable graph first and reaggregate when candidate versions change dependencies; preserve installed members satisfying constraints, including transitive dependencies of disabled members. The 4-level limit restricts supplementary installation depth, not constraint collection in installed branches. Joint upgrades must check the entire replacement graph, not falsely report cycles by comparing one new version with the old graph.
- A permission summary isn't a package fingerprint: the complete downloaded manifest must match the market description, and every package path, file mode, and content must bind to the preview. Binary content may contain NUL; hash input must include content length, not just delimiter concatenation. RawMessage object-key order in manifests shouldn't count as a content change.
- Validate all previews, digests, source trust, engines, contribution namespaces, complete dependencies, and existing config first; prepare all packages, write all records in one PG transaction, then publish together. Missing new required fields may still yield `needs_setup`, but existing values incompatible with the new schema must be rejected before writing. Market guards preserve address / trusted public key across commits and recheck after callbacks.
- On ordinary commit failure, restore packages in reverse order; don't uninstall and delete old records, sealed secrets, or data. Put backups in a separate directory, not the preview root that may be moved wholesale into installed; keep valid previews for retry. Serialize writes, recheck current versions before post-commit prune, and call OnChange outside the write lock to prevent restore / late cleanup overwriting another request or callback reentry deadlocks.
- A lost COMMIT response doesn't mean PG rolled back. On unknown outcome or package-restore failure, retain packages and backups, don't publish success or activate, and reject later writes until hub reload verifies state; don't pretend "the original install is restored, safe to retry". External side effects after program startup aren't part of this transaction.
- Fault injection uses only fake Stores or a temporary database dedicated to this test binary created by `dbtest.TestMain`. Never add triggers to shared test or production databases. Cleanup needs an independent bounded context; `t.Context()` is canceled before Cleanup. Each scenario cleans its own records / audit too, so later tests don't read rows left by successful retries. Preserve plan-validation error text; stage wrapping mustn't change existing rejection copy or break i18n.Localize.

## An extension being enabled doesn't mean dependencies are ready (#2469)

- Checking only direct dependencies' enabled/version status makes A in A → B → C appear runnable when C is missing; restarting only B leaves A's stale state and exported contributions intact. Check config, secrets, and transitive dependencies in one registry snapshot and propagate changes dependency-first to every ancestor.
- Dependency `not_ready` means manifest/config unavailable, not a temporarily `stopped` process; don't force-start on-demand extensions for status checks. Only `ok` may display "Satisfied"; unknown mustn't fall back to ok.
- Cycle checks use the full candidate graph and recheck at commit after preview; checking and publishing registry state must be serialized, otherwise two individually valid previews can install concurrently and form a cycle. Preserve and diagnose old cycle records, allowing repair and unrelated installs; don't solve it by deleting data.
- Exporter initialization and change events filter unready contributions; changes to underlying dependencies, config, or secrets also update exporters reached by higher-level contributions, not only the directly changed extension's export targets.

- Guessing architecture from `os` during deployment installs the wrong binary, and installation didn't check executability (2026-11-05, #3103): config contained only `os`, which `deploy.sh` translated as darwin→arm64, everything else→amd64,
  so Linux machines missing `os` and Intel Macs got `darwin-arm64`. The remote side only ran `cat >`; successful ssh printed `installed`, while that node couldn't start or stayed old.
  Build all four platforms (linux / darwin × amd64 / arm64, matching releases and Docker images); ask the machine directly for missing `os` / `arch` (`uname -s` / `uname -m`),
  then run `version` on the target after installation. The hub's `bin/nodes/` also needs all four, or "Redeploy" on other platforms only says "hub has no personad for …".
  Test this function from `deploy.sh` separately (`scripts/test/test_deploy_platform.py`), without fake machines.

- In macOS sandbox-exec, symlinks along the path must themselves be readable (2026-11-06): the extension sandbox denies the home directory and allows only real package / data / runtime paths,
  but the hub refers to them through `~/.persona/…` (symlink to `.persona`, or vice versa). Resolving a path requires reading `~/.persona` inside the denied home directory, so `execvp` reports
  `Operation not permitted`, and data access through the symlink fails too. Merely replacing directory paths isn't enough: arguments, `HOME`, and socket environment variables still contain original paths. Allow each symlink along the path separately as
  "real containing directory + its own name" with `(allow file-read* (literal …))`: this only permits reading its destination; later components still use real-path checks (reading other home files via symlinks still denied in testing).
  `file-read-metadata` alone isn't enough. Test temporary directories themselves are under `/var -> private/var`; `TestDarwinSandboxForReal` runs with a symlinked state directory and reproduces the original error when the allow rule is removed.

- Deno source runtimes (#5956): `fetch` checks the destination's net permission even with an explicit proxy-aware `HttpClient`. Permit only the registered loopback broker and rewrite global fetch through existing egress; clear inherited proxy variables for that local broker request. Dynamic local module loading needs read permission for the exact bundle. Verify actual kernel cgroup membership and limits before starting each guest: a successful systemd user scope can move only the systemd controller on cgroup v1, leaving memory and CPU in shared parent groups. Namespace/engine success is not full resource acceptance.
  - Guests can still open raw sockets to their allowed broker, so source listeners must refuse ordinary proxy requests and CONNECT before they reach legacy forwarding. Rejected routes must close the connection rather than let net/http drain an incomplete body outside the fetch deadline. A canceled context alone does not interrupt net/http body reads: set a socket read deadline before calling Body.Close, which can wait for the reader's mutex. HEAD entity lengths are metadata, not the framing length of the broker's POST response. Bounded RPC peers must cancel connection-scoped work on closure and return a bounded error when a result exceeds the frame budget.
  - Bundle byte limits and a parser mutex do not bound AST allocation or recursion inside the manager. A 256 KiB artifact of repeated expression statements allocated about 13 MiB during validation; this is allocation amplification, not measured peak residency or a kernel resource limit. Resource-bounded artifact validation remains a separate security gate; guest cgroup tests do not establish that protection.
  - Keep syntax construction and traversal entirely in a disposable native validator, including offline build and package-preview paths. Check effective kernel limits before parsing, and recheck them inside the namespace by mounting only its own read-only cgroup directory; otherwise a bare helper invocation can bypass the supervisor's gate. A bounded stdin/result and external deadline are necessary even for trusted parser code. Preserve the signed digest across the streamed input and never echo parser diagnostics, which may contain source or private paths. Do not wait for that child under a shared registry lock: commit-time integrity checks can reuse preview acceptance of the identical digest, with full process validation again before activation.
  - Go/cgo test executables need virtual-address headroom unrelated to physical memory. A too-small `RLIMIT_AS` can kill pthread startup even on harmless syntax. Native validators use explicit tagged policy, child CPU/FD/file/core limits, Go runtime memory/stack/thread settings and external sampled RSS/wall termination, not a V8 heap claim or independent `prlimit` parser bypass. Use a non-race trusted child for real bounded-process tests and race-instrument the parent separately; TSAN startup failure is not parser acceptance.
  - Never `dup3` onto a fixed FD in an already-running Go supervisor after filesystem reads: netpoll may own FD 3, causing intermittent fatal epoll failures. Keep the sealed policy at its allocated descriptor and use `exec.Cmd.ExtraFiles` to map FD 3 only during the new child exec. Rewind descriptors before bubblewrap's `--ro-bind-data` consumption. A disappeared `/proc/<pid>/status` can precede Wait's completed exit; bound that reaping race instead of misreporting a normal child exit as RSS termination.
  - Verify `pids.max` in both validator pre-parse gates too: requesting `TasksMax` is not proof that the task controller was delegated. Preserve the signed runtime artifact in a sealed memfd and pass it to the trusted launcher with explicit per-run disposal; `--ro-bind-data` consumes the descriptor before the guest starts. Reopening the checked package pathname permits replacement, and opening a parent's `/proc/.../fd` path fails after user-namespace entry. Test the descriptor through the scope and namespace separately from real resource acceptance.
  - Broker overload rejects before the admitted request's body deadline, so it must set `Connection: close` as well. Bind CONNECT-handshake socket closure to cancellation immediately after dialing, not after the proxy answers. A guest's stream-cancellation hook may never settle; start that cleanup without awaiting it when releasing fetch admission after size rejection.
  - Broker deadlines must interrupt socket writes as well as request-body reads: a raw guest can request an allowed multi-MiB response and never read it, retaining global admission after the request expires. Set both deadlines up front and interrupt both on cancellation, including registration changes. Test the stalled response writer independently of guest watchdog death. Fallback-only fixtures must skip when independent kernel enforcement is selected; production capability success is not a development-policy failure.

- Development-only source resource testing (#5956): use both the Linux `persona_dev_resources` build tag and isolated-instance `PERSONA_DEV_SOURCE_RESOURCES=1`; production rejects the opt-in and missing namespaces never fall back. Apply `setrlimit` only in a re-exec child, not the manager. Pinned V8 reserves roughly 18 GiB virtually, so `RLIMIT_AS` is not a viable replacement without breaking its sandbox; Linux `RLIMIT_RSS` is not enforced. Distinguish old heap, cumulative CPU lifetime, sampled RSS termination (including overshoot), and wall-clock death from independent hard memory/CPU quotas. Production delegation remains gate #5975. When integrating its runtime manifest with the stream schema, regenerate every language and the SDK bundle together; implement `allOf`/`anyOf`/`not` in both canonical validators rather than merely allowing new keywords in the keyword check.

- Independent resource-fallback verification (#5956): a guest can open raw sockets to its permitted broker address, so rerun native `Deno.connect` GET/CONNECT negative controls after integrating runtime changes, not only global `fetch` tests. Regenerate a conflicted adapter from the merged TypeScript and current SDK rather than choosing either generated side. Keep Bun-dependent source packaging tests explicitly skipped when Bun is absent on Go-only server runners; never describe those skips as bundler verification. Validate raw millisecond budgets before converting to `time.Duration`: integer multiplication can wrap a huge lifetime into an apparently valid one.

- Sandboxed extensions connecting to hub-machine loopback bypass the proxy entirely in HTTP libraries (2026-11-05, #2942): the egress proxy allowed literal `127.0.0.1` entries in `permissions.network`,
  but Go `net/http` (`httpproxy`) hardcodes direct access for `localhost` and loopback IPs, even with empty `NO_PROXY`. Requests never reached the proxy (`sandbox.egress` count 0) and sandbox-exec rejected them with
  `connect: operation not permitted`; Linux sandbox loopback isn't the hub machine's anyway. Reserve `host.persona.internal`, which the proxy connects to the hub machine's `127.0.0.1:<port>`,
  only for extensions listing loopback addresses; never allow the proxy's own port (`docs/extensions/extensions.md` ("Egress")). For "local access fails in sandbox, count is 0", suspect client proxy bypass before proxy rules.
  `TestDarwinSandboxForReal` uses default `http.Get` inside a real sandbox to test both `127.0.0.1` (fails) and this hostname (works).

- An ownership rule between managers has to hold on every path into the hub, not just the obvious one (2026-11-06, #5923). After #5556 made `device.call` and the cards check the caller's machine, a review still found a manager reaching another's things through `ws.broadcast` (relabelling its own card as another session's question), `notify.request`, extension pushes, `app.call`, relayed Persona Links that outlived a move or a revoke, and extension events sent to every manager. When adding a bus method, event, relay or push, take the caller (`handleBus`, `c.managerID()`), check what it names with `ownsRef` / `machineOwner`, and recheck after anything a move can change in between (`withCaller`, cards and relays checked again once kept). For what a manager sends on, allowlist its types (`TestBroadcastTypes` reads them and the hub's from the code with `go/ast`) rather than denylisting the hub's, and read it with exact keys: Go's `encoding/json` matches keys regardless of case and takes the last of a repeated key, so `{"type":"interaction","Type":"x"}` read into a struct passed a denylist while the apps read `interaction`; refuse repeated and case-variant keys and pass on what was checked, re-marshalled (#5960 review).

- Merging managers' to-do links must resolve each machine/session pair by `sessionOwner` before falling back to the built-in assistant (2026-11-06, #5829). A session belongs to its machine's owner (#5556), which may not be the built-in assistant. Go map iteration has no guaranteed order, so looking up the owner's and built-in assistant's entries directly prevents a later-visited fallback from overwriting the owner. Test both report arrival orders and nil versus empty reports: a failed read retains links, while a successful empty report withdraws them.

- To-do Agent linking and removal must lock rows in the same order (2026-11-05, #2660): acquire a session transaction lock, then lock the target and other active to-do parent rows by id, then update Agent rows, preventing concurrent link/removal deadlocks and duplicate active links. Auto-link first confirms the target isn't an existing row or dismissed marker, releases old active links, then inserts; `todo_agents_active` is an immediate unique index, so inserting before release raises 23505. Preserve soft unlink and roll back the whole transaction on unexpected conflicts. Regressions observe real lock waits and use `FOR UPDATE NOWAIT` to check order, not added sleeps or retries.

- Workspace ownership transactions and publication need one ordering boundary (#1479). A store can commit A, return B first, then return A: serializing only the in-memory map still leaves two task lists and a stale automatic-routing index. Order link, unlink, rename and task deletion with their live publication. Release the write lock for session-list RPCs, then recheck the exact link before fanout; background automatic membership writes must also verify their still-current record before persisting. Test committed-store barriers and delayed session RPCs through HTTP handlers, including reload, failure/retry and preservation of manual/spawned links.

- Extension-button notifications must check ordinary direct APNs, not just relay (2026-11-05, #2619): relay's encrypted envelope already had `mutable-content: 1`; direct delivery with only dynamic category/actions doesn't run the notification service extension on the App's first background receipt of a new category, preventing button registration before delivery. Add the field only when extension buttons exist and update `.apns` samples; plain, buttonless, and static interaction notifications stay unchanged. Check JSON actually sent to fake APNs; server success doesn't replace first-background-notification acceptance on iPhone.

- When the hub calls a new node RPC, don't cache rejection forever (2026-11-04, #1742): the hub restarts first during deployment, while resident node (`node --attach`) waits until assistant chats are idle before replacing its binary via `exec` (same pid; `restarting on` in `node-attach.log` confirms replacement). At #1700 rollout, the hub's first cleanup preceded node replacement by 27 seconds; old node returned `unknown method session.retire` for every completed Agent, and the hub cached "don't ask again unless session changes". After node upgrade none of the backlog closed. Rejections of new methods need expiry (retry here after 10 minutes); log only the first occurrence / changed reason. Regression covers "rejected → node replaced → ask again later" end to end.

- Persistent failures need increasingly sparse logs, not fixed-interval throttling (2026-11-04, #1765): on a computer without herdr, its client logged "can't connect" every 30 seconds and event-subscription retries logged another line every 30 seconds, about 5700 hub log lines/day. Fixed intervals handle occasional failures, but long-term absence is noise. Use `herdr.LogBackoff`: log initial failure, then 30 seconds and doubling up to hourly; log recovery and reset. Regression counts a day's lines with a fake clock (30).

- Session-creation HTTP budgets must match node's actual launch path (2026-11-04, #1551): clients may always send `async: true`, but node returns an immediate placeholder only for Claude; explicit or machine-launcher-configured Codex / an internal Codex variant still wait synchronously for a real session. Looking only at async makes the hub time out after 15 seconds even if background creation succeeded. Use the same async eligibility on both ends and keep 100 seconds for synchronous paths; regressions run real HTTP handlers, model config, and fake RPC, inspecting operation-context deadlines for explicit types, config-derived types, and Claude controls without actually waiting for timeout.

- After disconnect, accepted operations must use their own bounded context for to-do linking too (2026-11-04, #1551): session creation with receipts and both message-send paths continue executing, but if post-success `LinkTodoAgent` uses the HTTP request context, backgrounding or disconnect causes "operation succeeded, to-do not linked", and receipt replay doesn't repair it. Postprocessing uses the same operation context; fake node cancels the request before success, and fake persistence rejects canceled writes like Postgres, testing creation, ordinary send, and dedup send individually. RPC returns immediately if already canceled/expired and rechecks context inside the actual writer lock before writing; entry-only checks miss cancellation during JSON serialization or waiting for other writes and still send side-effecting launch requests. Regressions cancel during parameter serialization and while the writer is unavailable, asserting no request bytes on the wire; this doesn't change cancellation semantics of already-sent operations or remote execution.
- Waiting for RPC writes must honor caller context and connection closure (2026-11-05, #2994): `Peer` used `sync.Mutex` for serialized writes; one stuck write (SSH stdin to a frozen node, stalled relay WebSocket, full `io.Pipe`) left every later `Call`/`Notify` waiting forever, past their deadline and `Close`. The write entry now uses a single-slot semaphore, selecting `ctx.Done()` and `p.closed` while waiting and rechecking after acquisition; canceled requests never reach the wire. An ongoing `Write` itself can't be interrupted. A permanently blocked writer verifies short-deadline calls return on time with no bytes after release, and `Close` wakes queued calls and notifications.

- Docker Desktop gvisor forwarding sources aren't necessarily the gateway (#1030): local and LAN clients may both appear as the same public-looking address. Inspect actual peers with `netstat -tn` inside the container; don't allowlist the `docker network inspect` gateway or masked authentication-log subnet directly. Restrict host-port reachability first, then configure a single-address `security.trusted_cidrs`; admin `PERSONA_ADMIN_CIDRS` is separate. Ingress diagnostics must be independent of admin access mode.

- GitHub device-code sign-in isn't on the API domain (2026-11-04, #1106): code acquisition and polling use `github.com/login/device/code` and `github.com/login/oauth/access_token`,
  while the API uses `api.github.com`. Some networks let the hub reach `api.github.com` but time out on `github.com`; the GitHub connector works with the computer's gh login,
  then device-code login returns 502 `dial tcp …:443: connection timed out`. Verify device login through personad's actual `POST /api/v1/connectors/github/login`,
  not just curl against the API; configure `github.proxy` if unreachable (`docs/operations/config.md`).

- Don't treat every `EnvironmentFile` as unrecoverable root configuration (2026-11-03, #277): an old service may keep only unrelated runtime fields there, so rejecting it breaks upgrades using the default root. Extract only HOME assignments, honoring file fields overriding unit assignments, later files winning, optional files, and `UnsetEnvironment` last. Don't return/log other fields or execute shell expansion. HOME text inside multiline unrelated fields isn't a new assignment. Missing required files must still stop, not silently switch to an empty new root.

- Changing the runtime root requires more than fixing resident-service environments (2026-11-03, #277): SSH logins don't inherit systemd / launchd `PERSONA_HOME`; deploy, pairing, or test scripts reading fixed `~/.persona` may write binaries to the wrong place, miss old config, or rewrite service launch paths. A shared resolver first honors explicit new/old environment values (even empty new variables win); otherwise it reads root fields only from the existing user service for that role, honoring cleared `Environment=` and ordered drop-ins. Independent hub, node, and link roots on one machine can't borrow from each other. User-unit writes and lookups honor `XDG_CONFIG_HOME`. Remote resolution happens in its own HOME, never copying developer-machine paths. Keep default symlinks, concurrent initialization, and lexical paths aligned with Go / SDK so scripts don't recreate two stores. Run actual deploy entry points with temporary HOME and fake SSH / systemctl, checking binaries, web, and APK land in the original service root without running real services or creating credentials.

- Connector mode watching mustn't record only startup mtime and wait for a later change before checking mode (2026-11-03, #748): the run loop picks active/passive then starts its watcher asynchronously; if the user switches before the watcher goroutine begins, new config becomes the initial snapshot and the first switch is missed forever. Sample initial time, then immediately compare actual mode and cancel the old mode if different. Bidirectional regressions directly simulate "config changed before watcher starts", not longer waits; observing initial state doesn't mean the watcher is ready.

- Collapsing purpose models into category defaults needs more than hiding lists or making the resolver ignore old records (2026-11-03, #697): starting a task, resuming its own session, or returning to the source session may reintroduce old `Launcher/Model/Effort` execution parameters, which `applyModel` treats as explicit choices. Stop passing these old purpose parameters at execution entry points; compare only category results when deciding launcher changes on resume. Regressions check real RPC arguments for all three paths, unchanged permissions, rejected old-purpose PATCH, and preserved original records.
- Sending into an existing assistant chat (phone messages, scheduled follow-ups returning to the source) mustn't choose launchers independently (2026-11-04, #1518): follow-ups resolved the Agents category, defaulting to bare Claude Code,
  and at the scheduled time `--resume`d the main chat into a logged-out claude on the hub machine. Main-chat sends then inherited the "currently running" launcher, so they never switched back and every message reported
  `Prompt is too long · automatic compaction failed: Not logged in` (context exceeded the new launcher's window, and compaction needs API access). All assistant-chat sends use `assistantSendModel`;
  every main-chat message follows main-chat configuration, not session-state inheritance. For this error, check `node_sessions.launcher` and `is not in <launcher>'s list` in hub logs.
  Scheduled tasks themselves (e.g. a morning briefing's own chat) later moved to the main-chat category too (#1544): work in the assistant directory shouldn't use the "new Agent" default.
- An assistant turn's bounded model recovery must run **before** node publishes its idle result (2026-11-10, #3204): `watchScheduledRun` and the clients treat the first idle API error as final, so retrying in a later manager watcher races the failure push and a whole-task rerun. Keep the session working until success or exhaustion, keep the retry budget on the conversation rather than the process replaced by a model switch, and resume with hidden `<persona-context>` instead of duplicating the original user message. The launcher's `/model` list is ordered; ModelProfile.Tiers is an unordered set of Agent presets, not a main-chat fallback queue.
- Occasional launcher `command not found: claude`, or node-started chats reporting `send to claude: write |1: file already closed` (2026-11-05, #2581), usually means npm is globally reinstalling Claude Code (auto-update moves the old install first, writes the new one about 2.5 seconds later). Long-lived old claude processes (months-old sessions in botmux / herdr) each periodically detect "new version", reinstall, then never restart themselves, causing reinstalls every minute or two (7 times in 8 minutes measured). Check repeated `install --global @anthropic-ai/claude-code` in `~/.npm/_logs/` and versions via `/proc/<pid>/exe --version`; the root fix is ending old processes. node retries early "command missing" exits every 2 seconds, at most 3 times, and reports stderr instead of a pipe error.
- SSH sessions can't read macOS's login keychain: claude started by an SSH-started node gets 401 if its API key comes from the keychain (Agents in herdr work because herdr is in the login session).
  Install resident node on that Mac (`scripts/deploy/node-launchd.sh install`, `docs/server/server.md` ("Persistent node")).
- Go `UnixListener.Close()` deletes the socket file by default: during redeploy, old node exit deleted the new node's `~/.persona/node.sock`,
  silently disabling every hook / MCP (unreachable hooks allow the TUI). Fixed with `SetUnlinkOnClose(false)` and a rebuild self-check every 15 seconds.
  Suspect inactive hooks / MCP? Run `personad node doctor` first (actual socket response, generated files matching the binary, command paths still present; `docs/integrations/claude-integration.md` ("Self-check")).
- Whoever takes `node.sock` by remove-then-listen must take only what it checked (2026-11-08, #1556). Standby asked whether anything
  answered (up to 1.3 s), then removed whatever file was at the path and listened: a node serving by itself that did its own
  remove-then-listen in between lost its fresh socket file, kept listening on an unreachable one, and standby owned the path for good.
  Standby also listened again in the same round it saw its file gone, beating that node's listen. `TestStandbyNeverTakesALiveSocket`
  failed 6 of 100 runs with every CPU busy (a timeout, or the node's `bind` failing) and 0 of 300 after the fix: compare the
  `os.Lstat` identity before asking and before removing, and let a path go for a round when the file changed under you.
- macOS `lsof -p a,b,c` exits 1 if any pid has exited or is a zombie, but still prints other processes' results to stdout (2026-11-03, #541):
  a pid merely not listening doesn't count (exit 0). Short Agent commands often disappear between `ps` and `lsof`; discarding results as errors froze the port list and logged every 4 seconds.
  `lsof` complaints go only to stderr; parse stdout even on exit 1, and log the same error once.
- Other Agents may redeploy and clear hub memory at any time (three deploys in two minutes on 2026-10-25): cross-request flows (e.g. OAuth state) must be self-contained or persisted.
- Video / audio proxies must forward Range / Content-Range (AVPlayer first requests 2 bytes and rejects a whole-file response); `URL.appending(path:)` double-encodes `%`,
  so paths with `/` in IDs must be assembled with `percentEncodedPath`.
- Keeping WebSocket open when the App backgrounds makes a suspended socket appear alive to the hub, so `phone_*` waits until timeout; now disconnect on background and wake by silent push when needed.
- Hub APIs default to `Cache-Control: no-store`: otherwise iOS URLCache stores every JSON page (tens of MB of background sync, never read).
  App-managed disk caches (chat images, Lark media) fetch via `APIClient.uncachedSession` to avoid storing bytes twice.
- With a TUN proxy on the hub machine, system auto-updates restart systemd-networkd, which by default deletes other programs' ip rules (proxy routes vanish and all proxied connections time out;
  Tailscale repairs its own, the proxy doesn't). Set networkd `ManageForeignRoutingPolicyRules=no` on that machine (push stopped for about 3 hours on 2026-10-29).
  APNs is a persistent HTTP/2 connection; Go doesn't detect that it's dead after a network outage. Close idle connections when a push request errors so the next request redials (`push.go`).
- `URLSessionWebSocketTask.sendPing` callbacks require both: the pong echoes the ping payload unchanged (empty pong doesn't count),
  and a pending `receive` exists. `scripts/mock/mock_hub.py` once returned empty pong, causing App heartbeat to declare the connection dead and reconnect every minute (hub coder/websocket was fine).
- Machine monitoring (`docs/server/machines.md`): darwin personad is cross-compiled with `CGO_ENABLED=0`, so Mach calls like `host_statistics` are unavailable,
  and `vm.page_*` sysctls lack anonymous-page and compressor counts. macOS CPU / memory come from persistent `iostat -n 0 -w 10` / `vm_stat 10` (one line per 10 seconds, not a new process per sample),
  the rest from sysctl, route socket (`NET_RT_IFLIST2`), and statfs. Before manually decoding C structs (`if_msghdr2`, `struct loadavg`), print `offsetof` with a small C program to confirm offsets (`#pragma pack(4)` exists).
  Do not parse `sysctl -n` / other CLI output for numbers the kernel already exposes: under `de_DE` / `fr_FR`, `vm.loadavg` prints a decimal comma and `ParseFloat` fails. The usage notification's load used that path and reported 0; read the raw sysctl (`parseLoadavgRaw`) / `/proc/loadavg` through `sysmon.LoadAverage` instead.
  Linux virtual interfaces are identified by whether `/sys/class/net/<name>` points into `/sys/devices/virtual/`, more reliable than names (TUN proxies use varied names).
  When the node runs in a container, `/proc/meminfo` and `/proc/self/mounts` show the host: a pod limited to 32 GiB on a 512 GiB host showed 31% memory while it was close to an OOM kill,
  and the host's 1.7 TB data disk came in as directory binds (`/tmp`, `/etc/hosts`) at 28% while the container's own `/` was 83% full. Memory uses the cgroup limit; disks use mountinfo's mount root to skip directory mounts (#3520).
- macOS Desktop, Documents, and Downloads are privacy-protected (TCC): personad running over SSH gets `operation not permitted` without authorization,
  so the phone can't open files there. node explains the error (`explainDenied`): for an SSH-started node, enable sshd-keygen-wrapper in Full Disk Access; for a node a LaunchAgent started, add that agent's program (the node's own agent's binary; for the node of the hub's computer, the built-in manager's agent's `persona-manager` or `personad`, or the hub's agent's `persona-hub` or `personad` when the hub starts the manager as its child; `fullDiskAccessProgram` reads it from the agent's plist, by the label in `XPC_SERVICE_NAME`); or use another folder (#5944, "A LaunchAgent node is throttled, and a protected folder stalls Claude before any hook" below).
  Put user-visible files (screenshots etc.) in an ordinary directory under `~/`, not Desktop.
  TCC attributes permission to the responsible process: SSH-started node and children all count as `sshd-keygen-wrapper`; granting the personad binary itself doesn't help (and redeployment replaces the binary and loses authorization).
  Granting it gives every SSH login Full Disk Access (including `~/Library/Messages/chat.db`). To authorize only one program, launch it via launchd (LaunchAgent), see `docs/plans/plan-connectors.md`.
- If node differs from the hub version (`node_mismatch`), App "Redeploy" asks the hub to install it: hub deployment leaves platform node binaries in
  `~/.persona/bin/nodes/`; the hub installs via SSH, kills resident node for launchd to restart, then reconnects (`devices/deploy.go`). Deploying only one machine, not the hub, leaves old binaries there and the button reinstalls the old version.
- SSH remote commands are parsed by the target's **login shell**, not necessarily POSIX (2026-11-04, #1770): fish rejected the deployment script's first `b=…` with
  `Unsupported use of '='`, executing nothing else. Wrap sh scripts in `sh -c <script>` (`posixCommand` in `devices/deploy.go`), with outer quoting from `anyShellQuote`:
  fish still interprets `\\` and `\'` inside single quotes; csh still expands `!` history (`'a!b'` → `Event not found`). Keep quotes, backslashes, and `!` outside quotes, backslash-escaped, so sh / bash / zsh / fish / csh agree; no newlines (csh doesn't accept them).
  A command plus arguments (`personad node --stdio`, `true`) works in every shell without wrapping. `deploy_test.go` runs deployment, quoting, and node-start commands in each locally available shell (fish only tested if installed).
- macOS tar includes extended attributes when sending files to the hub (2026-11-03, #665 / #884): build artifacts carry `com.apple.provenance`,
  downloaded files also carry source URLs. Two mechanisms: `._<name>` AppleDouble files (GNU tar extracts as normal files) need `COPYFILE_DISABLE=1`;
  `LIBARCHIVE.xattr.*` in pax headers (GNU tar drops them and logs `Ignoring unknown extended header keyword`) need `--no-xattrs`. Use both.
- Building personad on a hub in China: `proxy.golang.org` may be entirely unreachable (TCP timeout; working OS repositories / Docker Hub don't prove it works).
  Before building, `go env -w GOPROXY=https://goproxy.cn,direct`. If Postgres image pulls fail, use `public.ecr.aws/docker/library/postgres:18-alpine` (`docs/server/server.md`).
- Cloud public IPs / EIPs usually have an outer firewall or NAT (measured on UCloud: all ports including 22 and 8080 unreachable publicly, though local ss showed listeners,
  and local curl to its public IP failed too—hairpin self-connect can't establish port openness). Prefer the user's VPN / private address for phone access, or open ports in the cloud console;
  test public reachability externally (e.g. check-host.net TCP checks), not only on the machine.
- App update page says "This hub can't update itself": inspect `can_update` in `GET /api/v1/hub/update`; `Build.SelfUpdates()` requires
  channel=release, valid build number (YYYYMMDDHHMM), and updateURL (`<release site>/dl`) compiled in.
  `deploy.sh` injects it with ldflags from build config `publish.site_url`; manual go build deployments often omit it. Missing `publish.site_url`
  means no `-X main.updateURL=...`; add config and rebuild with `deploy.sh`'s ldflags. Docker updates images, not the binary,
  so prompting to change the image is expected (`docs/operations/hub-update.md`).
- Closing an RPC peer doesn't end the hub connection: the SSH child may stay alive. Connection loops must wait on `peer.Done()` too, or "reconnected" machines stay online while every call returns `rpc: connection closed` (`TestReconnectGivesAWorkingConnection`).
- tailcat (`docs/server/server.md` ("tailcat hole punching"), 2026-11-01): empty `Server.AllowedClients` lets **everyone** connect; refuse to load a node key file without the hub public key.
  `Server.TailcatAddr()` embeds DERP-region servers in the address and, without `RegionID`, reselects by latency each start, changing addresses. Build addresses with `ConnInfo{RegionID}` and select the region during `init`.
  Ports outside `ServedTCPPorts` time out rather than return RST; dialing needs its own timeout. After node restart, an old hub tailcat client fails its first dial;
  discard it on connection failure (`Dialer.Reset`) and rebuild next time. tailcat connections (gVisor netstack) lack keepalive and read forever after peer disappearance; hub must ping periodically. On node exit, close hub connections, `DrainTCP`, then close the tailcat endpoint;
  reversed order prevents FIN delivery, so the hub waits for two ping timeouts (about 2 minutes), causing redeployment timeout (integration testing 2026-11-01).
  Receiving files over tailcat needs `Multistream(false)` on `gzip.Reader`, otherwise it waits for another stream and the connection's reply never sends.
- Self-hosted derper (`docs/server/server.md` ("Self-hosted relay"), 2026-11-02): `-certmode manual -hostname <IP>` without a certificate makes derper generate a self-signed one
  and log a map node (with `CertName`), convenient but valid only 1 year. tailscale `tlsdial` still checks `NotAfter` for pinned certificates,
  so expiry breaks relay access; generate a long-lived certificate yourself.
  derper doesn't serve DERP maps; tailcat HTTP GETs `--derp-map-url`, requiring a separate static server. Running as root without `-c` writes the key to `/var/lib/derper/derper.key`.
  Changing the map reselects the region on `init`, changing the address but not the key.
- Don't expect `http.Server.Shutdown` to finish WebSockets: it doesn't wait for hijacked connections, while `pgxpool.Close` waits unboundedly for all borrowed connections. During a NAS upgrade on 2026-10-31, old hub hung after SIGTERM until SIGKILL, causing about 46 seconds downtime (normally ~1 second). Now close listeners, send WS 1001, and drain with bounds; every step has a timeout and total shutdown has a budget (`shutdown_timeout_seconds`, default 8 seconds), see `docs/server/server.md` ("Shutdown"). External `docker stop -t` must exceed it.
- coder/websocket reads, writes, and pings close the entire connection as soon as their context ends (`context.AfterFunc`), without a close frame.
  Connections needing close codes (persona-link replacement 4001, revocation 4002) mustn't read/write with a context canceled first: the hub canceled link ctx then called Close,
  so an in-flight write cut the connection and the client got -1 instead of 4002 (`send`, `close` in `devices/link.go`; regression `TestLinkReplacedAndRevoked` in `hub/link_test.go`).
- coder/websocket `Conn.Close` is a handshake: waits for the write lock to send a close frame (up to 5 seconds), then for the peer's close frame (up to 5 seconds). A client no longer reading never replies, typically waiting the full 5 seconds, up to about 10. Don't synchronously Close under shared locks like `h.mu`: `sendJSON` did this under `notifyWS`'s lock for slow clients and stalled the whole hub. Use `go conn.Close(...)` (`client.queue`) or `CloseNow`; regression `TestWSNotifyDropsASlowClientWithoutHoldingTheHub`.

## persona-link on Linux (docs/operations/link.md ("Linux (headless)"), 2026-11-02)

- `errors.Join()` with no errors returns nil: persona-link route `race` returned "no winner, no error" when all candidates were canceled (service stopped during dialing),
  and the caller crashed on a nil link. Explicitly return `ctx.Err()` when all are canceled (`TestRaceCalledOffIsAnError`).
- Sessions entered through `su`, `sudo -u`, or `docker exec -u` lack `XDG_RUNTIME_DIR`, so `systemctl --user` reports `Failed to connect to bus`:
  the user instance may actually be running (linger enabled); setting `/run/user/<uid>` connects. If no instance exists (no linger and nobody logged in), setting it won't help;
  disabling linger stops a user instance with no sessions immediately.
- User instances have no `network-online.target`; `After=/Wants=network-online.target` in a user unit waits for nothing. The program must retry itself.
- With nonpersistent journals (`/run/log/journal`), ordinary users' `journalctl --user` can't see their services' logs: persona-link writes and rotates its own files,
  and `persona-link logs` reads them; journal is only a fallback.
- To check whether resident node exists, **don't** connect to `~/.persona/node-attach.sock`: each connection is a new attach and displaces the hub's active one. Use only hub reports.
- For a systemd container in Docker Desktop (Mac): `--privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock`,
  with `/lib/systemd/systemd` as PID 1. The image needs `libpam-systemd`, or `user@<uid>.service` won't start (log:
  `Trying to run as user instance, but $XDG_RUNTIME_DIR is not set`). A host service listening only on 127.0.0.1 is reachable through `host.docker.internal`,
  so test hubs needn't be exposed to the LAN.
- `docker pull --platform linux/amd64 <image>` replaces the local same-name tag with amd64; later `docker run` without `--platform` uses emulation.
  After testing, restore with `docker pull --platform linux/arm64 <image>`.

## persona-link passive mode and node service (docs/operations/link.md ("Passive mode", "Installing the node service"), 2026-11-02)

- Link only relays node streams; remote updates belong in node RPC (#1158, `docs/operations/node-update.md`). Each attach creates a new `Node`, so update exclusion and installation transactions must belong to the resident process; closing the current RPC doesn't restart resident node. Shut down the resident service only after the success response is actually written; a separate recovery process rolls back if the new version can't start. Old nodes don't know the update protocol and need one local upgrade first; version mismatch alone mustn't enable a deployment button.

- Linux `setsid` detaches the process group, not the systemd service cgroup: when node startup fails, the manager kills rollback processes in the same cgroup (#1158). Recovery must run in a separate transient user service and be ready before binary replacement. Complete its readiness handshake before acquiring the update-directory lock, or the parent waits for readiness holding the lock while recovery waits for that lock. Tests use fake `systemd-run`, never installing recovery services on the development machine.
- A missing identity and an unavailable identity need different fallback decisions (#2910). A Linux zombie keeps both its procfs start time and its `ps -o lstart=` result, so rejecting it in `osproc.Identify` alone still lets a watchdog check count it as alive. Preserve the difference between a live exact identity, a known exited process, and an unreadable identity; only the last needs a weaker reading. Check for known exit even for older markers without exact identities. Tests can hold a deterministic zombie with `waitid(WEXITED|WNOWAIT)` after SIGKILL, then reap it during cleanup.
- Detaching a child does not change who must reap it (#2978). `cmd.Start` then `cmd.Process.Release()` leaves a watchdog in its own session that, once it exits while the parent runs on (a canceled node commit, a failed hub exec), stays a zombie for the parent's life. Keep a background `cmd.Wait()` instead, as both update watchdogs do; an exec hands the child to the new image.
- On macOS, reopening an already-executed signed Mach-O staging file with `O_RDWR` may cause later self-check retries to receive `SIGKILL` (#1158). Use a read-only handle for post-validation `fsync`; don't request write access just to sync. Canceled commits return before self-check. Verify busy / cancellation retries five consecutive times, not just first execution.
- macOS UNIX socket path limits are short; test names plus default long `TMPDIR` may exceed them (#1158). Socket fixtures use a short separate `/tmp` root, or `TMPDIR=/tmp` in isolation; don't blame the updater or change production sockets.
  New tests easily repeat this: `t.TempDir()` includes the test name (and subtest name), so a socket a few levels below exceeds the limit (#2257, #2278). Linux's limit is 108 bytes and its `TMPDIR` is short; Linux-only testing misses this.
- **launchd labels don't follow HOME**: temporary HOME doesn't make `persona-link node install` safe; `launchctl bootout gui/<uid>/com.persona.node` still stops the real resident node on that Mac.
  Test Mac service installation only with fake commands (`env.command`), never actually on the developer machine; use a different label for real runs.
- Container `eth0` is `/sys/devices/virtual/net/eth0` in sysfs, so netaddr treats it as virtual. Excluding virtual interfaces leaves no pairing-link address in containers,
  making `install --listen` fail (same for NAS interfaces on virtual switches). Fall back to virtual interfaces if no other addresses exist, still excluding tunnels and bridges (`docker0`, `veth`, `utun`, `tailscale`…);
  no address means only no printed link, not failed service installation.
- Passive streams need close codes (4002 on hub change, 1001 on stop); like active mode, don't cancel `websocket.NetConn` context first. Use `context.Background()`,
  then `ws.Close(code, …)`, then close node's socket.
- `docker build` with `FROM <image id>` looks for a registry image named that id (`registry-1.docker.io … EOF`): first `docker tag <id> <name>`, then `FROM <name>`.
  Removing a tag added to a dangling image also deletes the image.

## A node service in a Docker container on the hub's own NAS (2026-11-07)

Setup: a plain Debian container on a bridge network of its own, no published ports, not privileged; a small supervisor script as the main
process (under `--init`) keeps `personad node --attach` and `persona-link run` up, both restarted when they exit; everything else lives in the
home volume (docs/operations/link.md "Without systemd").

- **Only the bridge's gateway reaches the hub.** The NAS's LAN address timed out from the container and its public name was refused:
  Synology's policy routing (`ip rule`: `from <LAN address> lookup <interface>-table`) sends the replies of a connection to the LAN address out
  through the router instead of back over the bridge, and the container had no IPv6 for the name's AAAA. The pairing link carries only the
  hub's addresses, so the redeem failed with both. Fix: rewrite the link's `url` host to the gateway (`172.x.0.1`) before `persona-link pair -`
  (the pin is the hub's key and does not check names) and `persona-link address add https://<gateway>:<TLS port>`: manual candidates survive
  the hub's endpoint updates and rank first. Pipe the link straight from `hub pair` into `pair -` so it never lands on a screen or in a log.
- **Restart measurements need over a minute between restarts.** Ten `docker restart`s about 6 s apart took 2.3, 4.5, 8.3, 16, 32 s to come back
  online, then hit a 2-minute timeout: Persona Link reconnected in about a second every time, but the manager's run loop counts a connection
  that ended within a minute of starting as a failed path (`pathEnded`: 2 s doubling to a minute) and does not try the path before then, even
  though a new link is up (`setLink` wakes the loop, `choosePath` still skips the path). With a container that stayed up 75 s or more before
  each restart, ten runs came back in min 2.2 / p50 2.4 / p90 2.5 / max 2.8 s (time from `docker restart` to the hub listing it online on a
  new link). A real flapping link (a laptop on a bad network) gets the same delays.
- **No systemd: remote node updates need the supervisor to say so** (2026-11-08). `nodeupdate.New` recognized only systemd (`INVOCATION_ID`
  plus `systemd-run`) and launchd (`XPC_SERVICE_NAME`), so the box showed `can_deploy` false ("The resident node is not managed by
  launchd/systemd…") and Redeploy skipped it. What an update needs from a supervisor is only a restart when the node exits: the switch execs
  the new program in place (same pid; the script's `wait -n` sees nothing), and the restart matters only after a crash or the watchdog's
  rollback. The script now runs `personad node --attach --supervised` (docs/operations/link.md "Without systemd"); the watchdog then runs
  detached in a session of its own, as on a Mac, so the script must restart only the pid it started. Don't fake `INVOCATION_ID` instead: the
  node would then call `systemd-run`, which the box doesn't have. A personad from before the flag ignores whatever follows `--attach`, so the
  script can pass it before the box runs a build that knows it; until then, upgrade by hand: put the hub's own node build (`~/.persona/bin/nodes/…-linux-amd64`,
  the same version) into the volume and kill the node process.
- **Some downloads only work over IPv6 from that network, and a default bridge has none**: the Claude Code installer's download host timed out
  over IPv4 from the NAS and its containers, while the npm registry worked (`npm install -g @anthropic-ai/claude-code` with
  `npm config set prefix ~/.local`). herdr's manifest (`herdr.dev/latest.json`) timed out repeatedly; the binary itself came from GitHub
  releases, checked against the manifest's sha256 fetched from another machine.
- **The node starts herdr's server by itself** when herdr is installed but not running (`herdr_autostart`, default on): no need to start it in
  the supervisor. Without systemd it runs in a session of its own: killing the node left it running (reparented to the container's init) and
  the restarted node used it again; a container stop ends it.

## Safe pulls in the Git panel (2026-11-03, #684)

- `git status` behind counts use only locally cached remote refs; "up to date" doesn't mean no new remote commits. Keep Pull available even when synced.
- `git pull --ff-only` must explicitly disable rebase and autostash, preventing `pull.rebase`, `branch.<name>.rebase`, `merge.autoStash` / `rebase.autoStash` from altering behavior. Combine clean-tree checks with Git's own protection; no reset, automatic stash, or merge.
- New network commands mustn't reuse the default 15-second read-only timeout: pull 60 seconds, full node operation 75, hub 90, client 100. Disable terminal credential prompts and bound pipe waits after cancellation. Test with local bare remotes and isolated Git config, not users' real repositories.
- Git's "no upstream" used nonexistent SF Symbol `cloud.slash`; registration in the design system let `everyNameIsASymbol` catch it. The correct slashed-cloud symbol is `icloud.slash`. Don't exempt icon registration or validity checks.
- Direct node RPC `handle` tests need even minimal fixtures to initialize `state: newState()`: dispatch reads session state before Git methods, so `home` alone panics first.
- An upstream deleted on the remote and pruned (a merged pull request's branch) still prints `# branch.upstream` in `git status --porcelain=v2 --branch`,
  but no `# branch.ab` line (2026-11-09, #1884). Ahead / behind left at their zero values then read as "up to date" and "nothing to push" while the new
  commits exist only locally: report the missing `branch.ab` as `gone`, and never treat "upstream set, 0 / 0" as in sync without checking it.

### Stash positions change outside the node (#1480)

A stash index is a reflog position, not an identity. Carry the full object ID from list to request and apply by
that ID. Checking it before an unguarded `git stash drop` leaves the same deletion race: terminal Git does not
share the node mutex. For files storage, own the common-directory stash reference lock while validating,
applying a pop and rewriting the reflog/reference, with packed-reference coordination for final deletion.
Refuse unsupported storage or reference hooks before applying content; Git before 2.45 echoes
`--show-ref-format` back instead of naming the backend, and only has the files backend. Preserve foreign locks on refusal,
keep conflicts retryable, preserve existing storage modes and honor repository sharing for new loose refs,
and require refresh/reselection after stale identities. Test the real JSON dispatcher
against disposable Git with an independent writer at the last read before removal.

A failed `git stash apply` is two different things (#1885). Local changes it would overwrite stop it before anything
changes; a merge conflict has already put the content in (unmerged files with markers, the stash's untracked files
back) and only keeps the entry. Tell them apart by git's `CONFLICT` lines plus `git diff --diff-filter=U`, never `-q`
(which drops those lines), and have the client read the status again after any refusal.

## Cloudflare Tunnel push relay (2026-11-02)

- `systemctl --user` reporting `active` means only cloudflared is running, not connected to the edge or loaded with remote ingress. Acceptance checks Tunnel connectivity,
  website health, and origin rejecting direct access; URLSession User-Agent must work through normal egress too.
- If UDP/7844 is restricted, use `--protocol http2` and confirm TCP/7844 egress. cloudflared retries other edge addresses after one timeout;
  don't declare the whole Tunnel failed from the first timeout log. Use `--token-file`, not `--token`; no debug logging.
- Python urllib's default User-Agent may get Cloudflare edge 1010 before reaching the Worker. Distinguish edge rejection,
  Worker 503, and origin `not through the proxy` 403. Don't disable site security rules for one test client; retest with the real App User-Agent.
- Proxy rollout changes only `proxy_secret` and the same-named Worker secret, preserving grant `relay.secret`. Origin health checks need proxy headers; website checks don't.
  Keep new credentials and existing grant-secret backups in the password manager; temporary files 0600, no secret values in arguments or logs.

## Unified model configuration

- Provider deletion must check dynamic extension purposes as well as the fixed category and Lark /agent IDs (#1839). Resolve the current extensions' effective bindings and use their display names in the existing localized refusal; an extension-only override is otherwise invisible to the guard. Build purpose IDs with `StorageKey()`, so a renamed extension still protects its saved provider. Regress deletion through HTTP, assert the provider and override remain unchanged on 409, and check deletion succeeds after switching or resetting the binding when no current purpose still uses it.

- A managed-provider read failure is not an empty list (#1365). Read the whole raw setting reliably before changing providers, deciding assignments or resolving deletion dependencies; preserve readable sealed records so users can explicitly replace or remove corrupt credentials. Keep the confirmed snapshot through nested model resolution under the configuration lock, and publish that same committed snapshot to nodes. Decrypt only where import matching or node publication needs credentials. Reconnects and profile/trust notifications must omit a replacement on read or credential errors, and a safe committed edit must log and defer unsafe publication; only a genuinely missing setting permits first creation. Regress with selective failures at individual reads while writes remain available, full sealed-record equality, zero writes or node pushes on rejection, and a healthy retry.

- A "provider" isn't necessarily a runnable launcher: direct API configurations can't be sent to nodes or listed for new sessions; filter purpose selections by API / launcher / plugin capabilities. UI or `ANTHROPIC_MODEL` changes alone don't guarantee the model is used; shell launchers may clear environment variables. Verify argument forwarding and pass the model to the actual executor.
- Model caches and process reuse must include an effective-config fingerprint: changing URL, credentials, or model under the same provider name must invalidate old translation results and warmed processes. Test both same-config hits and changed-config misses; a changed fake API endpoint isn't the same config.
- Exact node model metadata and `node_sessions` must save atomically; use Postgres `settings` key `node_model.<machine>.<session>` with SQL CTE, no local JSON state. Single-purpose edits use database revision-conditional updates; PATCH read-modify-write isn't concurrency-safe by itself.
- Model switching mustn't kill an assistant with running background tasks; apply next turn at a safe boundary and explicitly reject sends while busy. Ordinary task edits omitting model fields preserve existing selections, so time/toggle edits don't overwrite values just saved in unified model settings.

- Legacy assistant-launcher edits must update unified purpose config too, not only `assistant.launcher`: existing `model_config.assistant` overrides it, so the UI reports success but the next chat still uses the old value.
  Non-model edits such as "About Me" use `assistantConfigLegacy` as the base, avoiding copying effective model choices into legacy config that resurface when inheritance is restored; revision checks detect concurrent edits during approval.

## Upstream rate limits (2026-11-02)

- Gateways return `HTTP 429 account_quota_exhausted` for exhausted quota and `HTTP 503 account_concurrency_full` for full concurrency. Previously each caller treated these as ordinary failures: knowledge processing and Inbox sorting retried item by item, producing 429 requests and log lines in 15 minutes; Inbox items were marked sorted "by rules" and weren't reprocessed after quota recovery.
  Handle rate limits per connection in the shared client (`ai/limit.go`, `docs/assistant/model-config.md` ("Upstream rate limits")): while paused, return `ai.PausedError` without sending; callers seeing `ai.IsPaused` stop the round, don't log, and don't mark items processed. New model-calling loops must do the same.
- Thinking models (e.g. `claude-haiku-5-5` thinks by default) count thinking tokens toward `max_tokens` too (2026-11-03, #1000): a project profile of about 2000 Chinese characters needs 3.7–4.7K,
  so `kb.project`'s old 4000 cap often truncated into thinking plus half a JSON object, reported as "unterminated JSON object", retrying every 2 minutes.
  Inspect Messages `stop_reason`: if `ai.JSONMax` lacks a complete object and stopped on `max_tokens`, double the cap and retry once; if still truncated, return `ai.ErrTruncated`.
  Budget JSON tasks for both body and thinking.
- Model JSON strings may contain things JSON does not allow; once `json.Unmarshal` reports a syntax error, the entire result of that call is discarded:
  - escapes JSON lacks (2026-11-05, #2151): JavaScript / Python `\'` for single quotes, or a single backslash in Windows paths, reports ``invalid escape sequence `\'` in string``;
  - control characters typed in as they are (2026-11-06, #4872): a summary written in paragraphs carries raw line breaks and tabs instead of `\n` / `\t`, reporting `invalid character '\n' in string`.

  On initial syntax failure, `ai.JSONMax` repairs these inside strings (`\'` becomes a quote, other illegal `\x` becomes a literal backslash, control characters become `\n` `\r` `\t` `\b` `\f`,
  the rest `\u00XX`) and parses once more; line breaks outside strings are valid whitespace and stay as they are, and valid JSON never enters this path. New model-JSON consumers use `ai.JSONMax`, not their own `json.Unmarshal`.

## Time zones

- The hub runs in UTC; the user doesn't (2026-11-01: "fix all the time zones too"). Server times shown to people or AI, and day-based grouping, always use the user's zone (`Hub.userLocation()`, `kb.Store.Zone()` for knowledge),
  not `.Local()` / `time.Local` / `time.Now().Format`. `internal/tzguard` tests scan all non-test code; node (running on the user's computer) and "user zone unknown" fallbacks are allowlisted with reasons.
- Per-manager profile reads must preserve the built-in profile's identity (2026-11-06, #5828). Keep `assistantProfile()` tied to the built-in assistant for persistence and main-conversation checks; use `assistantProfileOf(id)` for the selected manager's time zone and skills machine. `userLocation()` has no request and follows the default manager. Fall back to the built-in profile only before the selected manager reports one, including after a hub restart; a reported blank field is not a missing profile. Test different manager profiles and default switches, not just one manager.
- Tests mustn't depend on the test machine's zone: construct UTC times and explicitly pass the display zone (e.g. schedule and prompt golden tests in `internal/assistant` use `Asia/Shanghai`; knowledge goldens use `goldenZone`);
  after time-related changes, run `go test ./...` on the hub with `TZ=UTC`, `TZ=Asia/Shanghai`, and `TZ=America/Los_Angeles`.
- Files storing only clock times (knowledge day files, timelines) need whole-file migration when changing zones and must record their zone; don't simply change the write zone:
  otherwise one file mixes old and new clock times, and `Since` parsing old rows in the new zone shifts them 8 hours and misses messages in compaction windows (`docs/assistant/knowledge.md` ("Time zones")).
- Send IANA zone names to other programs (the manager's `Manager.userTZName()`, `extmgr/usertz.go`): `time.Local.String()` is "Local", not a zone name.
- pgx reads `timestamptz` into `time.Local` by default (#3755): a non-UTC hub serializes database times with `+08:00`, violating protocol UTC. `AfterConnect` in `db.Open` registers a `ScanLocation: time.UTC` codec (`utcTimes`) per connection, so all reads are UTC; don't scatter `.UTC()` fixes.
- Hub Docker images (`docs/operations/docker.md`, 2026-11-01): lockfile `resolved` entries pointing at other npm registries make `npm ci` on other networks (GitHub Actions, NAS)
  stall over a minute then report npm's "Exit handler never called!", hiding the network cause. `privacy_check.py` rejects nonstandard lockfile registries
  (`docs/operations/testing.md` ("npm registry checks")); `Dockerfile` checks before installation too. Web builds inside the image use `--ignore-scripts` to skip `prebuild`'s
  privacy check (build context lacks `scripts/` and git); workflow runs that check against the full checkout before building.
- Container ssh can't identify uids absent from passwd (`No user exists for uid`); duplicate uid entries use the first home (alpine guest is 405,
  home `/dev/null`, making mounted `~/.ssh` useless). The entry script changes the `persona` user itself to PUID / PGID and removes colliding entries rather than using
  `docker run --user <uid>`. `docker compose exec` defaults to root; image `personad` wraps privilege dropping to `persona`, or newly generated tailcat keys belong to root and the hub can't read them.

## Knowledge base (docs/assistant/knowledge.md)

- An ascending GitHub search cursor cannot use `updated_at` alone when a page ends inside one second (#4461). Keep the
  completed thread identities at that boundary, resume an unfinished thread across restarts, and rescan a tied boundary
  instead of trusting a page number across reordered results. Checkpoint matching-generation scans across quota windows,
  verify every ID again immediately before moving on, and retry a changing search. More than 1,000 threads in one second,
  or repeatedly lacking enough quota for that final check, is an explicit retryable limit. Search has no atomic snapshot:
  a result indexed only after the final check cannot be guaranteed. Test the actual connector against synthetic HTTP,
  including insertions, reordering, cancellation, repeated limited quota windows and retries.

- A Messages ROWID can be associated with more than one chat (#4440). Deduplicate by canonical conversation and ROWID,
  before page and search limits, and count unread messages by that same identity. A numeric changes cursor can only
  advance after every selected ROWID's associations are read; select the boundary and expand it in one SQLite snapshot,
  bound the expansion, and fail without returning a cursor when it cannot fit. A previously advanced cursor cannot
  reconstruct associations already skipped by older code.
- Forever is a sentinel, not zero days (#3780, #3862). Preserve `minutes_days: 0` before applying the transcript-retention floor, calculating an expiry date or confirming an echoed setting. A trimmed recording's metadata proves it has retained minutes even if `minutes.json` cannot be read; a parse failure must not turn forever retention into deletion. Test repeated sweeps, unreadable trimmed minutes, switching back to finite retention and explicit user deletion.
- A meeting answer may be spoken in front of others (#3862). Until private meetings can be marked, gate earlier minutes, their linked projects and action-item states together before retrieval; a prompt warning cannot hide material already sent to the model. Keep opt-in retrieval tests as well as a default-off test that checks the model input and returned citations.

- Deleting a connector is broader than deleting an archive namespace (#1836): include its alternate chat sources, posts and items, plus indexes and project references. Record the connector at ingestion, separately from a chat's namespace, and stamp item ownership before the unchanged-item fast path. Never infer legacy ownership from an item ID, title or a shared archive alias; use explicit provenance or a known source chat, leaving ambiguous records intact. Deletion tests need another connector's records, shared aliases and unowned legacy data, not only a single source.
- A connector's knowledge-base switch must gate every ingestion path (#1835): conversations, items (documents, meetings and events), document-title lookups and program-initiated posts. Filtering the source list alone leaves direct calls open after a switch changes. Check both the feed and the main switch at the call boundary too; a shared extension process may keep running after its connector is disabled. `Registry.Feeds` does not include `Registry.Enabled`. Test both switches independently, with a connector that still answers calls, and assert that neither source reads nor post forwarding occur while off.
- An archive switch must fence the commit as well as the source read (#4500). A response can already have crossed the manager boundary when the user turns the source off. Admit each page under a source generation, reserve the write and cursor together, retire the generation on off, and wait for already reserved commits before reporting success. A failed write can leave a journal that publishes data during recovery: settle it before releasing the reservation. Treat a lost reservation acknowledgement and an unreadable reservation record as uncertain, preserve them across source-manager restart, and test off/on, failure, cancellation, multiple pages and both local and bus adapters. The assistant also needs its own durable begin/settle/end journal with the token, generation and exact source/chat pairs: a replacement must recover it before new reads, never clear unrelated reservations, and fail closed when it cannot be loaded (#4732). The guarantee needs one authoritative source manager and cannot cover legacy work that never used the protocol.
- An asynchronous dedupe answer must recheck a user's split, record its verdict and merge under the same project-mutation lock as unmerge. The verdict-file mutex prevents lost JSON updates but cannot stop an already queued merge from reversing a completed split. A verdict write failure must stop the merge.
- A checkpoint after append is a replay window (#1343): connector pages can repeat after a cursor failure, a later batch failure or a process interruption.
  Preserve stable message IDs scoped by exact source and chat, including IDs such as `body` reused across threads. Formatted-text matching loses distinct messages;
  in-memory sets and atomic cursors do not repair append/receipt/counter failures. Journal the intended file replacements before changing text, then recover them
  before another writer reads counters or changes the same files. Test failure before and after every replacement with a fresh Store, and expire identities with
  retained text and source deletion. Legacy text and missing IDs cannot supply identities they never recorded. Compression must compare pending counts too:
  new IDs can share a timestamp or minute, so advancing coverage from time alone can skip concurrent messages. Success and refusal must share the snapshot gate,
  and a new ID in an already settled minute must reopen it for processing. Acquire metadata before message files:
  the existing project commit holds metadata while its frozen input reads the timeline under the message mutex, so the opposite order deadlocks ingestion.
  Attachment filenames must hash the full raw ID, not sanitize or truncate it: punctuation, case and long suffixes can otherwise overwrite an earlier message's
  attachment even when receipts correctly keep both IDs. Put new files in a separate day subdirectory so hash-shaped legacy names stay intact, keep legacy
  links/files, and use separate anonymous filenames when no stable ID is supplied.
- An archive timestamp does not bind a message reference to a line (#2814). Keep a hash of the rendered speaker and body with the ingestion receipt, and require a unique hash-and-minute match before using its reference. Keep the original absolute time so changing the archive's zone does not invalidate receipts, and prune hashes with their IDs. Identical lines or receipts remain ambiguous; legacy text uses only conservative destination/time/text matching. Count every candidate before consuming matches: removing an already matched line must not make a competing record claim another user's identical text.
- Recording uploads need an accepted read boundary across all three files (#1488). A final metadata error can otherwise leave rejected segments and rendered text searchable under the previous revision. Save a durable undo snapshot before replacing content, keep it authoritative for readers until commit, and restore on writable reopen before exposing the store. Cover both search APIs, detail, segments, reconciliation and summarization, including failed restoration and metadata ownership edits. Exclude the undo journal at the shared search eligibility boundary: searching only natural transcript text misses plaintext UUID/schema keys around its encoded contents, including ghost hits for a rejected first upload. Test every replacement before and after writing, actual subprocess exits, first uploads, legacy files and retry; an in-memory rollback or metadata-only assertion misses the defect. Keep snapshot locks outside the metadata lock and release them before read-capable observer callbacks.
- Enforce retention at write entry points, not only daily cleanup: initial archive reads "latest 100 messages", which in quiet sessions can reach years back;
  after cleanup, 47 expired day files reappeared 6 seconds later (production 2026-11-02). All day-file writes (`Archive`, `Ingest`, `WriteHistory`) share
  `Engine.textCutoff()` (midnight of the earliest retained day); cleanup uses `Engine.Prune`, with the same retention days. New write paths use it too. `TestArchiveKeepsToTheTextRetention` checks "cleanup immediately after writing should delete 0 days".
- For slow search, measure traversal first (#2632): with 37,000 archived files, caching contents improved only 1.6×; most remaining cost was serial `filepath.WalkDir` (~70 ms,
  more than 8-way parallel `stat` on all files at ~30 ms). Directory mtime changes for entry additions/deletions/renames, not content appends; cache directory listings by directory mtime,
  file contents by mtime + size. Together, warm search improves ~360 ms → 85 ms (~4.2×, docs/assistant/knowledge.md). Skip "just changed" entries for mtime caches (another same-tick write with unchanged size is invisible).
  Tests age files/directories with `os.Chtimes`, or never exercise the cache path.
- Rewriting a day file (`Store.MergeMessages`, imported history) must keep what readers cannot parse: split on `\n` only, keep `\r`, hand edits, odd lines and a missing final
  newline as they are, and test it as a property (original lines an exact subsequence of the result, count included) plus a fuzz target. The fuzzer found within seconds that a
  sender name with a line break formatted into two physical lines no reader took for one message, so its duplicate was written again; `Line.Format` now folds line breaks in
  the sender. Day files have no message ids: duplicates are "same minute, sender and text", which is exactly the formatted line.
- History import (#1239): a source's page `limit` is a hint (one real source answers limit+10 around a position, limit+5 paging back), so go on only from the cursor or
  positions a page answers, and treat "the same cursor again" as stuck, or a chat is read forever. A paced answer is empty with `more` false too: read `pace` first, then
  `unreadable`, and only then take it as the start (`HistoryPage.AtStart`).
- Never retry a history read that timed out, and never cancel one in flight to stop: the source may have served it (and counted it against a limit that trips a circuit
  breaker after a few failures). The driver's call context is detached from the run's (`context.WithoutCancel` plus its own timeout); every stop switch is checked before the
  next call instead, and any timeout or unknown code stops everything until a person looks.
- A slowness baseline must be per method: an image's first chunk legitimately takes 1–2 s where a page takes 0.3 s, so measured against pages every image looks slow. Count
  the "three slow among the last twenty" window over all calls, though, or a burst of image calls hides slow pages.
- Positional windows from a search: windows grow when a read finds an anchor the search missed, so persist the anchors and the ranges covered, not the windows; and every read
  must add coverage (give the gap up when one adds nothing), or a deleted message at a window's edge is asked for forever. Count as covered only what was kept (the read
  inside the windows as they are then): a review probe showed positions read and dropped outside one window, then inside another a later anchor widened, missing with no
  omission line. Omission lines need the edge messages' times, so keep those too: after a restart nothing else knows them.
- "Fall back for a year the search found nothing in" must not include years before the conversation existed: with a 10-year retention every empty year streamed a whole
  chat below its windows. Learn when the conversation began (one page around position 0) and skip the years before it.
- Pacing state is safety state: a rest, a `retry_after` pause, the multiplier and the very-slow watch kept only in memory are gone after a redeploy, and the next call goes
  out after one ordinary interval. Persist them with the status and wait out what is left before the first call.
- A write that downloads images inside it must be able to abort as a whole: written with placeholders after a stop cut the downloads short, the page's cursor moves on and
  the images are lost for good (a later read only adds the same message again, with its image, as a duplicate line). `kb.ErrDownloadAborted` from the download aborts
  `WriteHistory`, and the page is read again after the resume.
- Every call to a fragile source goes through the same safety path, a tool's too: a list call made from a chat turn was cancelled with the turn and its timeout only made a
  note, so the next call of the tool asked again. Detach its context, and stop the import on a timeout as the driver does.
- `filepath.WalkDir` does not follow a link at its root: a backup of a data directory that is a link archived nothing, succeeded, and three runs later had pruned every real
  backup. Resolve the root first, and fail a backup that kept no file.
- Attachment days longer than `text_days` only keep files no line points to (the day file is pruned, and a message past the text retention is never written, so its image is
  never downloaded): `Engine.AttachmentDaysFor` caps them at `text_days`.

## Port forwarding (docs/operations/ports.md)

- Credential revocation must cover auxiliary WebSockets too (#1823): closing `/api/v1/ws` leaves live screens, forwarded ports and previews usable.
  Register them with `trackRelayed` before opening a stream, recheck revocation after registration, and defer unregistering on every exit.
  Derive the connection context from that tracked request and close the connection when it ends; a context rooted at `context.Background()` outlives revocation.
  Abort the mux stream explicitly too: a sender waiting for flow-control acknowledgements cannot observe a closed connection. Test both ends closing,
  multiple connections per credential, other credentials staying usable, and revocation between authentication and registration.
  Remote setup cancellation is a separate lifecycle problem: `stream.close` for an unknown id is ignored, so a node that completes `port.dial` or
  `preview.open` after the caller canceled can still open a backend stream. Closing the client's WebSocket prevents further access, but does not prove
  cleanup of that late backend; established-stream tests cannot establish that guarantee.
- RPC `OnEvent` runs in the read loop: blocking there (e.g. writing received bytes directly to a slow WebSocket) stalls every RPC on that machine.
  Queue bytes per stream and write from a separate goroutine; bound queues through windows (sender waits for ack), not dropped data.
- coder/websocket defaults to a 32 KB message read limit (`read limited at 32769 bytes`, peer gets `StatusMessageTooBig`). Forwarded connections need `SetReadLimit`,
  as do test WebSocket clients / servers, or larger HMR messages look like forwarding failures.
- A dev server's first request may compile for seconds (next dev): when probing "is it HTTP?", a connected but silent timeout doesn't mean "not HTTP". List it first, then ask.

## Admin UI (docs/operations/admin.md)

- **Don't trust source addresses twice**: Docker's userspace proxy (docker-proxy) replaces port-mapped connection sources with the bridge gateway (`172.x.0.1`, private):
  host-local connections, all IPv6 when Docker IPv6 is disabled, and all Docker Desktop / OrbStack connections, including public arrivals. "Private = LAN" then classifies
  public requests as LAN, letting someone set the admin password first on a newly deployed hub. Container gateways never count as LAN (`containerGateway`); Linux LAN clients through
  iptables DNAT retain real addresses and aren't affected. A same-host reverse proxy (nginx adds no forwarding headers by default) makes public requests look like `127.0.0.1`, so loopback
  first-time setup needs the one-time code from logs. Measured in Docker Desktop 2026-11-02: Mac port-mapped access is the gateway, same-bridge containers use their own addresses, `docker exec` is loopback.
- **Forwarding headers only tighten access**: never allowlist via `X-Forwarded-For`; when present, require every listed address to be in range. Headers saying only "proxy" (`Via`,
  `X-Forwarded-Proto`…) without identifying the original source count as out of range.
- **Out-of-range must look nonexistent**: 404 reveals "there's a special path here" (other `/api/` paths without tokens return 401, page paths return web `index.html`).
  Pass out-of-range requests unchanged to ordinary web / App handlers. Match `/admin` case-insensitively: on case-insensitive filesystems (macOS), `/ADMIN/index.html`
  could otherwise be read directly by the ordinary static server.
- **Don't edit hub.json through maps**: `map[string]any` reserialization alphabetizes keys; use an order-preserving object and change only selected keys. Atomic-write locks belong on the directory,
  not a lock file beside hub.json (`rename` replaces the file; locking its fd doesn't lock the next writer).
- **`sync.Once` lazy state**: tests changing fields before first read have them overwritten by subsequent `Once` initialization. Initialize once before modifying.
- **Private addresses in tests**: `privacy_check.py` rejects private IPv4 literals (except documentation ranges like 192.0.2.0/24). Construct numbers for private-address tests
  (`netip.AddrFrom4`, `ip4` in `ingress_test.go` / `admin_test.go`); don't write literals in comments either.
- **`curl … | sh` can ask questions**: stdin contains the script; read `/dev/tty` (`(: </dev/tty) 2>/dev/null` checks terminal availability, absent in CI / cron).
  Test interactive branches locally with `script -q /dev/null <command>` for a pty, feeding answers through stdin.

## Same-host forwarding makes public connections look like loopback (2026-11-04, #1708)

- **Socket addresses can mislead too**: public 443 → reverse tunnel → local nginx `stream` (`ssl_preread` SNI passthrough) → hub TLS port makes every connection appear as
  `127.0.0.1`. Admin `cidrs` becomes ineffective (loopback always allowed; public `/api/v1/admin/state` returns `can_setup: true`), master token works publicly,
  auth failures aren't counted, logs show loopback. TLS passthrough can't add forwarding headers; use PROXY protocol (`security.proxy_protocol`, `docs/operations/network.md` ("PROXY protocol")).
- **Reject trusted upstreams without headers**, not "use headers if present, otherwise old behavior": direct proxy bypasses and proxies forgetting headers would become loopback again.
  Conversely, never read headers from sources outside `from`; existing clients remain unchanged. v1 `UNKNOWN` / v2 `UNSPEC` permit the socket address,
  but that's loopback for same-host upstreams, so reject them; only v2 `LOCAL` (proxy's own connection) uses the socket address.
- **Headers must travel the whole chain**: nginx `proxy_protocol on;` without headers from the preceding tunnel writes the tunnel client's `127.0.0.1` as source, still appearing local to hub.
  Hub logs once when "loopback upstream claims loopback source"; make the preceding layer send headers too, using nginx `listen … proxy_protocol` + `set_real_ip_from`.
- **Don't read headers in Accept**: one silent connection stalls all connections. Read in the connection's goroutine on first Read / Write / RemoteAddr
  (`http.Server` calls `RemoteAddr()` first in each connection goroutine), with its own 5-second deadline. Preserve any earlier caller read deadline and restore it afterward, never overwrite or clear it permanently.
- **Don't enable it for loopback on plaintext ports**: `hub pair`, `hub admin`, and plugins connect over loopback plaintext without headers. If `from` includes loopback and `listeners` includes `http`, refuse startup.
- **Tests mustn't assume Accept returns their own connection**: other developer-machine processes scan loopback ports (a test once accepted an unfamiliar connection starting `GET`, causing intermittent failures).
  Identify the dialed connection by its socket peer address.

## Hub HTTP responses used to be uncompressed (2026-11-05, #2149)

- WebSockets were always compressed (`CompressionContextTakeover`), but ordinary HTTP responses weren't: one Lark chat's messages were 2.9 MB (70 KB gzipped), taking seconds to open over VPN on the phone versus 0.5–1 second locally.
  For slow endpoints, first locate the cost: compare time and bytes with a hub-local `curl`, then inspect `Content-Encoding`.
- An all-response wrapper (`gzipResponses`) must sit outside `logRequests`: `securityError` uses a type assertion to find request logging's `statusWriter` and mark authentication failure;
  wrapping inside makes every 401 log twice. Implement `Flush` for streaming, and `Unwrap` so `http.ResponseController` can reach underlying connections for deadlines and hijack.
  WebSocket upgrades and `longLived` paths bypass this wrapper.

## Linked hubs (docs/server/mesh.md, 2026-11-02)

- In v1 peer linking, machine ids sent to the App had to change **before serialization**: `Hub.broadcast` called `json.Marshal` first, then modified the message map without changing encoded bytes.
  In v2 (single primary), the primary's machine object already uses the App-visible id (member computers become kind `hub` machines on the primary), so this translation was removed entirely.
- All `httptest.NewTLSServer` servers use the same built-in certificate: test hubs share fingerprints, so pinning can't detect "wrong hub". Check hub_id in hello.
- When one resident node has two routes (two configs, or primary direct plus via-member), both initially connect before uid is known; node keeps one attach and the later displaces the earlier.
  In v2 only the primary connects and, after uid merging, uses one route; route → uid is persisted (`mesh.path_uids`) so restart doesn't displace again. "No flapping" tests wait for connection counts to stabilize,
  then assert no new connections over several seconds, not just one correct instant.
- A hub with peers restarting doesn't know before links return whether it was replaced as primary (the new term is elsewhere). Wait 10 seconds or for the first link (`meshSettling`) before connecting its computers,
  or a returning old primary briefly steals them.
- Link RPC events run in the read loop: role changes upon higher terms (disconnect Apps, stop its own computer connections) only close connections or enqueue work, never wait for node there.
- Opening a node stream through a member mustn't use the opening step's timeout context for ssh: the process lives with that context and dies when the step ends.
  Use a stream-lifetime context. After an HTTP handler hijacks a connection, request context isn't the stream lifetime either.
- Testing two local hub admin UIs (127.0.0.1, different ports): cookies aren't port-scoped, so `persona_admin` cookies overwrite each other. Headless browsers must log in and screenshot one at a time.
- Passive persona-link counts failures per source, banning for 15 minutes after 10/minute: ordinary hub retries with bad credentials plus 30-second probes ban the hub's own address.
  After 401, pause the whole route (dialing and probing) for 5 minutes.
- Simultaneous dialing may show the rejected side either 4011 or hello `rpc: connection closed` (peer completed hello before closing). Both mean only "link already exists";
  return to the loop's start and wait for it to break, not repeated error logs.
- Admin `/api/v1/admin/` writes require `Content-Type: application/json` (CSRF), including bodyless DELETE and `/api/v1/admin/cli/` commands.
  The sole exception is plugin upload `POST /api/v1/admin/plugins/upload` (`application/octet-stream`, package as request body, #1208); other writes unchanged, and new exceptions need the same CSRF header.
- Local linking tests use two temporary hubs (separate HOME, ports, schemas in the test DB: add `&search_path=<schema>` to `database_url`). Loopback isn't a candidate,
  so link codes have no addresses; set `public.advertise` to `https://localhost:<TLS port>` in hub.json.
- **Singleton state must be keyed by hub_id** (2026-11-03): `mesh_group` began as a single-row table; hub and db tests shared a database, and db tests writing "primary hub-a" made an unrelated later test hub think it was a member, returning `503 center_unreachable` to all clients. Key by hub_id instead; restored backups on other machines need the same. Shared-DB tests clean their own rows.
- **Loopback-only hubs mustn't advertise on the LAN**: mDNS A / AAAA contain all local interfaces; if TLS listens only on 127.0.0.1, every address peers hear is unreachable.
## Late asynchronous results (2026-11-03, #556)

- Scheduled runs record results only at completion (`recordRun`), asynchronously. A slow failure (assistant takes time to give up) may finish after a later run or a "missed" skip,
  previously overwriting current task status with the old result. Each run now carries the task's `LastRun` at dispatch (`ScheduleRun.of`); if a newer run exists, write history only, not status.
  The same applies elsewhere when async work writes "latest status": check you're still the latest before writing. Tests wait for `runDue` dispatches (`h.sched.inflight.Wait()`) before inspecting.

## Cards delayed 4–10 seconds (2026-11-03, #751)

- **`select` consumed the marker**: `pollAgents` uses `reflect.Select` to wait on a timer and each host's event channel (buffer 1), then calls `drained()` on each to find "who changed".
  The triggering channel's marker was already consumed by select, so one isolated event (Agent blocked) had no owner, no snapshot was taken, and only the 10-second fallback remained.
  #611 lost `kicked = true` when converting `case <-kick: kicked = true` to multi-host select, delaying every state change 0–10 seconds; users only felt "sync got slower".
  Record the selected channel (`chosen` mapped to its host); don't expect to read it again.
- **herdr 0.9 event names contain dots**: `pane.agent_status_changed`, as in subscriptions. The underscores in old code/docs (`pane_agent_status_changed`) belong to older herdr.
  Accept both when branching by event name and feed both in tests. If unsure, subscribe directly to the socket and inspect raw output (one-line `events.subscribe`, then one event per line).
- **Ask what a fixed "wait first" is waiting for**: the hub waited 1.5 s on every screen card for a hook card to arrive; sessions without hooks (user profiles) never produce one, wasting time on every card.
  Decide per session (hook events seen since hub startup, or launched by Persona).
- Measure the chain: start a real node in the hub package (same process, own HOME, `~/.claude` linked to the real config), connect with pipes like SSH, and timestamp every hop for real Claude
  in isolated herdr (`hub/cardlatency_live_test.go`). Reports all sharing the same fractional second (.57 s, .58 s) indicate per-second ticks, not events.

## Deploy website Worker only through publish.sh (2026-11-03, #940)

- The `site/` Worker also serves public personad (`/dl/`) and `install.sh`, absent from the repo: `publish.sh` adds them to a copy of `site/` before deployment.
  Direct `wrangler deploy` inside `site/` (done during domain change) replaces all static assets, immediately making `/dl/*` and `/install.sh` 404 and breaking hub update checks / installation.
  Recovery: find the last release with `wrangler deployments list`, then `wrangler rollback <version>` (custom domains aren't versioned, so unaffected), restored in about 3 minutes.
- For `site/`-only changes (domains, pages), use `SITE_ONLY=1 ./scripts/release/publish.sh`: reuse published production artifacts, no new release (#951).
- Renaming the Worker (`name` in `site/wrangler.jsonc`) deploys a new Worker without the old one's secrets: the push relay answered 503 "push relay not configured"
  for every hub without its own APNs key until the secrets were put on the new name (2026-11-07, #1263). Move the secrets first; `publish.sh` now fails a deploy
  after which a relay that was healthy no longer is (`docs/operations/site.md`).
  Large-file downloads on this machine often reset connections, so curl uses `--retry-all-errors` (default `--retry` doesn't retry "Connection reset").
- Decide what happens to the old domain: shipped Apps hardcode the push-relay address and deployed personad checks old-domain `/dl/` for updates.
  With only 301 redirects, POST (push relay) isn't resent unchanged and breaks; the user accepted this on 2026-11-05, redirecting the whole old domain with 301 (#2389, `docs/operations/site.md` ("Old domain")).

## Faster access from China (2026-11-04, #1134, `docs/operations/site.md`)

- "CDN node in Hong Kong" doesn't mean fast mainland access: Alibaba Cloud CDN "global excluding mainland China" Hong Kong nodes took 200–390ms to connect, versus 37–38ms for Function Compute / ECS in Hong Kong. Measure ping and HTTPS using Globalping mainland probes (all three carriers) before deciding.
- Corporate-network international bandwidth varies wildly: the same 15MB file from the same entry point takes 1 second at one moment, over 60 seconds at another. Don't conclude throughput from corporate-network tests alone.
- Cloudflare geographic load balancing defaults `location_strategy.mode` to `pop`, determining country from the Cloudflare datacenter receiving DNS queries; mainland queries reach overseas datacenters and never classify as China. Use `resolver_ip` (`prefer_ecs: always`).
- DNS-only load-balancer records routing back to Cloudflare itself produce `error 1000`; use Cloudflare for SaaS. For a domain already on Cloudflare (O2O), custom hostnames accept only CNAMEs pointing at the SaaS zone; TXT prevalidation doesn't work.
- Alibaba Cloud Function Compute custom domains must CNAME to it; apex domains can't bind because Cloudflare flattens apex CNAMEs into A records.
- `wrangler deploy` reconciles Worker custom domains with `routes` in `wrangler.jsonc`: API-added extras are removed, listed domains reclaimed.

## Role permissions (2026-11-04, #1128)

- **GET doesn't mean read** (#1128): the `answer` role originally allowed "GET is read", so one GET could execute commands through the launcher API and another read `hub.json` to obtain the master token. Use role allowlists: list endpoints actually called by that role's clients, deny by default. Check historical client versions (`git log -p`), not only current code; inspect request bodies too, since parameters may be unauthorized choices (`launcher` on sends, `text` on card answers). Inspect side effects: sending to a nonexistent session id starts a new Agent, so rejecting `POST /sessions` alone doesn't help.

## Telegram images live in other data centers (measured #1255, fixed #1272, 2026-11-04)

- **First fetch from another data center takes 4–5 seconds**, mostly connection setup: Telegram images (avatars, photos, files) live in their own data centers, often not the account's; group avatars frequently span several.
  Fetching over the account connection returns FILE_MIGRATE, then gotd connects to that DC, generates keys, imports authorization (3.7–4.1 seconds measured; export only 0.3), and retries. gotd persists only the main DC session; other DC keys remain in memory, so every restart (deploy) and reconnect repeats setup.
- **A gotd middleware gate counts everything inside a request as that request**: FILE_MIGRATE, connection setup, and auth import all occur inside one `upload.getFile`; a one-at-a-time gate makes every other request wait those 4 seconds, connecting avatar DCs serially.
  Gate requests to Telegram, not connection setup: connect outside the gate (`Client.DC` per DC, only export goes through middleware), then gate requests on those connections individually.
- **Fetch directly from each image's `dc_id`** (official clients do too): otherwise even after setup every image first gets rejected by the account DC, doubling requests. Unknown id or moved image falls back to the account connection and gotd's FILE_MIGRATE handling.
- **Measure first**: total time doesn't reveal the bottleneck. #1255 added time/DC-only logging in the connector (`trace.go`), then fetched uncached avatars one at a time in a fresh process, showing connection setup—not gate queues or downloads—was slow. Prefetch saves only one round trip and adds requests.

## File preview foundations (2026-11-04, #1292)

- **JPEG pixels are stored before EXIF orientation** (#1303): apply all eight primary-image orientations, including mirrors, before sizing a thumbnail; re-encoding alone drops the orientation metadata. Keep metadata scans bounded and separate from nested/thumbnail IFDs. A derived thumbnail's ETag also needs a processing version, but clients must reach that validator: revalidate persisted disk thumbnails once per launch even when a listing's size/time match, and give browser thumbnail URLs a new page-load identity. Test both upgrade orders with a seeded old image; a new client may see an old node first. Unupdated clients can retain their private caches until they refresh or upgrade.
- **Go `mime.TypeByExtension` varies by machine**: it reads `/etc/mime.types` and other system files; docx / xlsx / ipynb / heic may be known on one machine and empty on another, making MIME-selected viewers differ for the same file. Keep a fixed preview-type table in node (`node/filemime.go`); outside it, consult the system, then sniff initial bytes.
- **`qlmanage -t` hangs on locked Macs** (still running after 30 seconds measured), without errors. System thumbnail tools need timeouts, cooldown after a timeout, and one concurrent execution: a file list may request dozens, otherwise node fills with stuck processes waiting on every thumbnail.
- **Hub and web share an origin; isolate inline files**: standalone `Content-Disposition: inline` SVG / HTML scripts run as the web app and can read localStorage tokens. Inline only images, audio/video, PDF, with `Content-Security-Policy: sandbox`; PDF is exempt because Chrome won't display sandboxed PDFs.

## Deploying from a stale branch (2026-11-04, #1314)

- Symptom: 13 minutes after #1283 (move task dock "All N" first) merged and deployed, the user still saw the old dock.
- Cause: hub logs showed another deploy after #1283 with `commit before #1283 + -dirty`: someone ran `deploy.sh` from an unre-based branch / workspace,
  and hub deployment also replaced the web with that directory's `web/`. Not cache: index.html is `no-cache`, assets hashed, no Service Worker.
- Diagnose through each startup version (`git describe`) in `journalctl --user -u personad`, and production `web/index.html` mtime (tar preserves build time);
  a PR-specific string in the live bundle identifies the web version.
- `deploy.sh` now requires HEAD to contain the remote's default branch, otherwise refuses; deliberately deploying an old commit requires `PERSONA_DEPLOY_ANY_COMMIT=1`.
- Fake backends can't catch dependencies in connectors written in other languages (2026-11-04, #1487): an external Lark connector pinned `SQLAlchemy==2.0.30`, which crashes on import under Python 3.13; green `CheckProgram` tests miss it. Build a real package and run actual pip/install/import checks. Accountless parts (dependencies, imports, QR initialization, decoding one synthetic push frame) can all be tested this way.
- `./scripts/test/test-server.sh ./... | tail` hides failed packages: Go sorts package names, putting `cmd/` first; `tail` leaves only final `FAIL` (2026-11-04). Save output, grep `^FAIL`; zsh uses `$pipestatus`, not `$PIPESTATUS`. If the hub can't reach proxy.golang.org, new-dependency packages show `[setup failed]`, not test failure.

## Plugin packages built on Mac won't install (2026-11-04, #1590)

- Mac `tar -czf` without `COPYFILE_DISABLE=1` adds an AppleDouble `._<name>` beside every file and even the top directory (`._<plugin id>`);
  hub searches the root or sole top-level directory for the manifest, so two top-level entries produce "no persona-plugin.json in package". Linux `tar tzf` shows `._*` and `LIBARCHIVE.xattr` warnings.
- Hub unpacking now skips `._*`, `__MACOSX/`, `.DS_Store`; packaging scripts should still `export COPYFILE_DISABLE=1`, since old hubs still reject such packages.

## Pitfalls in seamless hub restarts (2026-11-04, #1574)

- **Resident node systemd units need the same PATH as hub**: local node formerly inherited hub unit `PATH=%h/.local/bin:…` as a child;
  separate `personad-node` used systemd's default PATH and missed herdr / claude in `~/.local/bin`. `node-launchd.sh` now writes the same PATH.
- **Signal only the resident node's main process**: `systemctl --user kill -s HUP personad-node` defaults to the whole cgroup, sending SIGHUP to assistant claude and background commands too.
  Use `--kill-who=main` (`node-launchd.sh restart-idle`): `--kill-whom=` is only systemd 252's new name, and systemd 241 / 245 reject it as an unknown option,
  so the signal never goes out; deploy.sh ignores that failure, and the resident node stays on its old binary. Every version reads `--kill-who=`.
- **During resident-node `exec`, `node --stdio` could take over the connection**: the socket file remains (`SetUnlinkOnClose(false)`) but refuses connections; old `Relay` interpreted this as no resident node,
  serving the connection itself and putting assistant chats back in hub's cgroup, killed on next restart. Now wait up to 8 s on refusal (`dialResident`).
- **Shutdown "offline" broadcasts are false**: hub canceled ctx on SIGTERM, then disconnected machines broadcast offline `machines` and ended questions, so clients saw an offline cascade before disconnect.
  Suppress these during `stopping`; new hub uses handed-over machine snapshots (warm start).
- **Old binaries can't start after socket activation**: hubs unaware of `LISTEN_FDS` call `net.Listen` while `personad.socket` owns the port, repeatedly restarting with `address already in use`.
  Before rollback, run `PERSONA_HUB_SOCKET=0 scripts/deploy/hub-systemd.sh`. A connection after `systemctl --user stop personad` starts it again; stop the socket too for full shutdown.

## Workspace directories and independent discovery (2026-11-04, #1646)

- **Don't derive workspaces only from Agents**: host spaces can lack terminals, and Agent exit doesn't delete a workspace. Enumerate spaces separately, supplement session and bound directories, and persist the merged Postgres snapshot. Report partial host failures and preserve old records from those sources, not a successful empty list.
- **The node owning the filesystem validates directory identity**: hub doesn't interpret remote paths with its own filepath/filesystem. Validate directories and resolve symlinks when binding; sessions supply the same physical directory through optional `workspace_cwd`, retaining original `cwd` for runtimes. Frontend slash/`..` cleanup can't recognize symlinks and misses Agents in the same directory. Cache validation if needed, but later file operations still check independently.
- **Machine aliases can merge after binding**: normalizing ids only when adding cards isn't enough. On read, project cards and snapshots to current routing identities, preserving original card ids, columns, and order; mutations and CAS still target original state, preventing silent merger of two existing alias cards. New bindings dedup by effective machine and canonical directory (including archived); choose the latest complete snapshot by effective machine rather than restoring deleted workspaces from stale aliases.
- **Project cached catalogs only when discovery needs them** (#2108): a growing `proto.Workspace` magnifies an unnecessary decode/qualify/encode pass even when discovery code is unchanged. An isolated warm GET with 8,200 sessions allocated about 201.8 KB against its unchanged 177,600-byte ceiling; selecting raw snapshots and qualifying only offline or failed-source fallbacks reduced it to about 175.6 KB. Qualify cached source defaults and machine-scoped IDs before failed-source filtering and duplicate checks, not just before response encoding, or the persisted catalog can retain duplicate alias rows. Preserve the latest-snapshot rule, Orca metadata, successful-source deletions, unavailable flags and restart behavior; a cold-only measurement misses the redundant warm-cache work.

## Block iCloud paths component by component (2026-11-04, #1669)

- **Literal checks don't stop links**: `stat` / `file` only checked whether the resolved string contained `/Library/Mobile Documents`; a `~/x` symlink into iCloud still reached `os.Stat`, hanging the batch on a locked Mac.
- **`filepath.EvalSymlinks` isn't enough**: it `Lstat`s component by component; after following a link into iCloud it stats the next component there, so checks after return are too late.
  Walk components ourselves (`realPath` in `node/pathguard.go`), checking `offLimits` before each `Lstat`; lowercase comparisons because macOS is case-insensitive by default.
- **One hung stat mustn't stall the batch**: network drives and sleeping disks hang too. `statPaths` runs 8 concurrently, waits at most 3 seconds overall, and treats nonresponses as `missing`.

## Upload names the file system cannot take

- **Name limits differ by file system**: Linux takes 255 bytes per name, APFS 255 characters, so a 100-character Chinese name (300 bytes) lands on a Mac and is refused on Linux. A folder upload keeps the file's own name, so `beginInto` probes with `Lstat` and, on `ENAMETOOLONG`, shortens with `splitName` before any byte is sent (and refuses at begin if the shortened name is still too long for that file system). Before, begin accepted the name and the node only refused after the whole body (up to 2 GB) had been received. Windows is unchanged: Go does not surface `ENAMETOOLONG` there.
- **Test with more than 255 characters**: a shorter multibyte name passes on a Mac and fails on Linux, so the test would mean different things locally and in CI.

## Upload cancellation owns a terminal lifecycle (#1348)

- Keep a busy upload in the registry: removing it during a chunk or finalization hides it from abort. Mark cancellation terminal under a per-upload lock, then let the active file owner close/remove the part after its I/O settles. Completion must never reinsert retired state, and disk I/O must stay outside the registry lock.
- Stop idle timers when work starts and bind each new idle period to a generation. A callback already running when its timer is stopped must not cancel fresh activity. A short write is a failed upload, even when the writer returns no error.
- Serialize cancellation with the final rename on that upload. Abort winning first prevents publication and removes any empty reservation; a successful rename commits the upload, so a later abort leaves it intact. Test chunk cancellation and end cancellation separately through real RPC with gated regular files. The hub returns after a canceled chunk and sends deferred abort; it does not normally send end afterward.

## ego cookies (2026-11-04, #1304)

- **`task.cdp` permits only Target / Browser**: cookies use page `page.cdp("Network.setCookies" / "getAllCookies" / "deleteCookies")`; `Storage.getCookies` reports "task.cdp only supports Target. and Browser. commands".
- **ego-browser error output may echo the script**: cookie values are in the script, so report only exit code on error, never stdout / stderr back to hub.
- **Use `byOwnDevice` (`extmgr/helpers.go`) for "the user themselves"**: hub.json pairing tokens have full role too, but CLI and assistant processes hold them; credential operations require tokens with device ids.
- **Go nil slices become `null` in JS** (2026-11-05, #1926): list/clear with no cookies to import marshaled a nil slice as `const set = null`,
  and first-line `set.length` threw, breaking lists on every Mac. Real testing covered only import, while unit tests replaced `runEgo`, so this was missed.
  Script lists now always become empty slices first, and scripts guard against null `getAllCookies` results and null cookie fields; tests execute actual generated scripts with node (`TestCookieScriptTakesNullsFromEgo`).
  The same on the wire (#1573): `proto.Question.Options` has no `omitempty`, so a Codex free-text question arrives as `"options": null`. iOS and Android decode it as empty;
  the web typed it as a required array and `q.options.map` took the whole page down. A client type for a Go slice field without `omitempty` is optional (`?: T[] | null`), so the compiler finds every use.
- **Don't show raw ego JS errors to users**: replace with "Can't read ego sign-ins on this Mac; tap Retry…"; original errors only in node logs (property names, no cookie values).

## New runtime packages must enter the hub image (2026-11-04, #1777)

`go generate` in `server/internal/runtimepkg` reads `integrations/<name>/`, but image build context includes only `.dockerignore`-allowed directories,
and build stages only those COPYed by `Dockerfile`. #1668 added `persona-paseo-runtime` to neither, so `v0.1.4` image failed at `go generate`;
PR smoke paths didn't include `integrations/**`, so no premerge run. Add runtime packages to both `.dockerignore` and `Dockerfile`; image workflow PR paths now include `integrations/**`.

Same for web tests (2026-11-05, #2666): web-stage `npm run build` runs `tsc -b` on `src/**/*.test.ts` too; #2399 `injected.test.ts` imports fixtures from repo-root
`spec/fixtures/injected/`, causing image `TS2307`. When web (or another build stage) references outside `web/`, allow it in `.dockerignore`, COPY to the same relative location in `Dockerfile`,
and add that directory to image workflow PR paths.

## Adding permission-digest fields mustn't change old plugin digests (2026-11-05, #1830)

- `plugin.Digest` is what users consented to (stored in `plugins.granted`, compared on install / upgrade). New manifest fields (here `node`) enter digests as `omitempty` pointers:
  plugins lacking the field must produce byte-identical old digests, or every installed plugin appears to request more permissions after hub upgrade. `TestDigestWithoutNodeIsAsBefore` recomputes using the old structure and compares.
- "More permissions" (`MorePermissions`) mustn't inspect only `Permissions` in `granted`: node permissions aren't there; compare old/new manifests (`NodeAdds`).
- Top-level secrets are absent from the consent digest too (#1829). Compare secret names through the same helper in `MorePermissions` and `AddedIn`: otherwise the marketplace candidate permits an automatic upgrade that its own preview rejects as asking for more. Cover required and optional hub-only secrets, unchanged names, and the persisted once-per-version consent notice without changing existing digests.

## State surviving node connections can't belong to `Node` (2026-11-05, #1830)

- `node.Node` is per connection (new on hub reconnect); `nodeupdate.Manager` is process-wide. Plugin programs on computers must survive hub reconnect (leases),
  so store them in a process-level table keyed by node directory (`pluginHosts` in `node/plugins.go`), sending events to the newest connection peer.
- Change event destinations only when a real peer exists: every `plugin.node.*` request accesses the table; peerless calls (tests, local commands) mustn't clear the destination and lose events.

## "Ready" is not "loaded": the spool replay waited 2 s for a state that takes up to 20 s, or never comes (2026-11-06, #5889)

- `Node.stateReady` used to close when `loadState` was done, also when `state.load` failed or timed out (20 s), and nothing tried the load again on that connection. The resident node's spool replay waited only 2 s for it and then replayed anyway, so after a manager restart with a slow or failing database, report-only hook events of sessions Persona started were reported as sessions it does not know.
- A signal that something finished is not a signal that it worked: `stateReady` now closes only when the state came, `stateMissed` when the first try failed. What depends on it is held, not run on an empty default, and the thing is tried again on the same connection; the callers waiting on the dependent (live hooks behind the replay's gate) keep their own bound, so a hold never becomes theirs.
- Holding creates its own problem: whatever waited behind the hold is older than what went ahead. A late replay must not undo newer news (a session's old Stop after its new prompt, a pane bound back to the session it had); the node drops what live hooks moved on (`replayAhead`). But not everything that goes ahead is news: a resumed hook is a kept event asking again, and counting it would drop its own kept copy. And a late load must redo what the empty one decided (ultracode observations read in against nothing were written over the stored ones).
- A retry that only happens on the next connection is no retry on a stable connection, and a retry needs "not now" told apart from "no": the manager answered every failure as an uncoded error, so a hub that was away used up a notification's tries. It now answers `hub_away` (`internal/nodeproto`), which waits without counting. Only when the call never left: `rpc.ErrDropped` means it went out and the hub may have pushed it, so the manager takes it for sent rather than risk a second push.

## Single-value channels can't have two waiters (2026-11-05, #2277)

- Extension host (`extension/host`) used `go func() { ch.exited <- cmd.Wait() }()` to put one value in a capacity-1 channel. `runOnce` consumed it in the main loop, then deferred `end()` waited forever:
  self-exited programs (crashes, listen failures) never restarted and `Stop()` never returned. HTTP-only programs always hit it; RPC programs depend on `select` order.
- For an event awaited in multiple places or repeatedly, close the channel to mean "happened", storing results separately (write before `close`, read after closure).
- Put potentially hanging test waits (`Stop()` etc.) in separate goroutines with timeouts: fail only that test, not the entire package at `go test` timeout (#2278 hung macOS for 10 minutes).

## Wait for test goroutines before replacing package variables (2026-11-05, #2959)

- `-race` always flagged devices and connector: tests only `defer cancel()` without waiting for `Machine.run` / `watchOne`, so the next test changed `reconnectGrace` / `watchBackoffFirst` while old goroutines read them (even already-finished reads race without synchronization). devices tests use `runMachine(t, m)` (Cleanup cancels, then waits with timeout for `run`); connector `Start` reads backoff parameters once and passes them to watchers.
- Waiting for `run` exposed another bug: a tailcat connection stuck writing in `configureNode` (peer not reading) outlived ctx, while `watchTailcat` exited without closing it; `run` never returned and hub `workers.Wait()` hung at shutdown. `serve` now closes tailcat on ctx end (`context.AfterFunc`). Cancel-without-wait tests hid the hang.
- A goroutine stopped before it starts still reads what it reads when it does start (2026-11-11, #3942). `TestPlanThenWatchStopsWhenTheCardAnswers` cancels the plan-then watch at once, and the goroutine read `planThenEvery` after `fastPlanThen`'s cleanup had put it back; `planThenAfterTerminal` now reads it before `go`. The same holds for fields: `setCatalog` prefetches a pushed entry in a goroutine that reads `n.peer`, so a test gives the node its hub before `setCatalog`, never after.


## Knowledge corrections and extension permissions (#1908)

- Checking locks only before model calls misses corrections made during calls. Re-read metadata at actual persistence; manual writes lock touched fields by default, pipelines retain current pinned/field locks, and model waits don't hold write locks. Summaries persist field locks before body writes, preventing background writes between the steps. Clear old objects before refilling persistent values: omitted empty JSON associations don't clear old slices.
- Source deletion must account for returning model calls. Old objects/summaries mustn't recreate entities whose metadata was deleted; discard stale project body if input associations changed, preventing forgotten sources from returning. Unlink existing reverse references too.
- Declared extension permissions aren't grants: RPC and events check Granted. Explicitly list public kb methods; don't expose internal kb.forget / kb.posts_* wholesale. Read registry grant/subscription snapshots under its own lock to avoid async-event/toggle races.
- A lock that doesn't say whose it is protects against the pipeline only (#6100): extension writes went through the same manual path as the user's and could overwrite user corrections. Attribute writes where the caller is known (the manager adds the extension id; never trust one in params), keep the holder next to the lock (`provenance`), and refuse rather than partially apply. Metadata fields the struct doesn't know are dropped on the next struct write: add new metadata to `Tagging` and to the background writes' preserved list together.
- Owner raw handles and others' HMAC identity hints serve different purposes: encrypt separately, track sources, clean on source deletion, and exclude from ordinary metadata, search, and profile snapshots. Opaque IDs are case-sensitive; corrupted auxiliary ciphertext fails only owner reads, not the whole knowledge base.

### Clearing tags (#2356)

- Array updates distinguish nil from non-nil empty slices: nil preserves, empty clears; length can't determine whether to update. Cover shared writes across six entity types and HTTP/RPC reads.
- Explicit clearing must survive metadata serialization: omitting empty fields makes existing clients fall back to old tags. Persist `[]` so HTTP/RPC and tools reading original metadata agree.

- Migrating hub.json values into database settings (2026-11-05, #1830 step 6, which Mac supplies iMessage): mark migration once; don't repeatedly migrate whenever the database value is empty:
  otherwise user disabling/clearing in the App is overwritten on next startup. Failed migration (unmatched machine name, database write failure) logs without marking and keeps reading hub.json, retrying next startup;
  migration failure mustn't disable functionality. Preserve old fields for one rollback version; old hubs only know them. Construct settings-reading connectors after extension registry load (`attachExtensions` before them in `startConnectors`),
  or they can read only hub.json.

## Reject corrupt signatures on first verification too (2026-11-05, #2214)

Official extension catalogs embed payload and signatures in one document. Reject malformed envelopes, duplicate fields and invalid UTF-8 before verification; verify the decoded payload string without trimming or regenerating JSON. No detached signature fetch or unsigned fallback is permitted for any official alias, including cached catalogs and manual re-registration. Nonofficial TOFU metadata is no longer a trust grant or an installation lock.

## Catalog mirrors must not carry credentials or partial download state (#1227)

- A mirror changes transport, never a source spec, package digest or signing key. Give it an independent bounded timeout, no market credential and a separate origin-bound bearer session. On every validation or transport failure, retry upstream with a fresh staging file, hash and watchdog; a partial mirror archive cannot contaminate the fallback.
- GHCR authorization is derived from a registered GitHub owner and marketplace-repository prefix, not catalog hints. Bind realm, service, pull scope and every redirect; both the initial market credential and issued bearer are secrets. A git clone token never authorizes a registry.
- Persist the complete signed envelope in JSONB and verify its payload again after restart. Saving a parsed index destroys signed whitespace; a stored signed flag is not verification. Quarantine unverifiable official caches and unrecognized pins while preserving retryable database errors and the deleted-market marker.
- Keep mirror settings in each extension manager's existing setting store. Opening settings must not initialize markets or start network reads. A settings screen on the hub is not evidence of cross-manager distribution.
- A rollback is a version change like any other: whatever is captured per installed version (here the verified market key and origin) must be snapshotted next to `previous_version` at upgrade and restored by the rollback, never copied from the row being left. Without a snapshot, drop the trust-bearing field rather than inherit it.
- Do not quarantine a signed market because the network failed. Tell "could not read the catalog" from "read a catalog that does not verify" with typed errors (`errors.Is`), not message text: only the second may mark it invalid; the first keeps the last verified catalog installable. A negative signature test must prove it received an HTTP 200 catalog, or a 404 passes for the wrong reason.
- The final official sandbox allowlist and compiled-in signing key must ship together. A fixture key proving envelope mechanics does not prove final-key verification, GHCR visibility or cutover completion.

## Daemon-launched user sessions must leave its cgroup (2026-11-04)

- Background: another tool's resident systemd user service on this development machine kept its herdr servers, shells, and Agents in its own cgroup,
  so `systemctl --user status` showed 27.9G memory and 1821 tasks. Restarting/upgrading the unit killed every terminal and Agent session;
  unit memory accounting, limits, and OOM included user workloads, and status output was unreadable.
- Rule: **user sessions outliving their initiating request, connection, or daemon belong in their own transient units** (Linux: `systemd-run --user --unit=…`, fallback `setsid` without user bus).
  Linux `setsid` detaches process groups, not cgroups; default `KillMode=control-group` (Persona hub/node units don't set `KillMode`) kills all same-cgroup processes on service stop/restart.
- How Persona currently meets this (code review and real-machine checks, 2026-11-04):
  - herdr server and all panes / Agents: `launchHerdrServer` (`server/internal/node/herdrstart_unix.go`) detects `systemd/private` under `XDG_RUNTIME_DIR` and available `systemd-run`,
    then launches `systemd-run --user --quiet --collect --unit=persona-herdr-server` into that unit's cgroup, unaffected by hub/node stop or update;
    environments without user systemd (containers etc.) fall back to `setsid`. Real `personad.service` cgroup contained only hub, local `node --stdio`, and `assistant`, no herdr, claude, codex.
  - node self-update rollback: `nodeupdate/watch.go` uses `systemd-run --user --unit=persona-node-update-<id>` ("persona-link passive mode and node service" above).
  - children remaining in node/hub units (assistant claude / codex, Paseo bridge, plugins, connectors) are tied to the initiating process through stdin pipes and should exit when their owner disappears,
    not outlive the daemon; each uses `Setpgid` and owner stops the whole group. node updates wait for idle and use `node-launchd.sh restart-idle` (HUP main process only), avoiding running-chat interruption.
- Future long-lived daemon-independent processes follow `launchHerdrServer`: transient units plus no-user-bus fallback, with generated command lines tested separately.
- Known, not fixed:
  - Hub self-update rollback watchdog (`startWatchdog` in `hub/selfupdate.go`) only uses `setsid`, remaining in the hub cgroup under systemd.
  - Without resident node (`personad-node`), hub's local `node --stdio` and assistant claude live in `personad.service` and stop on hub restart (`docs/operations/hub-restart.md` ("Chats not interrupted: hosting by the resident node")).
  - `herdrstart_test.go` doesn't cover the `systemd-run` command branch.
  - Whether macOS `setsid` protects herdr from launchd job-stop cleanup hasn't been checked on Mac; Windows node doesn't launch herdr, so no equivalent issue.
- When a field can mean either "package to send" or "built-in, no package", add a package-only accessor (2026-11-05, #1830 wrap-up, `Manifest.NodeProgram()`):
  adding `node: {command: "builtin"}` to cua / ego made dozens of `m.Node != nil` checks trigger package transfers, status rows, confirmations, and consent-digest changes (changing UI).
  Replace with `NodeProgram() != nil`; test that adding built-in node leaves digests, permission rows, and `node_machines` identical to absence. Package/status/consent code uses the accessor, not the field.

## Generated types for open objects must preserve extra JSON (2026-11-05, #2467)

- A schema with `additionalProperties: true` but generated types with only fixed fields silently loses custom extension points on decode/re-encode. Keep typed fields plus raw extra keys; write extras at the same object level, never allowing them to override or fill known fields.
- Go `encoding/json` matches struct fields case-insensitively: first separate known fields by exact original keys, or extra `UI` is treated as `ui`. Swift open objects encode/decode String-key dictionaries through single-value containers, avoiding caller snake_case strategies rewriting extra keys. Calling dictionary `init(from:)` / `encode(to:)` directly still applies key conversion; regressions actually failed, so don't substitute those for container `decode` / `encode`.
- Share fixtures across four languages with nested JSON, similar-case keys, `additionalProperties`, and `__proto__` / `constructor`, also running SDK manifest / initialize handshakes. Schema passthrough isn't hub authorization or installation validation.
- Stream contracts (#5956, 2026-11-06): an object `oneOf` for exclusive payload presence must still validate its sibling `type`, `properties` and closed-object rules. Go's flat unions omit required zero/empty fields unless generation explicitly preserves them; test empty state/operations and null/false/zero values. Frame schema validity does not prove owner authorization, version order, keyed topology or atomic reduction. Keep those later gates explicit, and do not advertise runtime support merely because generated client models exist.
- Stream serialization/validation review (#5956): Kotlin fixture round trips using `encodeDefaults=true` do not cover the production codec, which omits defaults; stream serializers must retain required empty/null values and native empty children without changing the legacy codec. Discriminator fast paths must validate the complete selected branch (including `$ref` overlays) and sibling constraints. Track references in typed JSON unions when filtering plugin-only definitions. Generate required-field Go tags before marshaling, rather than round-tripping through maps and serializing nested payloads twice. Preserve native required-empty fields with stream-only wrappers: valid empty code text/table rows/file entries/comments must not disappear, while legacy Component serialization stays unchanged. Swift stream variants must reject mistyped or null optional destinations instead of silently converting them into append operations; legacy models remain permissive. Validate raw stream numeric literals before float64 rounding: `9007199254740991.1` rounds into the safe range and fractional versions can appear integral. Keep original numeric text for bounds/integrality checks; bounded-precision big floats avoid allocating huge integers for hostile exponents.


## personad downloads exceeded Workers' 25 MiB static-file limit (2026-11-05, #2541)

#1002 embedded 3.1 MB of tiered-avatar PNGs in hub (already compressed, little gzip benefit), growing compressed personad from 22.4 MiB to 25.8 MiB.
`check_asset_sizes.py` blocked publish before upload; TestFlight uploaded normally, only personad wasn't released. All four platforms were already within one or two MiB of the limit;
image compression only buys time, so archives moved to R2 (Worker forwarding, two rotating slots), leaving small files in the site. Check release-package sizes before embedding large `server/` assets.
## Hub-created cards must identify their initiating session (2026-11-05, #2558)

App/web place cards in chats by `machine_id` + `session_id`; sessionless cards appear only in Inbox "Needs you", invisible in chat and not marking the chat as needing you. `Hub.approve` (connector writes, computer control, browser scripts) previously included only machine, prompting "assistant approval cards no longer render". `mcp.call` already carries `session_id`: whenever hub asks for a chat, pass `asker` (machine + session). Computer/browser cards belong to the assistant's machine, not the controlled machine. Only actions the user taps directly in connector chat pages omit session.

## Stop peer handling when the caller leaves (2026-11-05, #2597)

`internal/rpc` lacked cancellation: when `Peer.Call` ctx ended, only the local call returned; the peer continued. Interrupting an assistant turn stopped Claude on node and made the connector's send tool (`*_send`) report "Connection closed", but the hub approval card remained until its 15-minute expiry. Allowing it sent a stale message, and assistant retries produced a duplicate card. node `mcp.call` now sends `rpc.cancel`. To give another method this behavior, add it to `Peer.Cancels` after confirming stopping on ctx end is correct (don't add writes that must finish).

## Interaction-history writes need timeouts (2026-11-05, #2937)

Interaction queues now live in the manager (`server/internal/assistant`, moved from hub by #3246): write history before sending cards to App or answers to hooks (`fleet_ix.go`: `record` before `announce`, `recordEnd` before `p.done <- r` in `resolve`); tests rely on this order. These writes used `context.Background()` with no pool `statement_timeout`: when another session locked the interactions row (reproduced with `SELECT … FOR UPDATE` on Docker postgres:18), hook answers waited until unlock, forever if never released, occupying one of 8 pool connections. `record`, `recordEnd`, `outcome`, and request-context `writeEnded` in `fleet_routes.go` now use `historyTimeout` (5 seconds), logging failed writes only. `keepAlways` in `resolve` writes "Always Allow in Session" (`assistant_always`) before hook delivery too, bounded by `historyTimeout` each time. Tests: `assistant/history_timeout_test.go`. Every synchronous database write on a user-waiting path needs a timeout.

## Handler ctx ends after the reply; long-lived work needs connection ctx (2026-11-05, #3139)

#2645 added `rpc.Peer` cancellation, calling `cancel()` on each request ctx when its handler returns. `session.watch`'s transcript loop used `WithCancel(ctx)`, so watch was canceled immediately after replying: hub subscribed and node returned ok, but no later `session.items` arrived; pages saw only `sessions_delta` and needed refresh for content—main sessions and sub-Agents alike (users noticed sub-Agents first). Unit tests stayed green because they called `n.watch(...)` directly with `context.Background()`, bypassing `rpc.Peer`.
Work continuing after reply (watches, streams) uses `rpc.ConnContext(ctx)`. Test watches through a real `rpc.Peer` pair (`servedNode` in `node/subagent_watch_test.go`), not bypassing requests. Check other request-ctx goroutines similarly (terminal/full-interface streams already used `context.Background()`).

## A struct copied by value for a settings write still shares its maps (2026-11-05, #3344)

- `s := u.settings`, unlock, then `PutSetting(ctx, key, s)`: the struct is copied, but its maps and slices are still the live ones. While the encoder reads them, another goroutine writing the same map under the lock is a `-race` report, and at run time `fatal error: concurrent map read and map write` takes the hub down (`recover` cannot catch it). Take the snapshot to be written with `maps.Clone` / `slices.Clone` while holding the lock (`extensionUpgradeSettings.clone`, `saveSandboxes`, `saveMachineIDs`).
- Writing after unlocking also leaves the order of the writes open: an older snapshot can land after a newer one, and a restart reads the old value back. Give each key a lock that only guards its write, and take the snapshot after acquiring it, so the last write always carries the newest state.

## In-place exec loses systemd sockets (2026-11-05, blue-green switching)

Hub startup takes sockets from `LISTEN_FDS`, then clears the environment. `net.FileListener` duplicates fds with close-on-exec, and original fds are closed.
So self-update `syscall.Exec` leaves the new process without either fd or environment; it tries to `bind`, but `personad.socket` owns the port, causing `address already in use`.
Reproduced on Linux with a minimal program (same `listen.go`, simulating systemd → hub → exec).

Hub now runs behind an entry process that keeps the port; version changes no longer exec (`docs/operations/hub-restart.md` ("Blue-green switching")). Without an entry process, retain systemd fds marked close-on-exec,
then clear the flag and provide `LISTEN_FDS` / `LISTEN_PID` before exec (`execListenEnv`).
Don't get fd numbers with `os.File.Fd()`: it switches fds to blocking mode; duplicates share a file description, making the listener blocking too. Record fd numbers beforehand.

On Synology NAS, `/tmp` is noexec: put test binaries and fake `systemctl` in the home directory (`TMPDIR=$HOME/…`).
Windows `git archive` exports CRLF according to `core.autocrlf`, breaking Linux scripts and embedded prompts; use `git -c core.autocrlf=false archive`.

## Slow extension installs: download, not build or startup (2026-11-06, #3277)

Measured installing Lark, GitHub, and an internal code-hosting extension on an isolated test hub: disk writes, database, and startup together took under 1 second; nearly all time was preview downloads.
Production `catalog/preview` logs showed 400 responses after 1–5 minutes: GitHub market packages download from Releases (14–17 MB), the hub process lacked a proxy,
and market HTTP clients didn't read hub.json `github.proxy`. Direct GitHub downloads sometimes took 2–3 seconds, sometimes delivered only half and waited the full 5-minute timeout.
The same package through a proxy consistently took 1.6 seconds. Changes: market GitHub access honors `github.proxy`; stop after 30 seconds without new data (`download_stalled`), not 5 minutes;
cache packages by sha256 for retries/reinstalls; preview collection members concurrently. Two more pitfalls: canceling a stalled HTTP/2 request doesn't close its connection, so retries reuse the same stuck pooled connection; call `CloseIdleConnections` after a stall. Don't simply force HTTP/1.1: Go HTTP/1.1 through this machine's outbound proxy to GitHub returned EOF in testing (curl HTTP/1.1 worked). Diagnose request durations in hub logs first, then time each step on a test hub (`started_at` / `ended_at` in `extension_operation`).


## Secret-binding checks belong in registry operations; delete on uncertainty (2026-11-06, #3500)

Voice-extension keys bind to service addresses (delete on host change). The first implementation in `PUT …/-/config` called `SecretsSet` to check for a key, deleted if present, then `SetConfig`. Review found three holes:
`Registry.SecretsSet` swallows database errors and returns an empty map, so one read failure skips deletion and saves the new address with the old key (exactly what binding prevents); checking, deleting, and config writing were three separate registry operations,
allowing concurrent "set key" to pair a key with an unintended address; and only this endpoint enforced it, so other `SetConfig` callers (assistant settings edits) bypassed it. Move to registry hooks (`BoundSecrets` / `SecretNeeds`),
checking/deleting under the same operation lock in `SetConfig` / `SetSecret`. Address changes delete unconditionally (idempotent); `SecretsSet` only decides response wording.
For future safety-driven delete/reject decisions, don't rely on error-swallowing read APIs or split check-then-write in handlers.

## Heartbeat timeouts on slow links (2026-11-06, #3452)

At about 30 KB/s from hub to a computer, its link repeatedly disconnected with "no pong within 5s", failing every message. Ping doesn't jump the queue: it follows bytes already given to TCP,
and the peer must receive those before ping, then pong. One 1 MiB block (downloads, uploads, node update packages all use this size) takes over 30 seconds at 30 KB/s, so a 5-second timeout always wins, and the next block breaks again after reconnect.
Heartbeat timeout must exceed "the largest message on this connection delivered over the slowest link", otherwise it measures bandwidth, not liveness. `link.ping_seconds` / `ping_timeout_seconds` in hub.json
are now configurable (`docs/operations/config.md`); the hub informs Persona Link through the manager (`endpoints`, `X-Persona-Ping`), both bounded by `link.PingTimes`. Old Persona Link ignores it and still uses 5 seconds.
Disconnect reasons include the ping error ("no pong within …: …"), distinguishing no pong from ping never sent.

Two remaining limits aren't fixed by heartbeat settings:
- coder/websocket writes one message as one frame while holding the connection write lock; ping frames (and owed pong) wait only 5 seconds for that lock (`writeControl`, hardcoded). Normally a compressed message
  fits immediately in the kernel send buffer and releases the lock, leaving ping queued in TCP, so longer timeouts suffice. If a message exceeds send-buffer capacity and is still writing, ping/pong fails after 5 seconds and breaks the link/stream.
  The root fix is smaller frames (`Writer` segmentation), or ping only when idle.
- Hub Persona Link forwarding caps each write at 1 minute (`spliceWS`); below about 17 KB/s, 1 MiB blocks still time out. Use smaller blocks or speed-dependent limits.

## A new value for an existing RPC parameter may be silently ignored by an old node (2026-11-06, #4858)

Uploads already had a `dest` parameter (`terminal`, `health`), but older nodes never read it: given `dest=dir&dir=~/proj`, they still put the file in the system temp directory and returned a successful path.
The app saw a successful upload while the folder stayed empty, with no error anywhere. A node that knows `dest` but not `dir` answers `unknown upload destination`, which is easy to handle.
Now the node echoes `dest` in its begin result; without the echo the hub aborts the upload and answers 409 `node_outdated`.
Whenever an existing method gains a "write somewhere else" / "do it differently" parameter, work out what an old node does with it: if it would quietly do something else, have new nodes echo it and check the echo in the hub before going on.

## Where a file may be written isn't what may be renamed or deleted (2026-11-06, #315)

The write guard `writable()` answers "may a file go into this folder": the home itself says yes, and a path has its links followed first. Reused as is for rename / move / trash it would have let the phone
move the whole home to the trash, and a link `~/out → /elsewhere` would be judged (and changed) as its target. `file.rename / move / trash` check the entry differently (`node/fileedit.go` `entry`): the folder it is in
is resolved with links followed, the entry's own name is not (a link is renamed as the link), and the home itself is refused on its own.
Also, `os.Rename` replaces whatever has the new name without a word: a rename or move that must not overwrite uses `renameat2(RENAME_NOREPLACE)` on Linux and `renamex_np(RENAME_EXCL)` on macOS (`renameExcl`), not Lstat-then-rename.
And an Undo id must carry everything it needs (`trash_id` holds where the file went and came from): the hub and node get redeployed between the delete and the Undo.

## Docker: containers can't write `.env`; Postgres loopback doesn't verify passwords (2026-11-06, #4045)

- Compose reads `.env` on the host before containers start, including `${X:?}` failures. "Fill missing values on first startup" requires mounted storage (DB password in `data/docker/db-password`) read by image scripts, not rewriting `.env`.
- Postgres starts first (hub `depends_on`), so generate random passwords in its entrypoint: wrap compose `entrypoint` with `sh -c` (`$` becomes `$$`). Overriding `entrypoint` removes image `CMD` too; add `command: ["postgres"]`.
- Don't verify passwords with `psql -h 127.0.0.1` inside Postgres: official `pg_hba.conf` uses `trust` for local loopback, accepting wrong passwords. Connect to `postgres` from another container on the same network.
- Build database URLs with `postgres://persona:${PERSONA_DB_PASSWORD:-}@…`, not `${X:+…}`: old docker-compose v1 doesn't support `:+`.
- Generate random passwords only before a database exists: Postgres reads `POSTGRES_PASSWORD` only during data-directory initialization; if DB exists (restored only `postgres/`, deleted `data/`), generating a new one mismatches its stored password.

## Response bytes do not establish a successful read (#6063, #6158)

`io.ReadAll` can return valid JSON bytes together with a transport error. Check the error before decoding or publishing successful output, including raw logs and calls that discard the body. Keep existing HTTP-status classification, scrub diagnostics and close the body. A failed response after a write does not establish whether the remote action happened, so do not add an automatic retry.

The AI gate's `answered` records HTTP 200, including later read or decode failures. Preserve that meaning while returning the read error and no model output.

## Don't let `http.Server` read `websocket.NetConn` directly (2026-11-06, #3498)

After #3630 moved extensions into a manager, hub forwarded requests over a stream on which the manager ran `http.Server`; the stream was `websocket.NetConn`. Ordinary requests and SSE worked, but extension
WebSockets disconnected immediately after 101 (voice pages connect this way). On hijack (`ReverseProxy` protocol switching, `websocket.Accept`), `http.Server` uses an expired read deadline
to stop its background read. coder/websocket `NetConn` permanently cancels its read context on an in-progress read deadline, closing the whole connection; the error isn't `Timeout()`, and the server cancels request ctx too.
`http.Server` needs ordinary `net.Conn` deadline semantics: give it one end of `net.Pipe()` and copy bidirectionally between the other and `NetConn` (`splice` in `extmgr/serve.go`), regression
`TestRelayedExtensionWebSocket`. Hub client `http.Transport` sets no read deadline and doesn't have this problem; future `http.Server` over `NetConn` needs the same adapter.

Another trap after adding pipes: `NetConn` context was canceled when `Serve` returned (`defer cancel()`). `net.Pipe` is synchronous; the server's final write returns as soon as the copying side reads it,
while that side is still writing to `NetConn`. Server closes, `Serve` returns, context cancels, and the write is interrupted; coder/websocket closes the connection and hub receives truncated responses
(small bodies entirely gone, large bodies missing the final frame; ordinary manager requests too, not just extension HTTP). Cancel `NetConn` context only after copying finishes;
regression `TestRelayedAnswersArriveWhole` (concurrent small responses, slow-read large responses, SSE, byte-for-byte comparison). WebSocket-only tests, direct mux calls, or fake managers with a separately written splice don't catch it.

## Docker Hub references share one registry endpoint (#6167)

Explicit `docker.io` references use the same `registry-1.docker.io` API endpoint as implicit Docker Hub names. Apply the `library/` namespace to their single-component repositories only after rejecting empty names. Keep other explicitly named registry hosts unchanged, and test the actual tag-list request as well as parsed fields.

## Self-update node platforms can't depend on "current machines" (2026-11-05, #3186)

Hub self-update downloaded node binaries only for machines whose platforms were known at that moment. Later-added machines, or never-connected machines with empty os / arch during update, had platforms absent from downloads.
The release manifest is read only during self-update, with no later fallback: `nodeBinary` in `internal/devices` searches only local `~/.persona/bin/nodes/`, returning
"hub has no personad for `<os>/<arch>`" if absent; repeated Redeploy fails. If an old platform binary remains from the previous release, it sends that old version and mismatch persists.
Now download every platform in the release manifest (four, matching `deploy.sh` and images); the hub platform's node binary is the same as hub itself, so reuse that download.
For any download/build list derived from current runtime state, consider objects appearing only after update.

## A hub-only update left node builds that passed for the new version (2026-11-09, #1825)

With `auto_update_nodes` off (the default), the hub's self-update replaced only its own binary. `bin/nodes/personad-<os>-<arch>` stayed the previous
build, and the manager's package lookup takes an unlabelled personad there for a build of the hub's version, ahead of the release site: every
Redeploy installed the old node (or failed its self-check), and the mismatch could not be fixed from the apps. Now a confirmed hub-only update to
another version renames those unlabelled builds to `.prev`, so the lookup falls through to the release of the hub's version.
Whenever a file's meaning is "the build of whatever version the hub runs now", whatever changes the hub's version without changing that file
must relabel it or move it.

## Generic plans make "empty means no filter" lose indexes (2026-11-06, #4907)

- pgx caches prepared statements by default; after five executions Postgres may choose a generic plan that can't see parameter values.
  `WHERE ($1 = '' OR machine_id = $1)` can't simplify in a generic plan, so old `Interactions()` scanned every retained row through `interactions_created`.
  Split statements by supplied filters (`interactionsSQL`, etc.), regression `TestInteractionsHistoryIndexed`. First-five EXPLAINs look good, then slow down; tests use `SET LOCAL plan_cache_mode = force_generic_plan` inside a transaction followed by EXPLAIN EXECUTE to inspect generic plans directly.
- Machine-only or session-only queries lack an index ordered by `created_at`: busy targets are fastest through `interactions_created`, quiet ones through `interactions_session` then sort; a generic plan chooses only one.
  Use `pgx.QueryExecModeExec` (unnamed statements planned with values each time), regression `TestInteractionsNotCached`. Don't change pool-wide `plan_cache_mode`, which replans every statement.
- LEFT JOIN on composed keys (`s.key = 'node_model.' || n.machine_id || '.' || n.session_id`) may hash the entire `settings` table, which grows because `settings` retains one row per ever-started session.
  Use a per-row scalar subquery through `settings_pkey` (`nodeSessionsSQL`). Fetch whole jsonb and decode in Go; extracting individual fields in outer SELECT reruns the subquery per field.

## ServeMux fallback needs its own response locale

Unmatched API routes and method mismatches run outside matched handlers' request logging middleware. Passing the underlying writer to `apierr` therefore loses the locale even when `Accept-Language` is present. Wrap only the fallback writer with the request's resolved locale, preserving status, `Allow`, and the JSON error code; leave matched handlers and streams unchanged.

Translate only an exact standard HTTP status phrase at this boundary. Custom node/server diagnostics must not be guessed from a status code. A request without a locale retains the old English status phrase. Cover 404 and 405 through the real router, not only by calling the formatter directly.

## Record what a push reads before the action that triggers it (#4855)

The node recorded which transcript line to tie a send to only **after** the send returned, but Claude Code writes the line as soon as it takes the message and the watch reads every 250 ms, so the line was often pushed before the send returned.
That line is pushed once, so the push never carried `client_msg_id`; only a later page did, and the app's "sending" bubble waited for that page.
Any mark a push reads (an echo, an owner, a session id) is recorded before typing, starting or writing; undo it on failure instead of recording it on success.
## Flattened inbox errors need response-time translation

Typed localization errors lose their key when RPC transmits only Error(), and persisted inbox failures or reply receipts retain the language in which they were written. Keep the old canonical error spelling, but translate a response copy from narrowly allowlisted hub-owned catalogs. Preserve context, titles, model summaries, suggestions, opaque metadata and unknown diagnostics; do not use a general reverse-translation pass over user content. Decode opaque JSON numbers with UseNumber so a localized error cannot corrupt an unrelated large ID.

Error-valued message arguments and structured field errors also need the request locale. Localize only a direct typed error or a recognized canonical message; do not drop arbitrary wrapper context. Regression tests cover RPC-flattened strings, old receipts, changing request language, nested errors and untouched user/model text.

## A lookup that always misses can be the node's biggest cost (2026-11-06, #5336)

- The session list asks `rolloutPath` once a second for every registered Codex thread; for a thread with no rollout (an app-server thread that never wrote one) that was a glob over every day directory of the whole history, each time. On a hub with 194 rollouts it was 61% of the node's CPU. Profile the live node before guessing: `perf record -p <pid>`, then map the addresses with an unstripped build of the same commit (`go tool addr2line`; the deployed binary is built with `-s -w`, but `.text` is laid out the same).
- Caching a miss on directory mtimes needs care: a file created or moved into a directory changes its mtime, but the kernel stamps it with a coarse clock, so a second change within the same tick can leave it unchanged. Keep no miss whose directories changed in the last second (`TestRolloutPathMissRacy`).

## Localized RPC summaries must not imply successful or absent delivery

A timeout or disconnected stream may happen after input reached the node. Translate the error summary, but preserve every existing code, delivered and retryable field; never infer delivered=none just because the transport failed. Keep raw diagnostics in detail, preserve node-provided details/drafts, and leave unknown custom error codes as external data. HTTP replies, uncertain inbox replies and backlog/terminal errors must use the same locale-aware boundary rules.

Some apps still recognize a timeout by the summary's words, not its code: iOS `APIError.isTimeoutText` keeps it quiet and Android's herdr notice explains it when the text says "timed out" or "超时". Rewording these summaries (#5031 added what to do next) must keep those words (`TestTimeoutSummaryKeepsTheWordsTheAppsMatch`). The Japanese summary never had them, so those checks miss a Japanese timeout until they branch on `code`.

## A command that only waits on the node socket must not trap Ctrl-C (2026-11-06, #4548)

- `main` turns SIGINT / SIGTERM into a cancelled context for every subcommand. A subcommand that never reads that context while it blocks (here `personad launch` reading the node's answer) can then be stopped by neither Ctrl-C nor `kill`, only `kill -9`.
  `launch` calls the `stop` from `signal.NotifyContext` first: with no channel left for the signal, Go puts back the handling the process inherited at start (normally terminate; ignored stays ignored), then waits.
- Socket reads on the local node get a deadline (`launchNodeWait`, 10 s for `launch`): a node that accepts and never answers (restarting, stuck, a stale socket held by another process) otherwise blocks forever. Say which socket timed out and point at `personad node doctor`.
- Test a signal while the child is actually waiting: signal only after the fake node has read the request, not after a fixed sleep. A child inherits an ignored SIGINT (tests run in the background), so skip when `signal.Ignored` reports it (`cmd/personad/launch_test.go`).

## Deadline cancellation can still exit zero (#2841)

A shell may handle SIGTERM and exit 0. Keep the recorded cancellation when classifying its timeout so the command list stops, while ordinary success before cancellation remains successful. A public `secret.run` regression can block on a FIFO, observe ready/TERM markers and check that no next command runs, without sleeps or busy waiting.

## An empty Git file list is still authoritative (#6056)

A successful `git ls-files` can return no files when the requested directory contains only ignored output. Falling back to a filesystem walk on an empty result discards Git's ignore rules and lists that output. Use the result's availability, not its length, to choose the fallback; test an ignored directory that actually contains a file.

## Staging cleanup covers final promotion too (#6106)

A verified download or unpacked binary is still temporary until its final rename succeeds. Register removal of the staging path immediately after creating it, so copy, verification and promotion failures share cleanup. Remove only that temporary file; keep the existing destination and valid input archive intact.

## CSV numeric inference must precede lossy conversion (#1568)

Compare the original CSV decimal with the exact decimal written by the 15-significant-digit XLSX serializer before choosing a numeric cell. Require integers (often identifiers) to match exactly, but round decimals and exponents to 15 significant digits first: tools print a float's shortest round-trip form with 16-17 digits, and turning those into text breaks sums and charts over a computed column. Float-to-float comparisons can hide rounding and underflow; counting all digits also rejects safe trailing zeros. Normalize decimal digits and scale with bounded exponents instead of expanding powers of ten. Verify generated worksheet values and cell types, including signed identifiers, decimal/exponent boundaries, and ordinary numeric controls.

## Spreadsheet text coercion requires finite values (#6352)

`strconv.ParseFloat` accepts `NaN` and infinity without an error. Reject them when coercing formula text so existing `#VALUE!` and `IFERROR` behavior works instead of caching non-finite numbers. Verify generated workbook values, with numeric text, range references and lazy branches as controls.

## Decimal rounding scales can overflow before the result does (#1335)

`ROUND` can have a finite result even when its power of ten or scaled intermediate overflows. Bound places by the float64 range, split large positive scales for tiny inputs, and retain numbers whose rounding place is already below their precision. Only a genuinely overflowing rounded result becomes `#NUM!`. Test emitted XLSX caches, including subnormal values, large finite results, 15-digit ties and `IFERROR`.

## Database passwords need URI userinfo encoding (#6160)

Query escaping turns spaces into plus signs, but URI userinfo decoding keeps a plus literal. Build credentials with `url.UserPassword`, and check the generated database URL with the actual driver's parser. Comparing a provisioning result against the same URL helper cannot detect this password change.

## Reading an answer a reverse proxy passes on (#5278)

When a `httputil.ReverseProxy` has to read what it forwards (the sent-as-user ledger wants the id in a write's answer), copy the bytes as they go to the client, after each `Write`, and read the copy once the handler is done: buffering the answer first would delay a stream and change what the client gets. Which bytes the copy holds depends on the request: a client that sent `Accept-Encoding` itself gets the API's compressed answer verbatim (and the answer's `Content-Encoding` says so), while a client that sent none gets the plain body, because the transport asked for gzip on its own and unpacked it. Read `Content-Encoding` from the answer's headers when the copy starts, not from the request, and bound both the copy and what a gzip copy unpacks to. Test both clients, a chunked answer that is still being written, an answer over the limit and a body that cannot be read: the command must get the same bytes in every case, and an unreadable answer must still be reported.

## Notification success needs a selected recipient (#1910)

Calling both notification channels does not mean either selected a recipient: a saved device may have no usable APNs or relay route, and a connected client may not support `ws_notify`. Return selected and reachable counts from the same recipient walk, before and after its preference filters, so the API can distinguish `no_device` from `filtered` without a separate readiness check racing that selection. Preserve the earlier session-level `muted` result and count both channels: a WebSocket notification can still go out without any push credentials. These counts are not asynchronous APNs / relay acceptance or proof that a phone displayed anything.

## A push's nil "wants" means "asked for explicitly" (2026-11-06, per-manager push switches)

`Pusher.send*` and the WebSocket notify take `wants func(NotifyPrefs) bool`, and nil is not just "every device": `alertWanted` lets it past Phone Muted (`mute_while_desktop`) and the pusher sends it to an open desktop too (an assistant's reminder, an extension's push). Wrapping nil in a filter to add one more condition (a muted manager) silently turns those pushes into ordinary ones, and they stop reaching a phone in Phone Muted mode. Put an extra per-device rule where the devices are walked (`Pusher.sendFrom`, `notifyWSFrom` take the manager) instead of in `wants`.

## A LaunchAgent node is throttled, and a protected folder stalls Claude before any hook (macOS, measured 2026-11; re-check; #5944)

- A plist with no `ProcessType` doesn't get "Standard" scheduling: launchd gives the job the Background policy, low scheduling priority and throttled I/O (`launchctl print gui/<uid>/<label>` shows `spawn type = daemon (3)`). A resident node under it got 15.45 CPU seconds in 139 s (scheduling priority 4 instead of 31), and the Claude Code it started did not become ready in time. Nothing looks wrong in the node's own logs; only the timings are long. When a Mac's Agents start slowly or time out, look at the agent's spawn type before blaming the machine's load.
- `ProcessType=Interactive` gives `spawn type = interactive (4)`. It changes scheduling only and grants no TCC permission: a job that cannot read a protected folder still cannot (`docs/plans/plan-connectors.md` "Full Disk Access (TCC)"). The plists Persona wrote (`scripts/deploy/node-launchd.sh`, `nodePlist` in `cmd/persona-link/nodesvc.go`) did not set it; both write it now, and `personad node doctor` reports an agent without it.
- Editing the plist isn't enough. `kickstart -k` restarts the job from the definition launchd loaded, so the change needs `bootout` then `bootstrap`. Right after `bootout`, `bootstrap` can briefly fail with `5: Input/output error`, so retry it. Whether a running agent has the key is visible only in `launchctl print`'s spawn type, not in the file.
- The rename (#277) introduced a new node-agent label, but both installers leave an installed agent as it is. A Mac can have either supported plist, or both. Code that looks for the node's agent has to recognize the installed definitions. With both there, use the one launchd has loaded: the other is a file launchd is not running, and loading it starts a second node next to the running one.
- Never reload the agent from inside the node. The web page's Fix (`node.doctor.fix`) and a doctor running under the agent (`XPC_SERVICE_NAME` is the agent's label, either one) would be stopped by their own `bootout` before the `bootstrap`, leaving the node unloaded. Only `personad node doctor --fix` from a terminal does it. Test the real `launchctl` sequence with a throwaway label (`go test -tags launchdlive -run TestLiveLaunchAgentFix ./internal/node/`), never with the node's labels.
- `launchctl bootstrap` returns as soon as launchd accepts the job, not when the program is serving (a review measured 14 ms on a Mac, with a throwaway agent running the real node binary). Checked at once, the restarted node's `node.sock` was there but answered nothing (6 runs out of 6; a cold node publishes it about 0.9 s after start), so `--fix` reported the node it had just fixed as broken and exited 1. After a reload, wait (bounded) for what answered before to answer again, then check.
- `ProcessType` has nothing to do with privacy, but a different failure looks the same from the phone. When a skill directory, a command or agent file, or the working directory is (or links into) Desktop, Documents or Downloads, a Claude Code started by a program macOS has not allowed stops at "Loading commands and agents" and never reaches `SessionStart`. No hook fires, so the node sees a pane with no session and no error, and the phone shows a session that never starts. It looks exactly like a slow machine (#1396) or the throttling above, and a timeout message about the shell is misleading. The exact point it blocks at may move between versions: treat "stalls before `SessionStart`" as the symptom rather than any single message.
- This is TCC on the responsible program, the same rule as the file-access entry above; only the shape differs (a stall rather than `operation not permitted`). The responsible program is the terminal for `personad node doctor` run in Terminal (often granted Full Disk Access), `sshd-keygen-wrapper` over SSH, and the node binary under launchd. So a check is only meaningful from the node's own process: the command line asks the running node to do the reads (`{"kind":"preflight"}` on `node.sock`), and says so when it had to read from its own process. There are two ways out: grant Full Disk Access to the program that runs the node, or move the directory out of the protected folder. `explainDenied` (`node/files.go`) words file errors on the phone the same way, by what started the node.
- To find the path, read the same paths in a separate process with a timeout. Don't `stat` them first in the node or in a shell `[ -e ]`: macOS refuses stat inside a protected folder too, so a refused path passes for a missing one. Judge a link by where it points, not where it lives: a link under `~/code` can point into `~/Documents`. Resolve it with `readlink`: `realpath` stats the components inside the protected folder, and those stats are refused too. Kill the whole process group, and parse only what the child printed before the kill. Without that, the dying child reports the killed read as a failure with an empty message, and the hang isn't blamed on the right path.
- The first preflight ran `ls` / `head` per path from `sh`. On a Mac whose security software inspects every exec, 108 linked skills took 5.2 s, longer than the whole rest of the doctor. Re-running the same binary in a hidden child mode (`node doctor --preflight-child`) does all the reads in one process: 1.7 s for the whole doctor. It also gives the exact errno (EPERM = TCC, EACCES = file mode) instead of locale-dependent shell messages; dash says "No such file" where bash says "No such file or directory". Tests start the test binary in that mode from `TestMain`, never `node doctor` (which would run the tests again).
- A client's deadline must be longer than the server's worst case for the same request. The command line first waited 20 s for the node's preflight while the node allowed itself 30 s plus cleanup: a slow but working run was reported as "node not running" and read again from the wrong process (wrong TCC identity). Derive both from the same constants and test the inequality.

## Moving a node service to another program (2026-11-06, #5821)

- A service file's new `ExecStart` (or a plist's `ProgramArguments`) takes effect only when the service starts again: systemd after `daemon-reload` and a restart, launchd only after `bootout` and `bootstrap` (KeepAlive restarts a job from the definition it loaded, not from the file). So `persona-link node switch` writes the file, restarts, and only then knows whether the new program runs.
- Ask the node itself whether it came up, without touching the hub's connection: `ping` on `node.sock` answers `program` and `version` (a Node's and standby's alike). Dialing `node-attach.sock` instead would take the node from the hub.
- Keep the old service file beside the new one until the new node answers, and put it back on any failure or interruption; a backup still there later means a switch that did not finish, which `persona-link run` (at start and every 30 s) and the next `node install` or `node switch` undo (`node uninstall` drops it with the service). Drop the backup only once the service manager has taken the old service's restart: launchd can refuse a `bootstrap` for a while after a `bootout`, and a backup dropped then leaves no node and nothing to retry. The old program's file is never touched, so going back always works.

## A release's version is not a Docker image's (2026-11-06, node packages)

- The image built from the tag `v1.2.3` reports `1.2.3`; the release published from the same commit reports `v1.2.3-<build time>` (publish.sh appends it). A node build fetched from the release therefore never "is the hub's build" by its version string: whatever compares a node with what it should run must compare it with the build its platform resolves to (`devices.nodeTarget`), not the hub's version, or the computer is redeployed forever. publish.sh also keeps the release under `dl/X.Y.Z/` for the tag so the image finds it.
- A node build for another platform cannot run on the hub: check its Go build info (`debug/buildinfo`: `GOOS`, `GOARCH`, the main package for the kind) besides the manifest's hashes, and leave `version -v` to the manager's own platform and the node's `node-update-check`.

## A DELETE through a client that follows redirects can come back 200 (#5278)

Go's `http.Client` follows a 301, 302 or 303 as a GET for any method but GET and HEAD, so a `DELETE` to a path that has moved (GitHub answers 301 for a repository that was renamed or transferred) is sent on as a GET, which answers 200 with the resource and deletes nothing; only a 307 or 308 keeps the method (checked with a small server for each of the five). A helper that treats every non-error status as success then reports a delete that never happened. For a write whose success is a specific status (GitHub's delete of an issue comment answers 204, the only status its description lists), accept that status alone, and test it with a server that redirects the `DELETE` to a `GET` that answers 200. A 404 after a delete is not proof either way: GitHub answers it for a thing that is gone, for one the token can read but not delete (a public repository) and for one the token cannot see. Look at the thing itself (a GET of the comment: found, so it is there and the delete was refused) and only when that is a 404 too, at what only the second case would hide (the issue it was on, which a token that sees the repository can read, or which answers 410 when it was deleted); a lookup that fails leaves nothing known, which is a plain error, not an unknown outcome, as the 404 deleted nothing.

## A held pool connection hangs the test that fails (manager key rotation)

`pgxpool.Pool.Close` waits until every acquired connection is released. A test that holds one (an advisory lock on a connection of its own, `KeyRotationLock`) and then fails with `t.Fatal` runs its deferred `Close` before anything releases the connection, and hangs until the 10-minute test timeout, with no output. Release a held connection in a `defer` registered after the pool's `Close` (deferred calls run last-in first-out), and wait for the backends of a closed pool to leave `pg_stat_activity` by polling, not with a fixed sleep. Also, the hub and manager migration sets share a table (`sent_messages`, created in both before the split): "does this database have the hub's tables" must test the hub set minus the manager set (`db.hubOnlyTables`), or every manager database looks like the hub's.

## Notification choices must bind to complete, current options (#5113)

- Display text is not answer data: an alert may clip labels that a runtime would silently accept as a custom answer. Carry complete labels separately, submit indexes with a revision, and map them to the current card's labels before forwarding through resolve. Hash an unambiguous encoding such as JSON; NUL-separated labels collide structurally when a label contains NUL.
- When a structured question cannot fit, remove it whole and restart alert fitting from the original text. Otherwise the push loses its choices and still keeps the unnecessarily shortened alert from the failed fitting pass. Check direct and relay budgets separately, and verify the relay sees only ciphertext.
- A hub card snapshot can lag the manager's outcome. Resolve may return `200 already:true` for an earlier answer; a new notification choice must report `not_pending` rather than claim that its new selection took effect. A retry of the original operation instead replays its saved success receipt.
- A dropped bus connection can wrap the offline error even after a request was sent. Notification choices use the send-aware forwarding path: only a definitely offline manager releases the receipt; a lost response keeps an unknown receipt so reconnecting cannot execute the same tap again.
- Hub tests isolate `HOME` in `TestMain`. A subprocess such as the architecture guard's `go list -deps` can then lose the downloaded toolchain and module caches even though the parent `go test` compiled successfully. Before running tests with a downloaded `GOTOOLCHAIN`, export `GOPATH`, `GOMODCACHE` and `GOCACHE` from their original `go env` values; retain the isolated home rather than disabling it.

## GitHub's bounded thread window starts at the REST tail (#1590)

Ascending REST pages 1–5 retain the oldest 500 entries. Discover the last page from Link metadata, read enough tail pages to cover 500, then trim: a partial final page can require six physical pages. Keep pagination metadata beside the cached body on fresh and conditional reads. An unchanged first-page ETag does not establish an unchanged last page; prefer updated 304 Link metadata, and make one unconditional discovery read when a 304 omits it. A refreshed discovery establishes the earliest request admission order for its selected tail pages: their individual TTLs may otherwise reuse pre-append bodies. Keep completion timestamps for TTLs separately, publish immutable representations, and fence cache replacement by admission order so a late partial response or 304 cannot supersede a newer full page. Extract page numbers while keeping the original endpoint, and bound requests independently of the total history. Exercise the real Thread merge and every retained cursor with synthetic HTTP lists larger than the window.

## A source read newest first must not let an older entry replace a newer one

A connector that folds several notifications into one thread (GitHub CI failures per repository and branch) read them newest first and let each replace the thread's thing, so the oldest one read won. It went unseen while the in-memory `seen` set skipped notifications already read; after every restart the whole unread backlog was read again, a few per poll, and the open item's time walked back by hours while its count rose once per step (#1527). Keep the newest per key by comparing times, never by reading order, and have the consumer ignore a step older than what it holds: a restart, a retry or a second page can each hand it an earlier one. Test it with a backlog larger than one poll's budget, read after a restart.

## Hot paths: an alternation of sayings and a MarshalJSON that copies the struct (2026-11-09)

- Go's `regexp` runs an unanchored alternation by trying every branch at every position of the text. `transcript.AskOf`'s seventy sayings took about 0.3 µs a byte on a reply that asks nothing, and it runs on every session of every list a manager publishes: a node's `sessions.changed` for 300 sessions spent 37 of its 41 ms there. A pattern run often on texts it seldom matches is `lazyre.NewGated`: it first looks for the literals every match starts with (worked out from the pattern) and skips the engine when there are none. It needs every branch to start with a literal; a leading class, repeat or `(?i)` leaves it ungated, which `TestAskPatternsGated` catches for the ask patterns.
- A `MarshalJSON` that builds `struct{ T; Extra }{T(s), …}` copies the whole value, and the calling encoder then validates and copies the returned bytes again. For a large type encoded in bulk (a `proto.Session` is ~1.4 KB, a machine lists up to 150 on every push), embed the value by pointer and also implement `MarshalJSONTo` on the pointer, encoding with `json.DefaultOptionsV1()` so a caller's `SetEscapeHTML(false)` cannot change the bytes. Compare the bytes against the old encoding through a value, a pointer and a no-HTML encoder (`TestSessionWireBytesUnchanged`).

## Hot paths: a list copied to stamp one field, an UnmarshalJSON on a list's element (2026-11-09, #1704)

- The hub copied a machine's whole list (~200 KB for 150 sessions) on every push only to put the manager on each session. A list read without a copy has to be one that is never changed in place: `Machine.sessions` is replaced, and anything that changes it (`markUnknownLocked`, `sessions.revs`) copies it first. Put such a field on while encoding (`encodeStamped`), copying only a row that differs; a new writer that changes the kept list in place breaks every reader holding it.
- A type with `UnmarshalJSON` decoded in bulk makes the decoder find, validate and copy each element's bytes before the method decodes them again. Implement `UnmarshalJSONFrom` (it takes precedence) and decode from the list's decoder in one pass. Keep what `UnmarshalJSON` did on a bad value: decode into the value, so restore it on error, and compare both on many raw inputs (`TestSessionDecodeMatchesTwoPass`), repeated and case-folded keys and invalid types included.
- A benchmark of a lookup measures its fakes too: `BenchmarkReadinessMainLargeInventory` spent 11.8 MB a run in a fake `Session` that copied the whole inventory, which the real one does not. Fakes on a measured path find things the way production does.

## Hot paths: a replacement that matches nothing still copies, a whole list held before it is sent (2026-11-09, #1725)

- `regexp`'s `ReplaceAllString` and a `strings.Replacer` return a fresh copy of the text even when nothing in it matches. Run per line of every session's last words (`transcript.AskOf`), a list-mark regexp and a Markdown replacer copied each line two or three times for nothing. Cut a fixed prefix by hand (compare it with the regexp it replaces on edge cases, `TestCutListMarkMatchesRegexp`), check for the marks before running a replacer, and use `lazyre.NewGated` for a pattern most texts lack: its string replacements give such a text back as it is.
- An answer of thousands of rows built as one slice and one buffer (`GET /api/v1/sessions` for older clients, about 10 MB for 8,000 sessions) costs memory in proportion to the answer on every request. Encode it a row at a time into a small buffer and write that out as it fills (`writeSessionLists`); compare the bytes with the old whole encoding. Benchmark such a handler with a `ResponseWriter` that keeps nothing: an `httptest.ResponseRecorder` holds the whole body, and its growth hides what the handler itself allocates.
- A map keyed by a text keeps the text, and a substring keeps the whole string it was cut from. `transcript.Meta.notified` kept every task notification a node had shown, whole, to know a repeat, and `taskCalls` kept ids cut from them; a node keeps one `Meta` per transcript for its uptime, so two steady heap profiles of a real corpus had 5 and 12 MB of notification text in use. Key such a set by a digest (`noticeKey`, 128 bits of SHA-256) and `strings.Clone` the pieces that are kept. Check it with `runtime.ReadMemStats` around reading many large notifications (`TestTaskNotificationsAreNotKept`, #2864), and look at a heap profile's `inuse_space`, not `alloc_space`: the copies are cheap to make and costly to keep.

## Hot paths: a whole-input check before decoding, a list copied to read a few rows (2026-11-09, #1755)

- `json.Unmarshal` checks the whole input before it decodes it, only to report errors the way it always has; on transcript lines, whose message content is decoded again as a string and as blocks, that check was about a fifth of a cold read. Where only `err != nil` is read, decode with `jsonv2.JoinOptions(json.DefaultOptionsV1(), json.ReportErrorsWithLegacySemantics(false))` (`transcript.unmarshalLine`): the same values and the same failures (compare both on every fixture line, cut and wrong-typed ones included, `TestLineJSONMatchesUnmarshal`), but a value that fails is partly decoded, so the caller has to drop it.
- An accessor that clones a whole list costs the whole list for a caller that keeps a few rows (the assistant's view keeps about 14 of 8,200 sessions): give the store an accessor that copies out only what a predicate keeps (`Devices.SessionsWhere`). A lookup that returns its range value (`for _, s := range list { if … { return s } }`) copies every session it passes (a `proto.Session` is about 1.4 KB): `Machine.session` took 91 µs to find the last of 2,000, 6 µs ranging by index. A loop that only reads a field of the value is not copied, and by index it measured slower (`portAllowed`, 1.6 to 2.2 µs), so measure before changing one. Stamp a field only on the row returned (`Hub.decoratedSession`).

## Hot paths: a struct of mostly empty fields encoded by reflection, a lowered copy only to test a gate (2026-11-09, #1814)

- The reflect encoder walks every field of a struct even when most are empty: a `proto.Session` has about 110 fields, and encoding a busy machine's 150 sessions took about 0.67 ms on every list and push the hub sends. `proto.appendSession` writes the same bytes field by field and leaves nested values and times it does not write (outside the years 0-9999) to `encoding/json`. Prove it byte for byte against the reflect encoder for each field and each kind of value (empty, nil, escapes, `<>&`, U+2028, invalid UTF-8, zones), in each shape (pointer, value, list, interface) and each encoder that calls it (`TestSessionWireMatchesReflect`). Its `MarshalJSONTo` writes those bytes only to an encoder that keeps them as they are (`writesRawAsIs`): a json/v2 encoder without `PreserveRawStrings` unescapes them, `ReorderRawObjects` sorts their keys, and a caller's `WithMarshalers` would have been called on the fields.
- A hand-written encoder must not drop a field added later by a change merged without it. `appendSession` hashes Session's field names, types and tags (`sessionWireLayout`) and hands every session to the reflect encoder while they differ, so such a change only makes encoding slower; the golden test fails and prints the new hash. A field added to `proto.Session` needs its line in `session_wire.go` and that hash.
- A scratch buffer for such bytes is a `sync.Pool` of the encoder's own, not `jsontext.Encoder.AvailableBuffer`: `encoding/json` resets its pooled encoder for each value, and the available buffer shrinks with it. A nested value is written with `jsonv2.MarshalWrite` into a `bytes.Buffer` set on the output slice, so it is not marshalled and then copied.
- Lowering a text only to test a literal gate copies it every time (`transcript.AskOf`, three copies per list row): `lazyre.Regexp.MayMatchLowered` tests the gate on the text as `strings.ToLower` would have it, without copying an ASCII text, and the caller lowers only when it says true.
- A catalog of anchored templates tried in turn (`i18n.DeploymentText`, `InboxText`, `ComputerText`) runs every regexp on a text that matches none: keep each template's literal pieces and run its regexp only on a text that starts with the first, ends with the last and holds the others.

## Hot paths: re-reading every row of a list that barely changed, a catalog tried end to end (2026-11-09, #1986)

- A list decorated again on every publish re-read every row's question (`transcript.AskOf` on the last words of each of 8,200 sessions). Keep the answer per machine and session with the text it was read from, and read again only when the text differs (`asksMemo`); a complete list drops the rows it no longer carries, a subset keeps them. Prove it with a table of lists that change between calls, each decorated warm and fresh.
- To find out whether a list changed since it was last sent, compare its bytes as they are encoded (`changedWriter`), not after encoding them into a new slice: an unchanged list then costs no copy.
- An allocation bound read from `runtime.ReadMemStats` counts every goroutine in the process: in a full package run, work other tests left behind pushed the smallest bounds over. Run such a test in a process of its own (`isolated` in `assistant/profile_bench_test.go`, `isolatedSessionAllocation` in `hub/sessions_push_test.go`, #2050). Repeated garbage collection clears pools but does not stop unrelated goroutines; a synchronized allocation in another goroutine is still included in the measured callback's `TotalAlloc` delta. Rerun the whole test in the child, preserving its fixtures, functional assertions, collection cadence and allocation ceilings. Bound and reap the child, propagate its failures, and verify a child receipt rather than treating an early return as isolation. Check the source revision before attributing two failures to the same missing guard: a newer revision may already isolate one of them.
- A literal gate still checks every template of a catalog on a text that matches none (265 for `InboxText`): index the templates by the first byte of their first literal, and try the ones that start with a placeholder for every text (`i18n.templateIndex`).
- Background tasks, subagents and a subagent's latest tool are written by hand too, each type under its own layout hash (`backgroundWireLayout`, `subagentWireLayout`, `workflowToolWireLayout`). A field added to one needs its line in `session_wire.go` and that hash; until then that type goes back to reflection.
- The paged index rebuilt every row's item (a summary marshalled, the full row and search text joined) on each list update and `sessions.revs`, for one changed row: 2.65 MB for 150 rows. The index's own item is the memo: when its stored full row is the row's new encoding with the same suffix, its summary is the row's too, since the summary is cut from the fields that encoding holds (`indexedRow`, 0.10 MB). Text read from the row itself is still compared, because the encoding folds every invalid UTF-8 byte into the same escape. The keys must line up: a list with two rows under one id pairs one row's summary with the other's bytes, so after such a list, and for the list after it, compute every row. Prove it with the full computation run beside it on changing lists, step for step (`TestIndexRowReuseMatchesFullPath`), and let `Replace` keep a payload that is the stored slice itself without walking it by reflection (`sessionlist.sameItem`).
- With the index's rows reused, a `sessions.revs` reindex still copied the whole list to stamp the manager on it and encoded every row again: 7.26 MB and 10.5 ms for 2,000 rows, one changed (#2783). Take the rows from the push's kept encodings and read the scalars from the kept list itself; stamp the manager only where the index reads it, the summary of a row marshalled again (1.05 MB, 4.0 ms). A benchmark that bumps a rev in place on a list set without its bytes measures only the path that encodes everything: keep the list as the manager's are (`setSessionListRaws`, then a push) and hand over a copy per revs, as `onEvent` does.

## Hot paths: compressing the same file on every request, a privacy scan that cannot read gzip (2026-11-09, #2219)

- A response compressed as it is sent costs the same CPU for every client: the hub's gzip at level 6 over the web client's hashed assets was about 38 ms of CPU per cold first screen, 1.2 ms when the gzip bytes are stored. Files that never change under one name (Vite's `assets/`) are compressed once at build time (`webasset.Precompress`) and sent as stored; a request that must not get gzip (no `Accept-Encoding`, `HEAD`, `Range`, the admin paths) gets them decompressed, so its answer is the plain file's. Decompress such a file as it is read (`gunzipSeeker`: the size from the gzip trailer, a seek forward skips, a seek back starts again), not into memory first: a `HEAD` or a 304 then inflates nothing. A client without gzip pays the inflate (about 22 ms per first screen, against 0.9 ms for plain files); browsers always take gzip. Prove it against the plain site behind the same middleware for each kind of request, comparing status, headers and decoded bytes (`TestWebAppServesPrecompressedAssetsAsThePlainFiles`), and check that the stored bytes go out unchanged, not compressed again.
- A stored file's own name must not become a second way to fetch it: the built-in copy answers `assets/<name>.gz` with a 404, as the plain site did.
- `check_bundle_privacy.py` reads printable strings, and gzip turns a file's strings into noise: scan the plain web copy before precompressing it, or the scan of the binaries silently stops covering the web client.

## Fixed heap: a lazy value evaluated as an argument, a whole set built for one member (2026-11-09, #2475)

- A value made on first use is made anyway when it is passed as an argument to a call that may not need it: `writeIfMissing(p, prompts.RenderKBReadme())` parsed every prompt template on each manager start for a README that was already there. Pass the function (`writeIfMissing(p, prompts.RenderKBReadme)`) and call it after the check; a `heap?gc=1` profile of an idle process shows such frames.
- A catalog or template set built whole on first use holds every member once any is needed: the deployment, inbox and computer indexes compiled every template of theirs (624 regexps, 2.9 MB live for the three) the first time they translated a text, and one prompt render parsed all 139 templates (1.56 MB). Keep each member's source and build it on first use (`i18n.templatePattern`, `prompts.templateSet` parsing a template with the ones its tree includes), and keep a test that builds every member, so a broken one still fails there (`TestEveryTemplateCompiles`, `Names()`).
- A receive buffer sized for a worst case that cannot happen is kept for the life of a watch: the kernel never sends a netlink datagram over 32 KiB, so `netaddr` uses 64 KiB and retries a truncated dump with the old 1 MiB.

## Hot paths: a whole list decoded again for one changed row, one message projected per app (2026-11-09, #2250)

- A session list travels whole on every change, twice (node to manager `sessions.changed`, manager to hub `fleet.sessions`), and usually one of its 150 sessions changed. Decode only the elements whose bytes changed (`proto.SessionListDecoder`): a session decoded from the same bytes is the same session, so keep each decoded session by its bytes and hand out copies; drop what the last list did not name. The callers write `MachineID` and `Rev` into the list they get, so hand out struct copies, never the kept value.
- Splitting an event into the list and the rest is only safe where `json.Unmarshal` would read both parts the same way: it folds names case-insensitively (Unicode folding included, `ſessions` is `sessions`), lets a repeated key win last and merges a repeated array. `proto.ScanSessionList` takes the fast way only for one exact, unescaped `sessions` member and names given once, and leaves everything else to `json.Unmarshal`; compare both on every edge (`TestScanSessionListMatchesUnmarshal`).
- A row encoded from the same bytes, rev and manager is the same row: keep it with them and push the same slice again (`Machine.encodeKept`), so the delta and dedupe comparisons find it equal by its first byte. Tie the bytes to the exact kept list they describe (`rawsOf`, first element and length): any other writer of the list (a warm start, `markUnknownLocked`, a test's edit) then gets no memo and encodes everything. Prove it against a reference hub that decodes and encodes every list whole, byte for byte per app, and check the test fails when each key of the memo (bytes, rev, manager, an id listed twice) is dropped.
- A message queued to many apps through a per-app transform (`compat.ProjectJSON`, which scans for legacy names) costs the transform once per app: a third of a push with two older apps. Transform once per kind of app (`queueProjected`). Delta apps in sync hold equal maps of what they have; hand a connecting app the map another holds when they are equal, and work out each push once per map (`broadcastSessions`).
- An oracle test that compares output bytes cannot see whether a memo was used: the bytes are the same either way, so every test passed with the row memo or either hop's changed-row decoding turned off. Pin the reuse itself: the unchanged rows are pushed as the same slices, and each hop stays under an allocation ceiling far below a whole decode (`TestFleetSessionsOneChangedReusesRows`, `TestSessionsChangedOneChangedAllocations`). Bypass each memo once and check that a guard fails.

## Hot paths: a fleet copied for three items, a list read token by token, every language kept for one (2026-11-10, #2699)

- Live Activity's fleet copied each agent's session into its input and made an item (an id joined, a title and place clipped) for every agent, then kept three: 3.0 MB and 3,066 allocations per snapshot of 300 agents. Point at the decorated list (`fleetInput.Session`), key the maps by `[2]string` instead of a joined id, sort what would be shown and make items for the first `fleetMaxItems` only (0.50 MB, 56). Keep the old code as the oracle in the test file and compare both byte for byte on random fleets with ties, ids listed twice and cards on agents not listed (`TestFleetMatchesOracle`).
- A transform that leaves almost every row as it is still scans the whole message each time: `compat.ProjectJSON` ran over every whole list sent to an older app. Work out once per row whether it would change anything (`compat.Projects`, kept with the row memo as `asIs`) and project the message only when a row would. That holds only because the message's own members are never projected; check it with a row for each name and text the projection rewrites, under a machine name that looks projectable too (`TestWholeListProjectedByRows`).
- A general decoder pays for what a splitter does not need: `ScanSessionList` read the event token by token through `jsontext`, a quarter of a hop. A byte scanner with the same rules (exact unescaped `sessions` once, names given once, the same depth limit, invalid input left to `json.Unmarshal`) replaces it; keep `jsontext` as the oracle and fuzz both (`FuzzScanSessionList`, `TestScanSessionListDepth`).
- A buffer sized from the first row grows several times for a list of 150: size a whole-list encode from the average length of the rows last sent (`Machine.rowLen`).
- A catalog per language read as one keeps every language once any text is needed: the first `i18n.T` parsed and kept all three (1.2 MB live). Give each language its own `sync.OnceValue` (`i18n.catalogOf`): a process that speaks en keeps en, plus zh-Hans for a missing key and `Has` (0.38 to 0.76 MB). The reverse templates match every language, so they read the others without keeping them (`readCatalog`). Prove it in a process of its own, as other tests parse every language (`TestCatalogsParsedPerLanguage`).

## Long-lived buffers: a message read whole, a scanner that never shrinks, a link compressed to itself (2026-11-10, #2832)

- A reader that takes each message whole keeps the array it last read: `bus.Conn.Read` used `websocket.Conn.Read` (`io.ReadAll`, growing by doubling) and kept the rest in `c.buf`, `Write` copied every line into `c.wbuf` and kept that array too. One 21 MB request body relayed to the manager kept a 28 MB read array on each side until a larger one came, and in a churn soak these reads were 27% of the slot's allocation. Stream a message from `websocket.Conn.Reader` into the caller's buffer and send complete lines straight from the caller's bytes; keep a partial-line buffer only while it is small. Two edges a whole read handled on its own: past the read limit the library fails the read with its error and 1009 close and hands on nothing, so stop at the limit and look one byte further before handing on the last bytes (`atLimit`, compared with a whole read in `TestConnReadLimitIsTheLibrarys`); and a message that fails part-way has handed on its start, so end it with a byte no JSON has there, or the rpc reader takes a valid start (`{"event":"x"}` followed by spaces) for a whole line (`TestConnEndsMidMessage`).
- `bufio.Scanner` keeps the buffer its longest line grew for the life of the reader: 512 KiB per computer after one session list, 32 MiB after one large body. `rpc.lineReader` reads the same lines with the same errors and goes back to its first buffer once a long line is through. It also drops the line it handed out as the next `Scan` starts, as a Scanner does: kept while the reader waits for input, the line kept the grown buffer with it (32 MB in the manager until the next message, which a measurement 30 s after the body showed and one 60 s after hid). A reimplementation of a standard reader is only safe with the standard one as its oracle: fuzz both over random read sizes, buffer sizes, limits and errors (`FuzzLineReaderIsAScanner`).
- `json.Marshal` of a `json.RawMessage` checks, compacts and copies it, and the envelope's `Marshal` does it again; the line was then copied once more to add its newline. Put a non-empty raw value straight into the envelope (the same bytes and errors, `TestNotifyRawIsTheSameLine` with HTML characters, U+2028, whitespace and invalid JSON) and encode the envelope and its newline into one buffer. An allocation ceiling that reads `TotalAlloc` must keep the GC off for its loop: a GC empties the encoder's `sync.Pool` and adds a whole buffer to one run.
- permessage-deflate with context takeover costs each end about 0.85 MB of flate state while idle and, on the writing end, a frame buffer the size of the largest message; on loopback it saves nothing. The hub compresses only when the client offers it, so the hub's own manager (its child, or the built-in manager's service dialing the hub's local address) dials without it (`bus.Client.Uncompressed`) and every other client still offers it. A manager made in the app also runs with deploy `service`: decide by the identity and the address too, not by the deploy alone.
- Over a real network the takeover is still not worth it (#3506): Persona Link streams kept the same 0.85 MB per end to send a sixth fewer bytes. All four stream ends now ask for no context takeover (`link.StreamCompression`): a replay of 180 s of 10 computers' node traffic took 2.64 MB on the wire instead of 2.18 MB at the same CPU, and an idle pair keeps about 40 KiB instead of 1.7 MiB. Either end asking for no context takeover decides it (RFC 7692, and coder/websocket honours it as acceptor and as dialer), so an old end paired with a new one saves on both; check every old/new pairing, not only new with new (`TestStreamCompressionInterop`). That mode leaves messages under 512 B uncompressed, so measure the wire bytes of a link that carries mostly small messages (the remote bus, mesh peers) before switching it.
- A smaller first buffer sends the lines just past it through growth on every read (#3506): with `rpc.lineReader` at 16 KiB, a computer's session list of a few dozen rows (16-64 KiB) grew 16 -> 32 -> 64 KiB each time, and reading captured node traffic took 10% longer than with the 64 KiB start. Growing out of the first buffer straight to 64 KiB and not searching the bytes already searched for a newline (`seen`) read it as fast, and an idle connection keeps 18 KiB instead of 68 KiB. Measure the line lengths a reader really gets before choosing its sizes.

## A large upload on its way: read by doubling, encoded three times, decoded twice, kept for minutes (2026-11-12, #3199)

- One 22.4 MB request body relayed to the manager cost the slot 107 MiB of allocation and the manager's route 85 MiB to decode; the slot went from 45 to 180 MB resident and the manager from 56 to 192 MB, and an idle pair still held it all a minute later. `io.ReadAll` grows its buffers by half again and copies them into a final one (49 MiB): with the Content-Length known and within the limit, read into a buffer of exactly that size through the same `MaxBytesReader` (21 MiB), and give anything that differs (more bytes, fewer, an error) the results `io.ReadAll` gives, compared case by case (`TestReadRelayedIsReadAll`).
- An encoder given a struct with a `[]byte` field writes the base64 into a pooled buffer and copies it out; the envelope's encoder then checks and copies it again, and json keeps the grown buffer in its pool until the second collection (30 MB in the slot). When the large field is the struct's last and has nothing to escape, encode the rest without it, drop the closing brace and append the field straight into the line (`rpc.Appended`, `httpParams`: 58 to 28 MiB and a third of the time per call), sized up front so the frame budget is checked before anything is built. Pin the field order with a test of its own (`TestHTTPRequestBodyIsLast`).
- A line built by hand must come from the encoder the value would go through, and its oracle must be the code that writes the line today. The first version made the head with `json.Marshal` and one test compared with `json.Marshal` too; meanwhile #2907 had stopped `Call` from escaping `< > &`, so every query with an `&` would have gone out as other bytes, and only the test that compares with a real `Call` of the whole value saw it (`TestHTTPParamsAreJSONMarshals`). `rpc.Marshal` is that encoder; a copy of the old code as oracle proves what the code was, not what it is.
- In Go 1.27 `encoding/json`'s `Decoder` is jsontext underneath but hides a `*bytes.Buffer` from it, so a body already in memory is read again through growing buffers (85 MiB for 22.4 MB). Decoding the bytes where they are takes what `Decode` does by hand: `jsontext.NewDecoder(bytes.NewBuffer(b), json.DefaultOptionsV1()).ReadValue()` then `jsonv2.Unmarshal` with the same options (21 MiB: the decoded strings). Its errors are not always the Decoder's words, so on any error put the value back to zero and decode the same bytes the old way (`decodeLargeBody`); without `DefaultOptionsV1` field names stop matching case-insensitively, duplicate keys and invalid UTF-8 are refused, which the equivalence table catches (`TestDecodeLargeBodyIsDecodeBody`).
- After the request nothing allocates in an idle process, so no collection runs (`GODEBUG=gctrace=1` shows none in the minute after) and the pages stay. `debug.FreeOSMemory` a second after a body of 8 MiB or more returns them (`memtrim`); the second lets the request's own buffers become garbage first. A rate limit the first body claims leaves a second upload inside the window with nothing: schedule instead. Each body asks for a trim a second after it, trims run 30 s apart at the closest, and one asked for inside the window gets the next turn (`TestEveryLargeBodyGetsATrim`, on a clock moved by hand).
- Fewer bytes allocated is not a lower peak. The manager's route allocates 64 MiB less, and its peak stayed (192 to 188 MB): what sets it comes before the route, the bus line read by doubling (64 MiB for a 30 MB line) and the envelope's `json.RawMessage` copying the params once more (30 MB), and the last collection before the peak runs at the same heap size either way. Measure the process end to end (`scripts/diag/relay_body_rss.py`), beside the function's benchmark.
- RSS means different things per system. On Linux the runtime returns pages with `MADV_DONTNEED` and RSS falls at once. On macOS it marks them reusable: the physical footprint (Activity Monitor's Memory, `proc_pid_rusage`) falls at once, RSS only when the kernel takes the pages; and when the Mac is short of memory it compresses an idle process's pages, so RSS falls though nothing was given back (an unchanged build went from 192 to 70 MB by itself in one round of three). Compare builds by RSS and `VmHWM` on Linux and by the footprint on macOS.

## Hot paths: a node list copied three times, a list marshalled only to be compared (2026-11-10, #2826)

- A `proto.Session` is 1,520 bytes, so every copy of a node's list is a megabyte-scale allocation and slices grown by
  `append` from nil cost about twice that again. `sessionList` copied the runtimes' rows into a slice of their own,
  appended every row to a list grown from nil, then copied each kept row into a second one: 4.8 MB and 2.7 ms for 300
  rows, 63% of a node's allocation under churn. Size one slice from the last list, have the sources append into it
  (`runtimeRegistry.appendSessions`, `appendOffline`), and filter in place with a write index behind the read index
  (`out[j] = *s`, `clear` the tail). Run anything that reads the whole list, such as `resolveCodexHierarchy`, before
  the filter drops rows: a subagent finds its project through a parent the keep pass may drop. A presized slice is
  never nil, but a list that kept no row was: return nil then, or the wire says `[]` for `null`, and the manager takes
  `[]` from a `sessions.list` as its list emptied (`TestSessionListOfNoneSendsNull`).
- A stable sort of large structs moves each one many times (`sort.SliceStable` and `slices.SortStableFunc` both do
  insertion and rotation passes): 250 us of a 300-row list. Sort the positions with the same comparator, then follow
  each cycle of the permutation once (`sortNewestFirst`, 50 us); ties keep their order because the positions sort
  stably.
- Bytes kept to skip a resend need not be built through `json.Marshal(map[string]any{...})`: `sendSessions` built
  67 KB per 80 rows only to find them equal to the last list. Write them with `proto.AppendSession` into a buffer kept
  under the same mutex, swap it with `last` when the list goes, and fall back to `json.Marshal` of the whole value for
  any row the appender leaves to encoding/json, so the bytes and the errors stay its. Compare against `json.Marshal`
  for nil, empty, HTML characters, U+2028 and invalid UTF-8 (`TestSessionsChangedBytesAreJSONMarshal`).

## The push before the list: decoded from nil, copied, read three times, checked again (2026-11-10, #2950)

- `json.Unmarshal` into a nil slice grows it by doubling, so a decoded list of 1,520-byte rows costs about twice its
  size before anything copies it. Decode a runtime's push into a slice sized from that connection's last list and
  normalize it in place (`out := list[:0]`, then `clear` the tail). Give every push an array of its own rather than
  decoding into the last one: rows already handed out are read under their own lock, and json leaves fields of a
  reused element that the input does not mention as they were. A list that shrank keeps no array of the old size.
- Reading two side fields with two more `json.Unmarshal` calls parses the whole push twice more. Read them in one
  pass, but keep the separate reads as the fallback: a field of the wrong type fails a combined decode, while each
  single read only failed for its own field (`TestRuntimeRowExtrasReadsAsEachAlone`).
- Bytes known to be `json.Marshal`'s output still go through the envelope's checks, compaction and escaping as a
  `json.RawMessage`, copied twice into buffers that grow. Mark them (`rpc.Marshaled`) and write the line by hand only
  when nothing in it can need escaping: a plain ASCII event name, non-empty params. Compare the line with
  `json.Marshal`'s for HTML characters, U+2028, control characters and invalid UTF-8 (`TestNotifyMarshaledIsTheSameLine`).
  Give the type a `MarshalJSON`, or a `[]byte` passed anywhere else is written as base64.

## HTML characters escaped at the first hop stay escaped on every later one (2026-11-10, #2907)

- `json.Marshal` and a default `json.NewEncoder` write `<`, `>` and `&` as six bytes each, and an encoder keeps escapes
  it finds in a `json.RawMessage` even with `SetEscapeHTML(false)`. A tool input of HTML went from the transcript's clip
  through the node's rpc line to the hub's page and push: 61,011 bytes as Claude wrote them, 160,011 on the wire. Turning
  escaping off at one hop changes nothing while an earlier one still escapes, so the clip (`transcript.marshalPlain`,
  which `jsonLen` measures with too), every rpc line (`rpc.marshal`, `encodeLine`) and the hub's item bodies and pushes
  (`marshalItems`, `writeItemsJSON`) leave them as they are. U+2028 and U+2029 are still escaped, as encoding/json does.
- Prove it end to end, not per encoder: parse a line, send it over real rpc lines, read the page, the full item, the
  push and a subscribe's backlog, and check that `tool_input` is no longer than it was written and holds the same value
  (`TestItemsKeepHTMLCharactersOnTheWire`). A call's params carry no items and are pinned in the rpc package
  (`TestCallKeepsHTMLCharacters`). Putting back any one escaping hop fails one of the two.

## Transport source is not pane origin (#994)

A `runtime` session can be an Orca native desktop chat, a CLI in a terminal pane, a botmux topic or an unhosted runtime. Classify pane/host origins from the metadata, with desktop markers taking precedence, and keep transport filtering separate. New query keys need an independent advertised capability because an old hub may silently ignore them and return the wrong membership. Count the full matching index revision, and bind the origin to frozen cursors and delta membership even when the transport and timestamp never change.

When adding scalars beside an embedded session DTO, account for its custom JSON methods: Go promotes them and can silently hide sibling fields. Preserve the compatibility encoding and explicitly serialize the additional list fields.

## Catalog recipes carry opaque tool argument keys (#2037)

A catalog's recipe contains the upstream tool's argument object. Protocol field names are snake_case, but argument and input-schema keys belong to that tool and may be camelCase. Keep both opaque to the recursive wire-key check, while still validating their object shape and the surrounding recipe. Check original JSON before decoding optional limits or generated recipe types; otherwise mistyped fields and explicit zero values can disappear before semantic validation.

## Bound read-state snapshots by recorded entries (#2400)

When decorating a large session list, reserve the timestamp snapshot by the smaller of the list and the recorded read-state table, under the same lock used to copy it. Reserving for every row wastes memory when few or no sessions have been read. The unchanged isolated publication budget can expose this cost even after workspace discovery allocations are fixed; identify the failing subcase rather than treating all session-reader failures as the same regression. Keep the original budgets and seen-state semantics.

## Publication follows the explicit node policy (#1175)

A Hub-only receipt requires unchanged node versions. When the user explicitly enables automatic node updates, that condition would reject legitimate guarded transfers. Require matching verified packages and unchanged machine IDs, owners and UIDs instead; report each node's observed deployment state without treating Hub confirmation as fleet completion. Never send deployment requests from the publisher. Prefer the effective policy returned by the running Hub over its bootstrap file, and retain the strict unchanged-inventory check when the policy is off.

After activating matching native and downloadable packages, reapply an enabled runtime node policy through its existing settings route so managers recheck the new pointer. A filesystem pointer change has no consumer watcher. Check the current policy first so an intervening disable is respected; a failed recheck must not produce a completed publication receipt.



## Manager publication needs a real inventory producer (#3353)

A display session list cannot stand in for the local node's authoritative update inventory.
Read persisted identity separately from activation-plan validation, so the first idle read
does not require inventing the digest it is meant to produce. Keep the producer's completion
time when binding evidence to activation: later transport, REST and validation work must not
renew its lifetime. Test embedded publisher modules in an isolated interpreter as well as
ordinary imports; a transitive import can be available locally and missing remotely.


## Release status reporting is opt-in (#2671)

A source validation result and a deployment result are different facts. Keep release
logs and receipts regardless of whether GitHub accepts commit statuses. Default both
release workers to no status writes; explicit opt-in retains all three transitions,
including errors. Never fabricate a success to remove an old failing status.
## Idle wakeups: a ticker per watch, a P per core, a fallback that never waits (2026-11-09, #2590)

- A goroutine with a ticker of its own per watched session looks free, but every tick wakes the scheduler, and each tick opened, stat'ed and closed the transcript whether or not it grew. Two tickers per watch (250 ms transcript, 1 s status line) made 4 watched sessions at rest cost +260 ms of CPU and +9,150 context switches a minute over none. One ticker for every watch (`watchPump`), a stat before the open (`transcript.File.Changed`, and the same gate at the start of `refresh`), and goroutines only for work that can be slow (reading new lines, reading a screen) brought it to +60 ms and +2,420. What is left is mostly the 4 Hz tick itself: wakeups of the runtime's timer thread and sysmon, not work. Keep a guard per watch (`reading`, `live.reading`) so a slow parse or screen read skips that watch's ticks, as a busy ticker did, without holding up the others; and a lock taken on a tick must be a `TryLock` (a file being paged answers "changed").
- An I/O-bound process does not need a P per core. `node --stdio` and `node --attach` cap GOMAXPROCS at 4 (64 on the measuring host): threads 28 to 12 and about 1.7 MB less anonymous memory with 4 watches, CPU within noise. Cap it with `runtime.GOMAXPROCS` at the entry rather than in the environment, so the agents and tools the node starts keep their own default, and only when the default is above the cap: an explicit value stops the runtime from following a container's CPU limit, so a smaller default is left alone. A `GOMAXPROCS` variable always wins.
- A fallback that fails fast is still a process per call. With the herdr socket missing, every herdr call ran the CLI, which answered `server_not_running` in a few milliseconds, but the node asks about twice a second. Back off a fallback while it keeps giving the same "nothing there" answer, hand that answer back meanwhile so callers and logs see what they saw before, and reset on any success through either path.
- A buffer sized for the biggest answer, made per call, can cost more than the answer. Every herdr call is a connection of its own (herdr answers one request per connection), and each read its answer through a new 64 KiB `bufio.Reader`: 82 KB allocated for a 3.5 KB `pane.read`, and a resting Claude makes about 15 such calls a minute. The readers are pooled now (`answerReaders`, `Reset(conn)`), safe because `readLine` copies the line out (16 KB per call, #3477). Before pooling a reader, check that nothing returned aliases its buffer, and size what the pool keeps for the burst, not the average: a list pass starts the due dialog reads of resting agents together (a goroutine each), so the pool keeps about one reader per agent. With 64 KiB readers and 32 resting agents the node's anonymous memory was 3 MB higher than before pooling; with 16 KiB readers, which still hold a typical pane read whole (a bigger answer only grows `readLine`'s copy), it is within noise.
- A command that needs no server has no "nothing there" answer to back off on. `herdr session list` reads the sessions directory and knocks on each socket, so it succeeds with nothing running, and the node ran it every 5 s for the same output. Keep such an answer only while everything it read is unchanged and could not hide a change: the binary, the directory's entries, and each listed socket still missing (`herdr/sessionlist.go`). An answer that only a connect can give (a session running; a stopped server can leave its socket file) is never kept.

## A skip decided from two counters, and a test that swaps a served peer (2026-11-10, #2439)

`sendSessions` skips a byte-identical list only when no `sessions.list` answer is pending and the epoch has not moved;
an answer moves the epoch and then drops the pending count when it ends. Read the pending count first and the epoch
second. Read the other way round, an answer that starts and ends between the two reads leaves the old epoch and a zero
count, and the list is skipped behind an answer the manager may have taken instead. With a 20 µs pause injected between
the reads, 199 of 200 runs skipped; with the order swapped, 0 of 200. Without the pause it never showed in 2 million
runs, which is why only reasoning about the order, not a test, catches it.

A test helper that has already started `go n.peer.Serve(...)` must not assign `n.peer` afterwards: the goroutine reads the
field, so `-race` reports it, and it may end up serving the new peer too. Pass what the test needs (here a writer that
records the node's lines) into the constructor, and serve the peer through a local variable. A rule test also needs
steps that differ only in the guarded rule: revs followed by a list that differs anyway, or a `call` that waits for the
pending count to drop, pass with the rule deleted.

## Inbox rows as json: compression, HOT and the NUL guard (2026-11-10, #2779)

- **Compression goes in the same `ALTER` as the rewrite.** `ALTER COLUMN … SET COMPRESSION lz4` on its own changes only
  future writes, and `VACUUM FULL` or a same-type `ALTER … TYPE jsonb` copies each value with its old compression: 0 of
  28,600 rows turned lz4 either way. A type change resets the column's compression to the default, so a `SET
  COMPRESSION` run before it is lost too (`USING data::text::jsonb` after it: all pglz). Put the type change (`USING
  data::json`) and `SET COMPRESSION` in one `ALTER TABLE`, in either order: every value is computed again and compressed
  with lz4 (28,600 of 28,600, about 2 s). Check with `pg_column_compression`. For servers built without lz4
  (`feature_not_supported`) or before Postgres 14 (`syntax_error`), catch the error in a `DO` block around `EXECUTE`: a
  statement written into the block is parsed when the block is compiled, so a syntax error escapes the handler.
- **json, not jsonb, when every reader unmarshals the bytes.** The manager's start read of 3,999 items spent 59 of its
  67 ms sending the rows (jsonb back to text after pglz); as lz4 json it takes about 30 ms. Each half alone saves less:
  jsonb with lz4 -20 ms, json with pglz -13 ms, both -34 ms (same rows, 11 rotated rounds). Old prepared statements see
  a changed result type once (`cached plan must not change result type`); pgx drops the statement on that error and
  prepares it again on the next call.
- **A generated column, not an expression index, for a lookup into the document.** An index on `(data->>'source')`
  makes every update of `data` touch that index, so no update is HOT (0 of 4,800); a stored generated column whose
  value did not change leaves HOT possible (1,481 of 4,800 on the same updates; 142 to 300 of 300 per round on upserts
  shaped like `PutInboxItem`, from 0, once `updated_at` left `inbox_items_open`). An index on a column every upsert
  changes rules out HOT as well; `TestDoneInboxItemsIndexed` allows only `done_at`, `id` and `source` in indexes.
- **The generated column is also the NUL guard.** json accepts `\u0000` in a string, jsonb refused it on write.
  `data->>'source'` parses the whole document, so a write with `\u0000` anywhere still fails with 22P05, as before.
  Without the column such a row would be stored, and the done page's `data->'connector'` would fail on it later. Keep a
  computed column (or a check) that parses the document if `source` ever goes or becomes a column Go writes:
  `TestInboxItemWithNULRefused` fails without one.
- **A compatibility overload makes untyped calls ambiguous.** `jsonb_typeof(json)`, added so a pre-0471 build's done
  page still runs, makes `jsonb_typeof('…')` or `jsonb_typeof($1)` with an unknown-typed argument fail in the whole
  database (`function jsonb_typeof(unknown) is not unique`). Every existing call passes a typed value; new SQL casts
  the argument (`$1::jsonb`) until a later migration drops the shim, once no pre-0471 build can run.
- **The start read decodes from the driver's buffer, so an inbox type must not keep its input.** `InboxItems` hands
  each row as `rows.RawValues()[0]`, which the next row overwrites. `encoding/json` copies every string and
  `json.RawMessage` it keeps, so `InboxItem` holds none of the buffer; a custom `UnmarshalJSON` on any type inside it
  must copy what it keeps too, or the items loaded share bytes the driver reuses. `TestInboxItemsAllocationBudget`
  fails if the read starts copying rows again (6 allocations per 200-row read; collecting the rows took 616).

## Optional filesystem labels must not gate inventory (2026-11-10, #2750)

A regular Git HEAD can block in open despite stat succeeding. Keep project labels outside authoritative session discovery, cap concurrent metadata readers, and deduplicate pending paths across cache refreshes. Return all discovered session identities and busy states even when labels are unavailable; never replace a failed session read with an empty idle inventory. Test with a HEAD whose read cannot complete until after the inventory response, and retain a synchronous negative control.

## Hot paths: a merged list decoded to stamp one field, a whole inbox localized again on every GET (2026-11-10, #2807)

- Go 1.27's `encoding/json` is built on json/v2 (`GOEXPERIMENT=jsonv2` is on by default), and its rules for raw bytes are not the classic ones: an `Encoder` with `SetEscapeHTML(false)` still escapes U+2028 and U+2029 inside a `json.RawMessage`, which the v1 encoder copied as they were. A hand-written JSON fast path therefore copies only what `encoding/json` itself would write unchanged (compact, no `<`, `>`, `&`, U+2028 or U+2029 in a string) and hands everything else to `encoding/json`, never escaping by hand. Pin it with a differential test against a copy of the old code over every edge (`TestWriteMergedMatchesDecoding`) and a short fuzz target (`FuzzWriteMerged`).
- `writeMerged` decoded every manager's answer into a map, decoded each item into a map only to look for `manager`, and encoded everything again: 13 ms and 7 MB for 400 inbox items on every refetch. One `json.Valid`, then a structural scan that cuts out the top-level members and the items, writes the same bytes in 3 ms and 0.5 MB (`merged_scan.go`). Anything the scan cannot read the way decoding does goes to the old decoding, kept as `writeDecoded`: a top-level key that is escaped, non-ASCII or repeated (how a map keeps and sorts such keys is decoding's to say), a list that is neither an array nor null. Do the same for any other reader the scan replaces: `mergeList` takes the todos' ids from the scan only where a key cannot fold to `todos` or `id` (`encoding/json` matches struct fields regardless of case) and the id is plain ASCII, and decodes otherwise (`TestTodoOwnersMatchDecoding`).
- `i18n.InboxJSON` decoded and marshaled the whole inbox list on every GET to change a few labels (17-22 ms, 4-7 MB for 400 items). What it does to a value depends on that value alone, so the hub localizes each item on its own and keeps the result by a 128-bit hash of the item's bytes, per list route, manager and language, for the items of the latest answer only (`inbox_forms.go`). Byte identity with the whole-body pass needs one more step: when anything in an answer changes, the whole answer is marshaled again, so every unchanged item and the top level are then written as `InboxReencoded` (decode with `UseNumber`, marshal) gives them. An answer whose top level is itself an item is localized whole, since `InboxJSON` then visits none of its list. Equal bytes do not show the per-item path ran, so the relay test also counts the slots kept (`TestInboxListsLocalizedAsWhole`).
- A 2xx answer whose body was `null` made `writeMerged` panic: `json.Unmarshal` of `null` into a map leaves it nil, and the merge then assigned to it. Treat any answer that is not a JSON object, `null` included, as no answer.

## Remote tests need fixtures outside the Go module (2026-11-10, #2980)

When server tests read a shared fixture outside `server/`, include it in `scripts/test/test-server.sh` at the same
relative path and create its remote parent directory. The inbox list tests read `scripts/mock/inbox_events.json`;
copying only the Go module left them failing on the test machine. Extend the fake remote launcher test to read the
staged fixture from the package directory, check refreshed contents, and reject a missing source before Go starts.

## A node error without a code reads as "the hub is down" (2026-11-10, #1888)

A node method that answers a refusal with a plain `errors.New` / `fmt.Errorf` has no code, so `apierr.Call` treats it as `internal` and the hub answers 502 with the generic `rpc-errors.failed` summary; the real reason survives only in `detail`, which no client shows. Web words every 502 as "the hub can't respond right now", iOS and Android show "The request could not be completed": a dirty worktree or a missing upstream looked like a broken hub. A refusal the user can act on goes out as `refusal(code, english message, data)`, its code goes into `rpcFailureKeys` with a summary in every `rpc-errors` locale (name `{reason}` there to keep git's or the program's own words), and the hub then answers a 409 in the client's language. Success texts follow the same rule: answer a code (`outcome`) the clients put into their own strings, and keep an English `message` for older clients.

## Hot paths: a list encoded whole for each of its readers, a handler built for each request (2026-11-10, #2997)

- Every inbox change makes each app GET the whole inbox, and the manager copied every item (`listed`) and encoded all of them again each time: 4.1 ms and 1.8 MB at 400 items, 19 ms and 10 MB at 1,600, usually for no change at all. `listed` reads the item alone, so the manager keeps each item's bytes for that item pointer and encodes only the items without them (`inbox_list_memo.go`): 0.33 ms and 0.6 MB at 400, most of it the answer itself. A memo like this is only as good as its invalidation, and a struct field write can't be hooked: forget the bytes where every change already passes, here `touched` (the rollback bookkeeping's stamp) and `saveInboxPrepared` (every change is saved there), and compare against the pointer so an item replaced under its id is not listed from the old one's bytes. `saveInboxPrepared` forgets them before anything else: as it starts (it can wait for another save, and it returned early before the inbox was read), as it takes the rows and after its finish, which may undo a change.
- "Every change is saved" is not "saved before the next GET". A change that makes a call before its save (a reply going out, a group append's retries, an Agent start) is listed from old bytes for that whole window, and a reply kept open by a move that came in meanwhile is never saved by its own path, so the GET kept listing the item without its sent entry until the item changed again, though the reply's own answer showed it. Forget as the item changes, in the helper most of those changes pass (`chainAdd`), and where they don't (`putEvent`, an event's conversation being started); don't use `touched` for it, whose stamp the rollbacks compare. `TestInboxChangesAreListedBeforeTheirSave` lists each such change before its save.
- Equal bytes in a test do not show the memo is right in every state the package reaches. The package's tests list every kept item against a fresh encoding (`inboxMemoVerify` in `TestMain`), so any test that changes an item and lists the inbox again catches a missed invalidation; it found the early return above on its first run. Count the items encoded too (`TestInboxListEncodesOnlyChangedItems`), and remove each invalidation once to check a test fails.
- A writer that knows the answer's size grows the bus recorder once (`recorder.Grow`) instead of letting the buffer double as the pieces come: at 400 items the GET allocates 594 KB for a 542 KB answer.
- `BusHub.request` built `fleetHandler(d.Handler(s))`, two ServeMuxes with twenty routes, for every relayed request: 0.13 ms and 43 KB each. Route registration that only stores closures can be done once per service (`BusHub.httpHandler`); rebuild when the service changes and test that the new service is the one answering.

## Embedded code: stored raw, copied per package, read whole on first use (2026-11-10, #3455)

- `//go:embed` stores files as they are. The runtime packages (`server/internal/runtimepkg`) went into `personad`, `persona-node` and `persona-manager` raw: 1.63 MB, 1.06 MB of it the Agent SDK bridge, and `runtime-sdk` three times over. Generate writes each `integrations/` directory a package takes as one gzipped tar, stored once however many packages take it: 425 KB. A release archive is gzipped anyway, so the download stays the same (within 0.1%); what shrinks is the binary on disk and in memory, by about 0.93 MB.
- Keep generated archives byte-for-byte reproducible: no name or time in the gzip header, mode 0644, time 0 and no owner in tar entries, a fixed level, entries in path order (`TestGenerate` runs it twice and compares).
- A set read whole on first use keeps all of it: the first `Load` walked every package, so a node serving only DSH held the 1 MB bridge too (1.6 MB retained). Unpack per package and per part, each once (`dist.load`): 0.21 MB for DSH. Prove the unpacked files and hash equal the sources' (`TestGeneratedPackages`).
- Compressed code is noise to a strings scan. `check_bundle_privacy.py --whole-short` inflates every gzip member it finds in a binary and scans that text as well; a test hides a private word in a member inside binary bytes, after another member and in a member cut short.

## A download the app gave up on, and a budget no slow link could meet (2026-11-10, #3692)

On TestFlight 224 an extension's **Review** spun for two minutes and then failed. The operations list (`GET /api/v1/extensions/-/operations`) told the story the access log could not: the settings page's synchronous `catalog/preview` was downloading a 13 MB package at about 33 KB/s from the hub machine; the app's 120 s request timeout closed the request, the hub's relay to the manager closed with it, and the preview's context (the request's) took the download down at 4.5 MB (`context canceled`). Tapping again on the reopened page started a second download of the same package beside the first, which got nothing for 30 s (`download_stalled`, `400 invalid_plugin`). A read-only preview with no client timeout then showed the deeper problem: the download kept moving and was cut at 10.1 MB by `Download`'s fixed five-minute budget, so on that link it could never succeed, synchronous or not.
- A long download must not belong to one request. The manager now has one download per package at a time (by cache and sha256, `market_download_shared.go`): callers wait for it and get its progress replayed from where it is; when they all went away it goes on into the cache; only the user's Cancel (`context.Cause` is `extension.ErrUserCanceled`, set by `DELETE …/operations/{id}`) stops one nobody else waits for.
- Bound trickles by size, not by a constant: the stall rule catches a dead link, and the total budget is 30 minutes or the time 16 KiB/s needs. Tell our own budget from the caller's with `context.WithTimeoutCause` and a sentinel cause, or a caller's deadline reads as "too slow".
- When the client of a synchronous call leaves, the operation it started should say so in words (`client_left`), not `context canceled`.
- Progress is reported at most every 200 ms: a test that waits for an exact byte count can wait forever; wait for "some bytes" and prove there was no restart by counting requests.

## macOS `ps` prints `nobody` as UID -2 (2026-11-10, #3687)

- `uid_t` is unsigned, but macOS `ps -o uid` prints it signed: `nobody` (4294967294) shows as `-2`. Linux prints 65534, so hosted CI (Ubuntu) never sees a negative owner. `parseProcessTable` treated one as a corrupt row and failed the whole table, so `personad manager maintenance prepare|observe` broke on any Mac running a `nobody` process (`dhcp6d`, the DHCPv6 client, once a network uses DHCPv6).
- When a check covers every row of a system table, validate the format (a number), not a range taken from one platform; keep the real constraint (the target is the current user's) on the row it is about.

## A field that pushes a row past its size class (2026-11-11, #3937)

- `proto.Session` is copied row by row all over the hub, the manager and the node, and the allocator rounds every allocation up to a size class. #3897 added two fields (40 bytes) and took the struct from 1,528 to 1,568 bytes. A row copied on its own went from the 1,536-byte class to 1,792 (+256 B), every list copy grew by 40 B a row plus page rounding, and four allocation budgets in the assistant failed at once, none of them near the change. The readers copied no more rows than before.
- When a budget is over by one size-class step per row it keeps, or by rows × a few bytes, compare the row type's `unsafe.Sizeof` at both commits before hunting for new copies.
- Make room before raising a budget. The 17 bools sat in 10 separate words, 63 bytes of padding; moving bools beside other bools brought the row back to 1,528 bytes without dropping a field. Reordering fields reorders the reflect encoder's keys, so `appendSession` and `sessionWireLayout` follow, and byte goldens change only in key order (check that every line decodes to the same object). `TestSessionFitsItsSizeClass` now fails in `proto`, listing the bools that sit alone, instead of in another package's budgets.

## What the port relay costs, hop by hop (2026-11-10, #3067)

`server/cmd/vncbench relay` puts an echo server behind `…/ports/{port}/connect`. With everything on the hub machine
(client, hub, manager, its own node) a small message's round trip is 1.6 ms, 0.8 ms more than a WebSocket ping to the
hub, and a download runs at 88 MB/s: the hub, the bus, the manager and the stream mux are not where a live screen's
latency goes. The hub → node leg (`ssh -T` over Tailscale on the LAN) is where it varies; plain `ssh host cat` shows the
same spread, and the Mac's `sshd-session` already has TCP_NODELAY (lsof `-T f`: TF flag 0x4), so it is not Nagle.
## A child in its own process group outlives a node killed by `exec.CommandContext` (2026-11-11, #4259)

- The manager ran the hub machine's node (`node --stdio`) with `exec.CommandContext` and the default `Cancel`, a SIGKILL. The node puts each assistant process in its own group (`Setpgid`, so `stop()` can end its background tasks), and nothing ties that group to the node: a SIGKILLed node leaves its conversations running. Before blue-green switching, the systemd restart's cgroup kill hid this; a switch keeps the unit, so the old Claude processes went on beside the ones the next node resumed for the same conversations, and the next node rightly refused them the assistant's tools.
- A child that must not outlive its parent needs the parent to stop it, and the parent needs time to: end such a process the way its peer going away ends it (EOF on stdin) with a bounded grace before the kill (`cmd.Cancel`, `cmd.WaitDelay`), and have it stop its own children and wait for them before it exits. Stopping them in a goroutine is not enough: `Serve` returned and the process exited before the goroutine's SIGKILL fallback ran (`stopAndWait`).
- A test that kills the parent and looks for the child catches both halves; one that only checks the parent's exit status does not.

## A field in `proto.Session` is paid on every copy (#4342)

The hub copies sessions by value (decorated lists, readers that keep rows), and its allocation budgets
(`internal/assistant` `TestSessionReadersCopyNoList`, `TestAssistantViewCopiesOnlyItsConversations`) measure bytes per call.
One `int` took a 1,528-byte `Session` to 1,536, and those readers went 5 to 12% over. Small fields fit in the padding instead
(between the bools and the next 8-byte field): check the layout with `reflect` field offsets and keep the size unchanged.
A new field type also needs wire samples in `session_wire_test.go`.

## Fleet events: bytes checked again on the bus, every machine built to read one (2026-11-12, #3197)

Measured with the fleet soak (`docs/operations/testing.md` "Fleet soak": 10 computers x 50 sessions, a change per
computer every 2 s): the manager allocated 555 MB and used about 2.1 s of CPU a minute; the shares below are of those.

- `publishList` and `tellLive` handed `Tell` the bytes `json.Marshal` had just written as `json.RawMessage`, so the
  bus's envelope encoder checked, compacted and copied each list again: 9.6% of the manager's CPU. Bytes that are
  `json.Marshal`'s go as `rpc.Marshaled`. Prove the wire, not the type: write every told value both ways and compare
  the lines (`TestFleetEventsGoOutAsTheSameLine`), with HTML characters, U+2028, control characters and invalid UTF-8
  in the rows, and a list that does not encode (a time past the year 9999), which still goes out as `null`.
- An allocation ceiling taken with the GC off cannot see a copy that lands in a pooled buffer. Told either way, a
  200-session list allocated the same 663 KB once json's encoder had grown its `sync.Pool` buffers; in a busy manager
  a GC empties those pools every few lists. Take such a bound cold: two `runtime.GC()` calls before each run (the pool
  has two generations) and none during it. The two ways are then 708,868 and 1,224,668 bytes, to the byte
  (`toldAllocation`), and a second test keeps the old way failing the same limit.
- A lookup that builds the collection pays for every element's derived fields. `fleetMachine(id)` built every
  machine's summary (`Devices.List`: each one's trust from all its paths, whether it is reachable) to read one, several
  times for each list a node sends: 5.2% of the manager's allocation. Ask the owner for the one (`Devices.Summary`,
  8 bytes where the list of ten was 4,080), build it with the function the list uses, and compare the two in every
  state the list can be in: before the first regroup, with absorbed and derived machines, and while the entries and
  the listed order disagree (`TestSummaryIsListsEntry`).
- The micro benchmarks did not show either share: the fake hub writes no bus line and the fake devices' list is free.
  A benchmark of a path has to end where production ends (`lineHub` writes the line as `BusHub.Tell` does), and the
  shares come from profiles of the real programs: the soak lays a sampler over `cmd/personad` at build time
  (`go build -overlay`), so nothing in the shipped program changes to measure it.
- CPU time per minute moved by a factor of two and more with the machine's state (an Apple Silicon Mac moves a
  process between fast and slow cores; a whole run on the slow ones used 3.5 s a minute for the work that took 1.7 s
  on a quiet minute before), while bytes allocated per minute stayed within 1.5% and repeated within 0.1% between
  runs. Soaks run one after the other said -3%, +68% and +30% for three changes that each save CPU; even the same
  function's samples cannot be compared across runs, since code that copies memory slowed 2.3 times where a `write`
  slowed 1.06. Dividing by the hub's CPU time in the same minute helped, and was still 3 points apart between two
  sessions. What worked was running the trees at the same time, each with its own hub, manager and database: every
  minute is then the same machine for all of them, and the head's CPU time over the base's in the same minute came
  out at 0.943 with every one of the 20 minutes below 1, the unchanged hub's at 0.983 (`pairedCPU`). Measure a noisy
  quantity against a reference that sees the same noise at the same moment.

## `http.DefaultTransport` drops a push connection after 90 s idle (2026-11-11, #4800)

- The hub's relay client and `apns.Client` were `&http.Client{Timeout: …}` with no transport, so they ran on
  `http.DefaultTransport`, whose `IdleConnTimeout` is 90 s. Two thirds of one hub's pushes came more than 90 s after the
  previous one and paid a new TCP + TLS handshake (0.2-0.3 s to the relay; APNs wants one long-lived HTTP/2 connection).
  Both now use `apns.NewTransport`: their own clone of the default transport with a one-hour idle timeout and HTTP/2
  health pings (`http.HTTP2Config.SendPingTimeout` / `PingTimeout`).
- A client that talks to one service often gets its own transport with the idle timeout chosen for that service. The
  default one is shared with every other client in the process, and its 90 s fits none of them in particular.
- Test connection reuse with `testing/synctest`: serve TLS on an in-memory `net.Pipe` listener (loopback sockets do not
  let the bubble go idle), dial it from a clone of the client's real transport, count `http.StateNew` in the server's
  `ConnState`, and move the gaps with `synctest.Sleep`. The transport's idle timer and pings then run on the fake clock
  (`TestRelayPushesKeepOneConnection`, `TestSendsKeepOneConnection`). Register the server's and transport's shutdown
  with `t.Cleanup`, or a failed assertion ends in a synctest deadlock panic.

## `exec.CommandContext` cancels only while the process runs (2026-11-11, #4363)

- `cmd.Cancel` runs only when the context ends before the process exits. A shell that exits at once and leaves a background child holding its stdout or stderr (`sleep 4 & echo $!`) keeps `Wait` reading the pipe until that child exits or `WaitDelay`, counted from the shell's exit, closes it: the deadline passes unnoticed. `secret.run` held a 1-second run for 4 seconds, and the next command, which could no longer start, came back as timed out.
- Watch the context for as long as `Wait` runs and stop the group at the deadline, sharing one "stopped" flag with `Cancel` so the group gets one SIGTERM (`runSecretCommand`). Classify the result by that flag, not the exit status: the shell exited 0.
- A command that never started because the time was used up gets no result. A timed-out result for it reads as a command that ran and was stopped, and makes the Agent check the computer for one that never ran.
