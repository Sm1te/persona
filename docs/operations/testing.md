# Testing

## Commands

```sh
cd server && go test ./... && go vet ./...                    # server
SIM=$(scripts/app/sim-boot.sh "Persona Tests <issue number>")              # your own simulator, slim boot ("Simulators")
cd apple && xcodegen generate -q && xcodebuild -project Persona.xcodeproj -scheme Persona \
  -destination "id=$SIM" test   # iOS unit tests (ChatStore, ScreenParser, phone tool registry)
./scripts/app/sim.sh [-openSession <machine>/<id>]                    # run in simulator, pair automatically to the real hub
cd apple/Packages/PersonaKit && swift test                             # PersonaKit unit tests: directly on Mac, no App build or simulator
python3 -B scripts/test/test_sim_boot.py                               # sim-boot.sh (fake xcrun / simslim, no simulator)
n=0 failed=""; for f in $(find scripts -name 'test_*.py' -not -path '*/node_modules/*' | sort); do grep -q unittest "$f" || continue; n=$((n + 1)); (cd "$(dirname "$f")" && python3 -B -m unittest -q "$(basename "$f" .py)") || failed="$failed $f"; done; echo "$n suites, failed:${failed:- none}"; [ -z "$failed" ]   # all Python suites in scripts/, like CI (it skips files that never mention unittest); any failure exits nonzero
```

## Hosted CI

**No workflow runs automatically for a pull request, branch push, merge queue or schedule.** Before merging a PR, run the relevant
commands in "Build and verify" locally, inspect required screenshots and run `scripts/privacy_check.py`. The `CI required` check is
not a PR gate. A scheduler explicitly dispatches full CI on the latest `main`; a failure is fixed separately. Local PR verification
and scheduled post-merge CI are both required operational checks, not substitutes for one another.

`ci.yml` accepts only `workflow_dispatch`. Its `full` boolean defaults to **true**, so a plain dispatch runs all six required
platform lanes plus the independently reported Mac lane even on unchanged `main`; path filtering cannot silently turn a default run
into a collection of skipped jobs. Set `full=false` to use `lanes`: `all`, `auto` (diff against `origin/main`), or comma-separated
`server,web,android,apple,runtimes,scripts`. `base` overrides the comparison for `auto`. Unknown lane names fail.
Inspect selection locally with `python3 scripts/ci/paths.py --all` or `python3 scripts/ci/paths.py --base origin/main`.

```sh
gh workflow run ci.yml --ref main -f full=true                       # full suite (the default)
gh workflow run ci.yml --ref main -f lanes=all -f full=false         # equivalent explicit lane choice
gh workflow run ci.yml --ref <branch> -f full=false -f lanes=server,web
gh workflow run ci.yml --ref <branch> -f full=false -f lanes=auto -f base=<commit>
gh workflow run apple-test.yml --ref main -f lane=ios                # iOS and embedded Watch
gh workflow run apple-test.yml --ref main -f lane=macos
gh workflow run pr-needs-issue.yml --ref main -f pr_number=<number>  # metadata enforcement
```

The six required lanes have independent reusable workflows. **CI required** rejects failed, cancelled, missing or unexpectedly skipped
selected jobs. iOS/Watch contributes to that aggregate; **Mac (non-blocking)** reports its actual independent result without
`continue-on-error` and is not part of the aggregate. An unselected lane is never claimed as tested.
All workflows except the image publisher accept only `workflow_dispatch` and, for reusable platform workflows, `workflow_call`.
The image and Release publisher additionally retains `push` for **`v*` release tags**: Docker installations use its version and `latest` images, and the same tag gets a GitHub Release.
No branch push or PR starts it. The issue-link Action also requires explicit dispatch from the default branch with a PR number.

The `changes` job runs repository/commit privacy and path safety, including `scripts/ci/guards.sh`, migration freeze and sleep ratchet.
An edit, deletion or rename of a `.sql` file already on the base under `server/internal/db/migrations/hub` or
`server/internal/db/migrations/manager` fails that check. Comment-only and whitespace-only edits pass; add a new migration instead.
Each `_test.go` file under `server/` and `sdk/go/` has a committed count of `time.Sleep` calls (`scripts/ci/sleep_baseline.json`). The
count may stay the same or go down, never up, and a new file starts at zero. Comments, string literals and a line carrying
`// sleep-ok: <reason>` (the reason must be non-empty) do not count. `python3 scripts/ci/sleep_ratchet.py --update` only lowers
the baseline; it refuses to raise a count or to add a file that still sleeps. See `docs/lessons/testing.md`.
With `lanes=auto`, the base defaults to `origin/main`; an explicit `base` is resolved to a commit before guards and path selection.

Dispatched code runs with `contents: read`, without persisted checkout credentials, private config or new secrets. The issue-link
workflow uses metadata-only write permissions and runs only from the default branch. Image and GitHub Release publishing have `packages: write` and `contents: write` only in the publish job. CI concurrency keys use the repository/branch and full/lanes selection; dispatches in the same group cancel stale
pending/running work, while different selections stay independent. Reusable workflows retain their file-specific lane groups: deriving
a caller and callee's group only from `github.workflow` would let a called workflow cancel its caller.

Manual server and Android matrices allow at most **two** simultaneous shards each; the full scripts matrix allows **one**. All shards
still execute with `fail-fast: false`; these caps trade wall-clock latency for available runner slots. Hosted queue time is outside
repository control and must be reported separately from test execution. Do not change repository settings/rulesets or procure paid or
self-hosted capacity as part of these controls.

Server keeps four independent shards (at most two concurrent runners): browser/node, hub/voice/assistant, all other packages, and
formatting/vet/build/Windows/SDK checks. Every package from `go list ./...` belongs to exactly one test shard, including future packages.
Browser tests remain isolated from other packages. Go module/build caches are keyed by exact Go version, shard, dependency sums and
source inputs, with same-shard fallback keys; `-count=1` keeps test execution uncached. Test-event summaries report the slowest real
tests, rather than inferring runtime from `Sleep` inside fake-clock tests. The full scripts suite splits complete sorted discovery
across two independently isolated jobs, scheduled one at a time; each has its own synthetic HOME and mock ports.

Android runs two independently isolated lanes, both at once: `lint-build-test-0` (checker tests, all-module lint, the debug APK and
the first of two disjoint sorted feature-test shards) and `test-1` (core tests, the second feature shard, and platform/app tests).
The parts are balanced by measured task time, and each lane runs its parts in one Gradle invocation, so the upstream compilation they
share runs once per lane rather than once per part (#3056). Module declarations are dynamically partitioned; unknown module prefixes
use a fallback part, while unsupported declarations fail rather than skip coverage. Every module's complete test selector retains all
variants. Gradle workers remain bounded; build and public Robolectric dependency caches do not contain private config. Per-lane Gradle
profiles and test/lint reports are uploaded for diagnosis. Local entry points without lane options still run complete verification.

| Platform / reusable workflow | Local entry | Checks | Hosted runner | Planning duration (warm / cold) |
| --- | --- | --- | --- | --- |
| Server / `.github/workflows/server-test.yml` | `scripts/ci/server.sh` | gofmt, vet, complete Go tests against disposable Postgres 18, build, Windows cross-build/vet, the programs and tunnel tests with `server/build-tags.txt`, extension schema/examples/SDK bundle, Go SDK tests; hub route/architecture, prompts and hub i18n guards are Go tests | ubuntu-latest | 8–15 / 15–30 min |
| Web / `.github/workflows/web-test.yml` | `scripts/ci/web.sh` | npm ci, scoped ESLint, TypeScript, complete Vitest, production build and privacy | ubuntu-latest | 2–4 / 4–7 min |
| Android / `.github/workflows/android-test.yml` | `scripts/ci/android.sh` | i18n/token checker tests, translations/literals, token ratchet, all-module lint and unit-test variants, debug APK | ubuntu-latest, two lanes | 8–15 / 15–30 min (branch runs with main's caches measured 15–18 min, #3056) |
| Apple / `.github/workflows/apple-test.yml` | `scripts/ci/apple.sh ios` and `scripts/ci/apple.sh macos` | PersonaKit/iOS/Watch/HTML and Mac build/unit tests; synthetic configuration, no distribution signing; real results remain visible | macos-26 runners, Xcode 26.0.1 (Swift 6.2) | queue and execution measured separately |
| Scripts/docs / `.github/workflows/scripts-tests.yml` | `scripts/ci/scripts.sh` | every Python script test, doc/code paths and index, layout/shell/Python compatibility, privacy-rule regression, catalog/translation/token checks, actionlint, issue-link logic, website worker | ubuntu-latest | 2–4 / 4–7 min |
| Runtimes / `.github/workflows/runtimes-test.yml` | `scripts/ci/runtimes.sh` | TypeScript SDK typecheck/test/portability, Agent SDK build, every runtime integration's npm tests | ubuntu-latest | 2–4 / 4–7 min |
| Container and Release / `.github/workflows/release.yml` | `scripts/ci/hub-image.sh` | amd64 build; isolated Compose Postgres, HTTP/TLS health, pairing, web, admin isolation, file ownership/0600; publication remains amd64+arm64, then a version-tag run creates the GitHub Release | ubuntu-latest | 8–15 / 15–30 min |

These planning estimates predate sharding and are not measured success claims; use job timestamps and each script's PASS/FAIL/TIMEOUT
durations for an actual run. Parallelism reduces wall-clock latency but can increase total runner minutes through repeated setup and
compilation. Cancellation reduces wasted old runs, not already-used minutes. Public-repository standard hosted runners do not consume
private Actions minutes; billing multipliers and plans may change. Measure actual queue and runner time before budgeting.
With `full=false` and `lanes=auto`, Apple is selected by Apple/shared schema/spec/simulator/localization inputs,
not by a plain docs/web/server edit. The default `full=true` always includes it. Go/npm/Gradle/SPM download/build caches are used;
caches on dispatched branches are read-only except for the cache-writing paths explicitly restricted to `main`, and no credentials
are stored. Server Go and Robolectric caches restore with `actions/cache/restore` and save only on a `workflow_dispatch` of `main`
(`scripts/ci/test_shards.py`, `scripts/ci/test_android_shards.py`), so branch runs cannot push main's entries out of the
repository's 10 GB cache. The initial SPM/runtime download makes a cold Apple run materially slower. Cache and parallelization work remains
tracked in #1214; cache-based test substitution is not allowed.

Local prerequisites: Go from `server/go.mod`, Node from `web/.nvmrc`, Python 3.9+ (CI uses 3.11), Docker/Compose for server/image,
`pip install -r scripts/ci/requirements.txt` and `go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` for scripts, Bun 1.4.2 for
TypeScript SDK. Android needs JDK 21 and SDK `platforms;android-37.0` / `build-tools;37.0.0` plus GNU timeout; Apple needs macOS 26,
Xcode 26.0.1, XcodeGen and rsync. Local scripts fail on missing required tools rather than claim success. They do not deploy or publish.
The unsigned Mac app/notification-extension build keeps shipping entitlements. Before the ad-hoc signed offline Mac test invocation,
`scripts/ci/apple_entitlements.py` removes only profile-authorized push, time-sensitive notification and shared Keychain permissions from
the disposable source snapshot. The sandbox, other permissions and project hardened-runtime settings remain unchanged; the checked-in
project and release entitlements are not edited. A valid ad-hoc signature carrying restricted permissions can still be rejected before `main`.
On Apple failure, collect the simulator UDID log, boot command/status log, installed runtime metadata, `xcrun simctl list --json`, available
disk space, and any xcresult bundles before deleting the owned simulator. The workflow uploads the allowlisted diagnostics as
`apple-<lane>-diagnostics-<run id>-<attempt>` artifacts for seven days. Source/configuration files, keys and result-bundle symlink targets
are excluded. Do not infer a simulator boot timeout's cause without its diagnostics, add automatic retries, or extend the boot deadline.
A maintainer-approved `gh run rerun --failed` runs the original commit; it cannot acquire diagnostic code added by a later commit.

Deliberate boundaries: web `compatibility.live.test.ts` needs an isolated live hub/token and explicitly skips its one probe without them.
The offline web tests still run completely. Apple explicitly excludes five fixture/watchdog-dependent iOS cases (`OldHubCompatibilityIntegrationTests`,
`NotificationServiceTests/opensWhatTheRelayDelivered`, `VideoFileTests/nativeVideoPlaysSeeksAndCleansUp`,
`VideoFileResourceTests/realHTTPReadsMiddleTailAndRejectsChangedRevision`, `AssistantHTMLSecurityTests/aRealInfiniteLoopCanBeDiscardedWithoutBlockingANewCard`)
and five `ExtensionStreamSnapshotTests` Mac cases needing a live extension/hub pair. Apple result parsing rejects any unexpected skip and
requires the four design-system suites; UI suites, visionOS, Persona Link and SSH-package integration remain outside this lane.
Full simulator screenshots, device/instrumentation/UI suites, real Agent/phone integrations, release entitlements,
production signing and publication need devices, explicit lab inputs or secrets; retain the manual checks below.
The stock iOS unit-test simulator is granted Reminders access for the synthetic CI app, and `TEST_RUNNER_PERSONA_EVENTKIT_ROUNDTRIP=1`
runs the existing EventKit create/list/update/delete round-trip against its disposable store. This does not use a personal device or account.
Platform-gated and explicitly opt-in tests still report their own skips; they are not evidence of device/live integration coverage.
On Linux, `test_hub_restart_mac.py` skips its three Mac-only cases; `test_persona_cutover.py` has six optional database/container cases
without dedicated cutover inputs. The complete manual Python lane still discovers and runs every suite; no failing suite is excluded.
Manual server CI's Postgres does run ordinary database tests (unlike the old database-free workflow). Specialized offline cutover fixtures requiring extra
container/migration inputs are not supplied. Private-value scanning of commits and PR metadata remains in `scripts/hooks/merge-pr.sh` on
the merge machine; CI runs the generic privacy rules without those values. No baseline is raised to make a gate green.

`release` uses only public dependencies, the GitHub CLI preinstalled on hosted runners, and the existing `GITHUB_TOKEN`; no private registry or machine service is required. Its
repository enablement is a separate setting: enable only after confirming the hosted build/smoke result. Do not dispatch publication just
to test workflow dispatch. Runner migration removes workflow label variables; after green CI, delete unused repository variables and unregister
repository runners if present. Do not uninstall machine services or delete their files; organization-provided runners require their owner.


A test binary never touches the developer's real `~/.persona`: the packages that start Persona processes (`internal/hub`, `internal/assistant`) run through `testhome.Main` in their `TestMain`, with `HOME` set to an empty directory and `PERSONA_HOME` unset, and the run fails if anything created `<HOME>/.persona`. A test that needs state sets `PERSONA_HOME` (`t.Setenv`) to a directory of its own, as the two assistant-process tests in `internal/hub` do. A test started by hand with `go test` used to rewrite the real hub's assistant `manager.json` and `secret.key` (the next run's assistant then never came up); `scripts/test/test-server.sh` runs with a throw-away `HOME` already.

Server packages with database tests call `dbtest.Main` in their `TestMain`: the binary gets a database of its own next to `PERSONA_TEST_DATABASE_URL` and drops it at the end (`docs/lessons/workflow.md`). The databases `dbtest` makes commit asynchronously (`synchronous_commit = off`): a commit is visible to other sessions at once, and only a crash of that Postgres could lose the last ones, so a migration does not wait for a WAL flush per transaction. In `internal/db`, a test that needs a new database with the current schema calls `migratedDatabase(t, sets)`, which copies a template the binary migrated once per set (`dbtest.Template`, `dbtest.Clone`); a test that starts from an older state uses `freshDatabase` and migrates. `open(t)` and `db.TruncateForTest` empty everything their `TRUNCATE ... CASCADE` reaches, but truncate only the tables that have a row (`docs/lessons/testing.md`, "Test database setup"). The CI Postgres and hub databases keep their own settings.

## Fast local checks (`scripts/check-fast.sh`, #2849)

Runs only relevant second-scale checks based on git changes, with independent checks in parallel; useful before committing:

```sh
./scripts/check-fast.sh                  # compare changes from git merge-base HEAD <default branch> to worktree, select by path
./scripts/check-fast.sh --base main      # choose a comparison baseline
./scripts/check-fast.sh --all            # all fast local checks except full iOS/macOS and server Postgres tests
```

Without `--base`, the base is the clone's default branch, read from local refs only (it works offline): `origin/HEAD` if it points at a commit, else `origin/main`, else `origin/master`. With none of them it stops with exit 2 and says how to fix it (`git fetch origin` then `git remote set-head origin --auto`, or `--base <ref>`).

`--all` also runs all `scripts/**/test_*.py` like CI’s Scripts tests job (the same `find`; it skips only tests already covered by dedicated checks: `test_external_service_refs.py`, `test_sdk_bundle.py`, `test_extension_schema.py`, and `scripts/app/test_android_*`, see `script_test_add`; update this skip list when renaming or removing dedicated checks). They run sequentially in one check, each suite from its own directory with its own `CHECK_FAST_TIMEOUT_OTHER`. One failure does not stop the rest; the final summary lists failed and timed-out files. A `test_*.py` that never mentions `unittest` is a verification script, not a suite: like CI (`scripts/ci/python-tests.py`), check-fast prints `SKIP <path> (not a unittest suite)` for it instead of running it, in diff mode too (`python -m unittest` runs no tests there and exits 5, "NO TESTS RAN", from Python 3.12); `scripts/test/test_check_fast.py` compares the `--all` selection with CI's.

Path mapping:

| Changed path | Checks |
| --- | --- |
| `web/**` | `npm ci` (only if `node_modules` missing or `package.json` / `package-lock.json` changed) + `npm run check:deps` (fails, naming the packages and saying to run `npm ci`, when an installed direct dependency isn't the version `package-lock.json` pins: a lock bump merged from `main` leaves the old packages in place, #4776) + `npm run typecheck` (incremental, state kept in web's `node_modules/.cache/tsc/`: a second run only checks what changed, and a type error fails every run) + `npm test` (vitest run) |
| `schema/extension/**`, `spec/fixtures/injected/**`, `spec/fixtures/sync/**`, `scripts/mock/inbox_events.json` | In addition to other checks in this table, the `web/**` checks: `web/src` and its vitest suite read these files. They are the inputs outside `web/` of the web lane in `scripts/ci/paths.py`; `scripts/test/test_check_fast.py` fails when the two lists drift apart |
| `integrations/<pkg>/**` | Package `node --test test/*.test.{js,mjs}` (skip packages without tests) |
| `site/**` | `node --test site/worker.test.mjs` |
| `sdk/ts/**` | `bun test` (skip with reason if bun absent) |
| `sdk/ts/src/**` | In addition to the `sdk/ts/**` check, `go test ./internal/extension/denohost/...`: the trusted Deno adapter `server/internal/extension/denohost/adapter.mjs` bundles these sources, and `TestTrustedAdapterMatchesOfficialSDK` fails while the committed one is stale (#4221; regenerate it with `scripts/gen/deno_adapter.sh` and the Bun version `.github/workflows/server-test.yml` pins). Skipped with a reason when bun is absent. CI's server lane runs on these paths too (`scripts/ci/paths.py`) |
| `sdk/go/**` | `go test ./...` in sdk/go module |
| `server/internal/foo/**`, `server/cmd/bar/**`, `server/plugins/baz/**` | Affected packages, e.g. `go test ./internal/foo/...`; unmappable server changes fall back to `go build ./...` |
| `server/**`, `sdk/go/**` | In addition to other checks in this table, the server's repo-wide guards (`job_go_guards` in `scripts/test/check-fast.sh`): `go test` on `internal/lazyre`, `internal/archguard`, `internal/tzguard` and `plugins`, and the named guard tests of `internal/hub` and `internal/assistant`. They read other packages' source, `go list` output, the migrations or `sdk/go`, so they fail on code outside their own package (#1936). The list is curated: not every `*guard*_test.go` is repo-wide. `--all` runs them within `go test ./...` |
| `server/internal/db/migrations/**`, `scripts/ci/migration_freeze.py` | `python3 scripts/ci/migration_freeze.py`: a migration already on the base cannot be edited, deleted or renamed (comment-only and whitespace-only edits pass; a new file may be added). A rename out of the directory counts. `--all` runs it when the script is present |
| `server/**/*_test.go`, `sdk/go/**/*_test.go`, `scripts/ci/sleep_ratchet.py`, `scripts/ci/sleep_baseline.json` | `python3 scripts/ci/sleep_ratchet.py`: the number of `time.Sleep` calls in each Go test file may stay or fall, never rise (comments, string literals and a `// sleep-ok: <reason>` line do not count; a new file starts at zero). `--update` only lowers the baseline. A rename out of those trees counts. `--all` runs it when the script is present |
| `spec/fixtures/**`, `schema/extension/**`, `integrations/**`, `docs/<file>`, `scripts/test/server_test_env.py` | In addition to other checks in this table, `go test ./internal/<package>/...` for server packages reading these files (repository inputs covered by `server-test.yml` filters). At runtime, `git grep` finds packages referencing the path in `server/**/*_test.go` (strings or `filepath.Join` components; `//` comment lines excluded), so new readers need no script changes; other `spec/` files remain unmapped |
| `android/**`, `scripts/app/*android*` (including both `test_android_*` self-tests) | `scripts/app/android_i18n_check.py` and Android checker unittests (i18n, token baseline; no Gradle build) |
| `.github/**` | `scripts/test/pr_needs_issue_test.mjs` |
| `schema/**` | `scripts/test/test_extension_schema.py`, `scripts/test/test_sdk_bundle.py`, plus `go test ./...` in sdk/go (the same three checks as CI's Extension schema job) |
| `scripts/test/test_external_service_refs.py`, `scripts/{gen,test}/sdk_bundle*` | The scripts' own tests |
| `scripts/**` | Select second-scale privacy / PR / schema / refs / bundle / i18n checks by path; when uncertain run PR and schema. Also run changed `scripts/<subdirectory>/test_*.py`, and `test_<name>.py` for changed `<name>.py` / `<name>.sh` scripts (`-`→`_`; `.sh` also checks `test_<name>_sh.py`), from the test directory like CI (#3968). `scripts/hooks/**` also runs `scripts/test/test_privacy_check.py` (it runs the hooks in its temp repositories); `scripts/privacy_check.py` also runs `scripts/release/test_check_bundle_privacy.py` (it imports `privacy_check`) |
| `docs/**`, any `*.md` | `scripts/test/test_doc_paths.py`, `scripts/test/test_doc_go_paths.py` (quoted paths and CLAUDE.md documentation index), `scripts/test/test_doc_sections.py` (section names quoted after a doc path match a heading, a **bold** label, an `<a id>` or a list item's opening phrase of that doc; `scripts/test/doc_sections_known.txt` lists the ones left for now and only shrinks), `scripts/test/test_doc_wire.py` (protocol.md and extensions.md list every WebSocket type, feature flag, extension point and manifest field the code has) |
| `server/internal/{proto,hub,extension}/**`, `schema/extension/manifest.schema.json` | Also `scripts/test/test_doc_wire.py`: these hold the code side of the lists it compares with the docs |
| `server/**`, `web/src/**`, `web/e2e/**`, `scripts/**`, `sdk/**`, `apple/**`, `android/**`, `integrations/**`, `spec/**`, `schema/**` | Also `scripts/test/test_doc_sections.py`: most of the section names it checks are in code comments there |
| `apple/**` | `scripts/test/test_apple_l10n.py` (complete String Catalogs and Swift Chinese literals included in catalogs); iOS / macOS builds and tests need a Mac and are reported as skipped |
| `deploy/docker/**`, `docs/operations/docker.md`, root `Dockerfile` | `scripts/test/test_docker_entrypoint.py` (also checks root `Dockerfile`’s `EXPOSE`) |
| Other paths | No code checks; list unmapped paths individually |

Any changes always run `scripts/privacy_check.py` first: local-only, no network, a few seconds. Every check has a timeout (npm ci 600 s, vitest 300 s, one go test group 300 s, others 120 s; override with `CHECK_FAST_TIMEOUT_NPM_CI` / `CHECK_FAST_TIMEOUT_WEB_TEST` / `CHECK_FAST_TIMEOUT_GO_TEST` / `CHECK_FAST_TIMEOUT_OTHER`). Success/failure/timeout each prints a result and duration; the final summary lists passed/skipped (with reasons)/failed/timed-out checks and log directory. Exit: 0 if all pass or none needed; 1 for failure; 124 for timeout without failures (timeouts aren't passes; run full checks to confirm). Ctrl-C, SIGTERM and SIGHUP, sent to the script or to its whole process group (a closed terminal or tmux pane), stop every running check (and its child processes) before exiting with 130, 143 or 129, and keep the log directory. Missing toolchains skip relevant checks with reasons by default; under `--all`, missing core tools (node ≥ 22, go, python3) fail; bun is always optional. Requires GNU `timeout` (coreutils on macOS).

Not covered:

- iOS / macOS / watchOS builds, simulator screenshots and UI tests still run on Mac with `scripts/app/sim.sh`, `scripts/app/ui-test.sh`; PersonaKit uses `swift test`.
- Full server tests against real Postgres still use `scripts/test/test-server.sh` (`internal/db`, etc. skip without `PERSONA_TEST_DATABASE_URL`; full `internal/node`, `internal/hub`, etc. assume a hub-machine environment. Fast-check timeouts aren't failures; test-server.sh is authoritative).
- Android Gradle builds/screenshots use `scripts/app/android.sh`.

**PersonaKit** (`apple/Packages/PersonaKit`, #2095): non-UI App logic moves into this package. Tests run with `swift test` on Mac: about 10 seconds cold,
2 seconds after one file change (the same tests through `PersonaTests` took about 140 seconds cold, 45 after one file change, building the whole App and starting a simulator). Run `swift test` before merging package changes;
it isn't in `PersonaTests`; the Apple CI script runs its SwiftPM tests separately. iOS / Mac / visionOS Apps depend on it, watch/widgets do not.

- Currently included: `Notebook`, `HTMLResources`, `ChatFollow`, `PathRefs` (with `PathRef`, `PathLinkURL`, `PathMarkdown`); batch 2 `DocumentText` (with `TextCompare`), `TableParsing` (`ZipArchive`, `XLSXReader`, `DelimitedParser`, `TableSheet`), `MathMarkdown`, `Links`; pure functions extracted from views: `ConversationTitle`, `DirectoryPath`, `StreamChunks`, `StreamRepair`, `MarkdownHTML`, `APIErrorInfo`, `ZoomMath`, `LarkChangeGate` (views now just call them); batch 3 `AgentSyncPacing` (`AgentSync` fetch timing; `AgentSync.heldUntil` / `workingGap` delegate to it), `ConnectorPrompt` (with `cannotDrawNote`); `VoiceRoute` (the voice call's audio route, #3015); `LineDiff` (the chat's Edit / Write diff, #2915).
  Don't move `OpRetry`, `BrowserCookies`, `MachineMetrics` yet: `Shared/APIClient.swift` uses them, so moving would make the watch depend on this package (watchOS 10); assess separately. `Links` and `ConversationTitle` were in `apple/Shared`, but unused by watch/widgets, so moving them didn't add a watch dependency.
- Moved types and App-used members must be `public` (structs need `public init`); App files using them add `import PersonaKit`. Package tests still use `@testable import PersonaKit`.
- Assertions needing App types (views, `FilePaths`) stay in `PersonaTests`, also adding `import PersonaKit`.
- Package code must build with Foundation only: no SwiftUI / UIKit imports, App `Theme` or `AppModel`. Design-system source checks (`TokenRatchetTests`, etc.) don't scan it.
- On a Mac still on macOS 15 the test bundle builds but does not load (`Symbol not found … built for macOS 26.0 which is newer than running OS`: the package targets macOS 26). Build it for the running system in a separate build folder: `swift test --scratch-path /tmp/personakit-macos15 -Xswiftc -target -Xswiftc arm64-apple-macosx15.0`.

Transcript parsing uses real-sample golden tests (`server/internal/transcript/testdata/`, `fixtures_test.go`): each sample's parse result,
"incremental read while writing = one full read", and "backward paging has no gaps/duplicates". After parsing changes run `go test ./internal/transcript -run Fixture -update` and review the golden diff.
Before adding samples, redact with `scripts/diag/redact_transcript.py`, then manually review (`testdata/README.md`). After Claude Code / Codex upgrades, run
`scripts/diag/transcript_stats.py` to find new types the parser doesn't recognize.

API smoke tests: `curl -H "Authorization: Bearer $TOKEN" …/api/v1/machines`, `…/api/v1/sessions?machine_id=<machine>`.

## End-to-end (verified 2026-10-24)

| Scenario | Method | Result |
| --- | --- | --- |
| Create session from phone | `POST /api/v1/sessions`, launch via a launcher in `~/persona-test` on the hub machine | Directory trust accepted automatically; first message delivered |
| Questions | First message asks claude to call AskUserQuestion (one question / two, including multi-select) | question interaction appears on hub; after `resolve`, claude receives answers and continues |
| Plan approval | ExitPlanMode → deny + feedback → revise plan → allow + setMode | Both branches correct (`docs/integrations/claude-integration.md`) |
| Phone tools | claude on hub machine calls `mcp__persona__phone_device_info` | node → hub → simulator App returns iPhone 17 Pro / iOS 26.5 |
| Live stream | App opens a working session on another Mac | Tool steps append live; status bar shows spinner line |
| Watch | Watch simulator `-pairURL` | "Needs you" (waiting Agents + Inbox items), running sessions; `-openInboxFirst YES` opens first item, `-showWatchPet YES` shows the Milk Frog as if chosen on the phone (mock with `MOCK_PERSONAS=builtin`), `-showPetState <state>` forces its state, `-openWatchItem pet` opens its page, `-openWatchItem complication` shows the watch face's frog in every family (a simulator cannot add a complication from the command line), `-openWatchItem question\|plan\|permission\|screen\|session` opens first request of that type / first running session (`running`: hides "Needs you" to screenshot running rows; `inbox`: only Inbox rows under "Needs you"); `-showWatchMissing Work,Home` shows groups missing watch credentials (`""` is default group; no `-pairURL` opens "No watch credentials yet", #1185) |

## UI checklist (inspect screenshots after every UI change)

- Chat list: light / dark; pending interactions, no sessions, hub unreachable.
- Chat: long Markdown (tables, code blocks), collapsed tool groups, diffs, to-dos, images; Working bar; question / plan / permission / terminal panels.
- New chat: machine cards (offline grayed out), recent directories, directory browser, launcher search.
- Machines, mail (empty/list/detail/compose/add account), Settings, phone-data permissions.
- Watch: list, interaction details, Inbox details (AI reply), session details.

Simulator limits: computer-use can't click simulators while the screen is locked; use DEBUG launch arguments to open pages directly. Push, real HealthKit data and WatchConnectivity sync need devices.

## Android screenshots

`./scripts/app/android.sh shots -s emulator-<port> --out <directory>` installs a separate test package on a dedicated emulator and auto-pairs to anonymized `scripts/mock/mock_hub.py`,
screenshotting the four tabs and main pages in light and dark (`docs/android/android.md` ("Batch screenshots with a mock hub"); code-only refactors use that section's before/after pixel comparison with `scripts/app/android_shots_diff.py`).
UI changes also need all three styles (Glass / Morning / Board, `docs/android/android-design.md`); tablet/foldable screenshots in `docs/lessons/android.md`.
Inspect gestures, keyboards, notifications and real networking on devices or emulators.

## Mock-data screenshots (README / promotional images)

Screenshots must contain no real business data. `scripts/mock/mock_hub.py` returns only made-up data (three machines, Agents in several projects, assistant chat,
one question, one plan approval, Lark + mail Inbox), enough for main pages.

Real-hub Inbox end-to-end tests (triage, event synthesis, AI twin decisions) use `scripts/mock/mock_model.py`: a fake Anthropic Messages endpoint (`POST /v1/messages`),
returning fixed JSON according to prompts (event synthesis merges the batch into the first open item in the input, or creates one; question marks count as Needs you). `POST /_mode` switches to failure, exhausted quota,
or invalid output; `GET /_calls` inspects received calls. Point the manager's `manager.json` `inbox.triage_ai` (also used by event synthesis) and `judge_ai` at it; usage is at the file top, tests in `scripts/mock/test_mock_model.py`.

```sh
python3 scripts/mock/mock_hub.py &                                  # 127.0.0.1:18760
SIM=$(scripts/app/sim-boot.sh "Persona Shots <issue number>")          # create/slim boot ("Simulators"), normal boot if simslim absent
xcrun simctl status_bar $SIM override --time "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" --batteryState charged --batteryLevel 100 --wifiBars 3 --cellularBars 4
xcrun simctl launch --terminate-running-process $SIM <bundle id> \
  -pairURL "persona://pair?url=http%3A%2F%2F127.0.0.1%3A18760&token=mock" [-tab dev|mail] [-openSession <machine>/<session>]
```

- **Create a new simulator**, not one paired to a real hub: App disk caches (chats/Inbox) can leak real data into screenshots.
- Mock item ids are fixed; after changing mock data or ids, uninstall/reinstall the App or old caches merge with new data and duplicate steps.
- Agent chat work is collapsed to one line by default; `-openAllWork YES` expands it. `-tab` has no chat tab and stays on the last tab:
  uninstall/reinstall to photograph the chat tab (the fresh-install default).
- Home greeting follows the simulator's actual time ("Good evening"); status-bar time should be current too, not fixed at 9:41.
- Dark: `xcrun simctl ui $SIM appearance dark`.
- `MOCK_TOPBAR=1|3|long`: sub-Agents of the weather-widget session for the chat header's status line (`docs/archive/apple/plan-chat-topbar.md`): `1` one running and one ended (`-openAgent tb1` reads it, with a conversation); `3` three running (`tb3` newest); `long` a sub-Agent title of about 40 characters and a very long tab name (truncation). Add `-holdChromeOpen YES` so the header does not fold before the screenshot.
- An agent's edits as diffs (#2915, the chat's `DiffView` in `docs/apple/ios.md`): `MOCK_EDIT_DIFF=1` adds MacBook's "Rain hours and the dry threshold" (`macbook/a1f3c2d4-0000-4000-8000-0000000c2915`), one turn with an Edit that changes a word in two lines of a block, a MultiEdit of two pieces, a Write and an Edit of a 56-line switch whose unchanged middle folds (Show All 58 Lines), so the BeautifulUI style's chips read "Edit 4 files +20 −6" over four changed files.
- Tool names in words (#2986, `ToolNaming` in `apple/Persona/Views/Chat/ToolNaming.swift`): `MOCK_TOOL_NAMES=1` adds MacBook's "Why the CSV export times out" (`macbook/a1f3c2d4-0000-4000-8000-0000000c2986`), one turn that calls ToolSearch, TaskCreate, TaskUpdate (started, then completed), NotebookEdit, Monitor, SendMessage, ScheduleWakeup, another server's MCP tool (`mcp__github__create_issue`, "GitHub · Create issue") and a tool no table knows with nested arguments (`RenderChart`). The MCP call and the unknown one also have a chat of their own (`…0c2987`, `…0c2988`): open the work (`-openAllWork YES`) and tap the row to see its arguments as a key/value list with Raw Input under it. `…0c2989` is a review whose ReportFindings lists two findings, each with its file and line. `…0c298a` is the Monitor alone, its Background badge on the first screen at the largest text size. `-showAsUnread YES` lands the long turn at its first step.
  The first three also end chats of their own (`…c2916` Edit, `…c2917` MultiEdit, `…c2918` Write), so `-openSession macbook/<id> -openAllWork YES -showToolDetails YES` shows each diff without scrolling; `scripts/app/personactl.py tap --label-prefix "Show All"` opens the long one.
- A reply's Markdown as written (#3863, `CJKEmphasis` in PersonaKit and `MarkdownTable.swift`): `MOCK_REPLY_MARKDOWN=1` adds MacBook's "导出耗时整理" (`macbook/a1f3c2d4-0000-4000-8000-0000000c3863`) and puts the same turn at the end of Persona's conversation: a table whose delimiter row aligns its columns (the seconds right-aligned so the decimals line up, the result centered), headings at levels 4 to 6, and bold labels next to full-width punctuation (`**注意：**后文`, `**「必须」**`), which render bold with no asterisks left over.
- Tool outcomes (#4303, `ToolOutcomeText` in `apple/Persona/Views/Chat/ToolOutcomeViews.swift`): `MOCK_TOOL_OUTCOMES=1` adds MacBook's "Why the last export batch goes missing" (`macbook/a1f3c2d4-0000-4000-8000-0000000c4303`), one turn whose tool results carry the node's `outcome`: a test run that failed with exit code 1 and an output the page cut (its last lines in `tail`), an edit the user denied, a write refused before it ran, a command the user interrupted and one that timed out and went on in the background. Only the failed run is red; the others say in gray what stopped them. The failed run and the denied edit also end chats of their own (`…c4304`, `…c4305`): `-openSession macbook/<id> -openAllWork YES -showToolDetails YES` shows "Exit code 1" with "End of output" under the cut output, or "Denied".
- A file tool's change on its permission card (#4621, `PermissionDiff` in `apple/Persona/Views/Interactions/PermissionDiff.swift`): `MOCK_PERMISSION_DIFF=1` adds two MacBook chats that wait on a Claude Code permission card, as a hub sends it: "Keep the last export batch" (`macbook/a1f3c2d4-0000-4000-8000-0000000c4621`), an Edit whose diff is longer than the card shows (Show All 15 Lines), and "Test the last export batch" (`…c4622`), a Write of a short test file. `-openSession macbook/<id>` shows the card under the chat.
- Cron scheduled tasks (#4399, `docs/assistant/assistant.md` "Scheduled tasks"): `MOCK_CRON=1` adds the `task_cron` feature and a cron task (every 30 minutes in work hours on weekdays, `rt_cron`), and answers `GET /api/v1/assistant/tasks/cron` for the web editor's examples (anything else gets the hub's kind of refusal). iOS: `-tab assistant -openAssistantPage routines` shows it in the list, `-openAssistantPage cronTask` opens it. Web: `node web/e2e/task-cron.mjs` (`docs/web/web.md`).
- Notification settings that can't load (#1860): `MOCK_PREFS_FAIL=1` makes `GET /api/v1/devices/<id>/prefs` answer 503; `-openNotificationSettings YES` shows "Couldn't load: …" with Retry while the system-settings buttons stay enabled. `GET /mock/prefs_fail?on=0` lets the next read through (Retry, or switching back to the app).
- herdr stopped (#1916): `MOCK_HERDR_STOPPED=macbook` stops classic herdr for mock MacBook; Agents shows "MacBook's herdr isn't running" and "Start" at the top.
  `MOCK_HERDR_START=slow|fail|stale`: startup takes 6 seconds / fails (node's reason) / starts but `GET` still reports stopped (old hub). `GET /mock/herdr?stopped=…&start=…` changes it live,
  `/mock/reset` restores environment settings; `starts` counts requests per machine. `-showHerdrStart YES` auto-taps Start once on the first row (capture "Starting…" and failure).
  UI suite `HerdrNoticeTests` (`TEST_RUNNER_PERSONA_MOCK_HUB`, screenshots in `PERSONA_SHOTS`).
- Personal agents' Remove from list (#138): `MOCK_PERSONAL_AGENTS=1` lists six made-up personal agent conversations and keeps hide / unhide / `GET /agents/hidden` in memory (each write sends `agents`); `GET /mock/personal-agents?reset=1` brings every one back, and `?state=partial` (#3216) keeps them listed while a second source fails (`?state=partial_setup`: isn't set up), for the notice above the list. Preferences are kept in memory too; `?prefs_fail=500` refuses their PUT (#1880), and `?reset=1` clears both the saved fixture and the failure. The page is reached from the shell's **Personal Agents ›** pill (regular-width iPad: toolbar button). UI suite `PersonalAgentRemoveTests` (`TEST_RUNNER_PERSONA_PERSONAL_HUB`, screenshots in `PERSONA_SHOTS`, `PERSONA_SHOTS_SUFFIX` appended to each name).
- Computer control (docs/extensions/computer-use.md): `MOCK_COMPUTER=ready|permissions|both|locked|missing` enables extension `cua` and mock MacBook (ready / missing Screen Recording / missing both permissions / locked / not installed: "Install Cua Driver" advances through download, verification, disabling analytics, authorization and Allow, every `MOCK_COMPUTER_STEP` seconds, default 2). Add `MOCK_WEB=web/dist` (first `npm run build`) to host the web VNC page and forward MacBook's 5900 to this Mac's own Screen Sharing (UI tests only with `TEST_RUNNER_PERSONA_COMPUTER_SCREEN=1`); Mac Studio remains disabled. `=off` enables the extension but no machines. With `MOCK_EXTENSIONS=1`, the extension list includes "Computer control (Cua)"; "Computers it can control" syncs bidirectionally with each Mac's `computer.enabled` (`-tab settings -openExtension cua`). `-tab settings -openMachines YES -openMachineDetail macbook` opens MacBook's machine page. `MOCK_COMPUTER_OPERATING=1` adds a running operation to the assistant's "Calculate 6×7" and the Agent widget chat, showing "Operating MacBook · …" above input (`ScreenDrawerTests`). `MOCK_DEVICE_TOKEN=jd_…` (same `TEST_RUNNER_PERSONA_DEVICE_TOKEN` in UI tests, `UITestToken`) makes mock `/api/` accept only that token (`Authorization` or `?token=`), returning 401 invalid token otherwise, like hub (#1498: VNC page got a double-encoded token). `MOCK_COMPUTER_APPS=1` gives MacBook two assistant-approved apps (Calculator, entire screen), removable on its machine page (`ComputerSectionTests.testAllowedAppsCanBeTakenBack`, `TEST_RUNNER_PERSONA_COMPUTER_APPS=1`). "Don't auto-lock while the extension is on": machine toggle asks first (`ComputerSectionTests.testNoAutoLockAsksFirst`), and per-machine extension toggles honor `x-confirm` (`ExtensionMachineSwitchesTests.testAListWithAConfirmAsksFirst`, `MOCK_EXTENSIONS=1`); mock syncs extension-page `no_auto_lock` to each `computer.no_auto_lock`. `MOCK_SCREEN=fake` replaces forwarding to real Screen Sharing with a mock server (macOS-like ARD login first when openssl exists, then VeNCrypt Plain, account `me`, password `mock-pass`; `GET /mock/screen-password?p=…` changes password, `&clear=1` clears recorded keys; correct password+Return unlocks mock MacBook). It draws one desktop frame, supplies ZRLE when requested, records pointers/clicks/clipboard (node `GET/POST …/computer/clipboard` shares it); `?clip=…` sends clipboard to clients, `&slow=S` delays S seconds (miniwindow "Connecting…"), `&down=1` drops current and future connections (`0` restores). `MOCK_SCREEN_ANIMATE=1` moves a square at about 30 fps (`ScreenPasswordTests`, `VNCTests`, native client; UI tests use `TEST_RUNNER_PERSONA_FAKE_SCREEN=1`). `MOCK_SCREEN_DISPLAYS=2` gives the mock Mac two 480×300 displays side by side (a 960×300 picture; display 2 green, each numbered) and has its Screen Sharing list them (ExtendedDesktopSize); `=2:node` lists none there and puts them in MacBook's computer status as `displays` instead, as a node reads them (`VNCTests.testOneDisplay` with `TEST_RUNNER_PERSONA_SCREEN_DISPLAYS=2`). Simulators have no PiP; VNC falls back to a full page, miniwindow needs a device.
- File browser upload / Save to Files (#4858, `docs/protocol/protocol.md` "Changing files"): `MOCK_FILE_OPS=1` makes the mock take `dest=dir` (files go into the mock tree, a taken name is 409 `exists`, `conflict=keep|replace` as the node does, outside the home `read_only`) and the rename / move / trash / restore routes. `-tab settings -openMachines YES -openMachineDetail macbook -openMachineFiles YES` opens the MacBook's Files directly; `-showFolderUpload <name>` uploads a 24 MB fake file into the current folder (`MOCK_UPLOAD_KBPS=300` makes the progress visible); an existing name (`README.md` in session `macbook/a1f3c2d4-0000-4000-8000-000000000002 -openPane files`) asks first. UI tests `FolderUploadTests` and `FolderEditTests` (rename → move → select → trash → Undo in the MacBook's home; on an iPad also `-showFilesDrop YES`, the outline of a drag over the folder) with `TEST_RUNNER_PERSONA_FILES_HUB`.
- Temporary-code pairing (#2766): `MOCK_PAIR_TOKEN=jp_…` behaves like `personad hub pair`: usable everywhere before `POST /api/v1/auth/tokens` exchange, then for `MOCK_PAIR_GRACE` seconds (default 10 instead of hub's 2 minutes); a second exchange returns 401. Exchanged device tokens (`jd_mock_<n>`) remain valid; every 401 logs a line. Paired sockets close after grace like hub; `ws open host=… token=…` identifies handshake tokens. `MOCK_PAIR_TRADE_DELAY=<seconds>` delays exchange, `MOCK_PAIR_DROP=<seconds>` drops the first temporary-token socket once. Add `MOCK_HUB_NAME` and `MOCK_MESH_PEERS` with `center: true`, address `http://localhost:<port>`, to reconnect through the group's center (another address). `-openWelcome YES -openPairLink 'persona://pair?url=http://127.0.0.1:<port>&token=jp_…'` pairs like scanning (`-pairURL` doesn't exchange device tokens; `simctl openurl` prompts system confirmation); `-openPairLinkTimes 2` processes it again after a few seconds. Correct only if no 401 after grace and title isn't "Connecting…".
- Cloudflare Access (#3502): `MOCK_CF_ACCESS=<client id>:<client secret>` puts the mock behind Access: `/api/` requests without those two `CF-Access-Client-*` headers get a `302` to an Access login, wrong ones Access's HTML `403` (each logged). iOS: `-seedCloudflareAccess id:secret` gives the first `-pairURL` hub that token (`-` clears it), `-openCloudflareAccess YES` opens Settings › hub › Cloudflare Access; on the pairing sheet (`-openWelcome YES -openPairSheet other`) `-showPairAccess YES` opens the Cloudflare Access fields (filled from `-seedCloudflareAccess`), and `-showPairAccess '<pairing link>'` also pairs at once, showing the Access error when the token is missing or wrong.
- Browser (docs/plans/plan-ego-browser.md): `MOCK_BROWSER=missing|setup|ready` enables `ego` and MacBook (not installed / installed without first setup / ready, analytics on; `MOCK_BROWSER_ANALYTICS=off` disables analytics); Mac Studio disabled/uninstalled. "Install ego lite" / "Open ego lite" advance every `MOCK_COMPUTER_STEP` seconds. With `MOCK_EXTENSIONS=1`, list includes "Browser (ego lite)" (`-tab settings -openExtension ego`).
- Phone control (docs/extensions/iphone.md): `MOCK_IPHONE=one|several|none|pair|off` turns on the extension `iphone` with MacBook switched on. Each Mac lists the iPhones it reaches. `one`: one of them is this simulator, matched on `MOCK_IPHONE_MODEL` (`xcrun simctl getenv <udid> SIMULATOR_MODEL_IDENTIFIER`, default `iPhone18,1`) and `MOCK_IPHONE_IOS` (the runtime's version, default `26.1`); `several`: two look alike and the user picks; `none`: none is this iPhone; `pair`: this iPhone is plugged into MacBook but not paired yet, and Pair runs through Trust to paired in `2 × MOCK_COMPUTER_STEP` seconds; `off`: the extension is off, so no section appears anywhere. `MOCK_IPHONE_CONSENT=1` starts with this iPhone consented; `MOCK_IPHONE_ASK=1` adds an `iphone_do` approval card to Persona's main conversation (`-openSession server/b2e4d6f8-0000-4000-8000-000000000001`). The computer page's section: `-tab settings -openMachines YES -openMachineDetail macbook`; This iPhone's: `-tab settings -openConnectorDevice YES`. Unit tests: `IPhoneControlTests`.
- Cards on no session (#4059): `MOCK_SCRIPT_ASK=1` adds a question card with an empty `session_id`, like `personad ask` run outside herdr;
  Agents' needs-you list and the inbox both list it, and tapping it opens the card in a sheet (`-tab mail -openInboxAsk ix_script_ask` opens it directly).
- Password card (`secret_run`, #1137, `docs/assistant/assistant.md` "Password card"): `MOCK_SECRET_RUN=pending|ran|withheld|long` puts it in Persona's main conversation: `pending` waits for the password (two commands on the Mac Studio); `ran` is answered, every command exited 0; `withheld` the same with the first command's output withheld (it held the password); `long` the second command failed with a long output (the app reads the whole result).
  UI suite `SecretRunUITests` (`TEST_RUNNER_PERSONA_SECRET_RUN_HUB=<mock URL>` with `MOCK_SECRET_RUN=pending`): the commands verbatim, Run only once a password is typed, the answer carrying exactly that password.
- Questions the assistant sees first (web #544): `MOCK_DELEGATE=1` marks `ix_tests` and `ix_release_delegate` ("整理发布说明") `delegating`, gives `ix_review_delegate` ("检查 PR #42 的改动") `delegate` context recommending one of its choices, and adds one delegated answer to `GET /interactions/history` in the hub's row shape (`{interaction, status, resolution, resolved_at}`).
  `GET /mock/delegate?answer=<id>` answers a looking card as the assistant (`interaction_resolved` with `answered_by` / `reason` / `answers`, then into history), `?handover=<id>` hands it to you (sent again without `delegating`, with context).
  `POST /interactions/{id}/reanswer` answers like the hub (409 not answered by the assistant, 400 no answers) and broadcasts `interaction_reanswered`; `GET /mock/reanswered` shows what arrived. There is no reset: restart the mock between runs.
  On iOS / Mac, `-showAgentInfo YES` on a session lists its delegated answers in Session Info, and `-openChangeAnswer YES` opens Change Answer on the first answered card.
- Unified main-chat entry (docs/assistant/unified-entry.md): `MOCK_TASKS=1` supplies a started plan, three tasks working/waiting/done, one Agent-start failure (red card and receipt, #1077),
  answered questions, receipts, direct task messages and a final unstarted plan. `MOCK_TASKS_AT=<minutes>` cuts newer items so earlier portions sit at the bottom without scrolling.
  `MOCK_TASKS_NO_ASKS=1` suppresses task-Agent questions above input. `MOCK_TASKS_DOCK=2` keeps two working tasks (two dock capsules, then two manual Agents); `=pair` one task + one manual Agent waiting for you; `=many` adds four tasks (dock wider than screen, "All" fixed left); `=multi` keeps only "Fix widget landscape crash" with two Agents (task-title ⌄ switch, `TaskSheetHeaderTests` with `TEST_RUNNER_PERSONA_TASK_MULTI=1`); `=long` keeps two overlong titles (parallel capsules, no overlapping truncation; `simctl ui <udid> content_size extra-extra-extra-large`, #3010). Default: four unfinished tasks + two manual Agents (#1113). `-showTaskDetail <to-do id>` (e.g. `todo_task_widget`) opens details; `-showTaskBoard YES` opens board.
  Its newest line is a morning routine's `brief` item (#2320): a waiting card that names its chat and one that doesn't, failed and done rows past what is listed, a deployed to-do and a decision to verify, two questions that can wait and two to-dos in progress. `TEST_RUNNER_PERSONA_MOCK_HUB=<address> xcodebuild test … -only-testing:PersonaUITests/BriefCardUITests` (with `MOCK_TASKS_NO_ASKS=1`) checks the iPhone's folded rows, Accept, Handled and Open; `TEST_RUNNER_PERSONA_SHOTS` / `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` save the card folded, unfolded and answered.
  Hub-injected messages (#2358, `docs/protocol/protocol.md` ("Messages injected by Persona")): `MOCK_INJECTED=1` adds an unmarked task notification (recognized by text) and a marked scheduled task below receipts;
  `MOCK_TASKS=1 MOCK_INJECTED=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASKS_AT=14` puts them at the bottom. `InjectedLineTests` (`PERSONA_SHOTS` captures both lines and expanded scheduled task) opens scheduled task, then notification to open task.
  Watch: add `MOCK_INJECTED_WATCH=1` (working main chat listed first), `-openWatchItem session` opens it.
  New conversation layout (#1666, `docs/apple/ios.md` "New conversation layout"): `MOCK_TASKS=1 MOCK_TASKS_REPORTS=1 MOCK_INJECTED=1 MOCK_EXCHANGES=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASKS_AT=5.5` puts a long Persona → task message, the task's long report (a link in its words, two evidence links) and a supervision digest at the bottom.
  `NewChatLayoutTests` checks the headers, folding and Expand, no evidence list, baseline conversation presentation with `-ui.newChatLayout NO`, a held slide along the right edge, and a quick swipe there that only scrolls the list (#2615); `PERSONA_SHOTS` saves folded / expanded / off / the navigator (`-showEdgeScrub 3`).
  Markdown in a task's words (#2363): add `MOCK_MARKDOWN=1` to put **bold**, `code` and a heading in that message, that report and the release notes receipt (`MOCK_TASKS_AT=19.9`); they render, with no raw marks, in either layout.
  A renamed task's receipt (#3036): `MOCK_TASK_RENAMED=1` renames the release notes task after its receipt was written; iOS / Mac and Android draw that receipt with the new name ("写本周发布说明"), not the one in the receipt (the web's assistant chats list tasks in the Progress tray, by the to-do's name, and draw no receipts).
  Task answer records (#1231): "Complete network-layer unit tests" has three full question/answer pairs; "Fix widget landscape crash" is an old summary-only record. `MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASKS_AT=23` puts them at bottom; `AnsweredLineTests` (`PERSONA_SHOTS`, collapsed/expanded) opens then collapses them.
  What Persona sent to its tasks' agents (#1542): `MOCK_TASKS=1 MOCK_TASKS_SENT=1 MOCK_TASKS_NO_ASKS=1` adds, at the bottom, a task card whose long hand-off it takes in ("1 instruction sent"), a rework to the widget task as a folded line, and the user's own word to the unit-test task as a bubble; `SentToAgentTests` (`PERSONA_SHOTS`: folded, card list open, line open). With `MOCK_TASKS_AT=1559` the bottom is yesterday's blog-migration card, now a line, with its hand-off as a folded line under it (#1668).
  The hub's notice line (#4305): `MOCK_TASKS=1 MOCK_TASKS_NOTICE=1 MOCK_TASKS_NO_ASKS=1` adds "电脑和模型接好了，定时任务恢复运行，这次是「早报」" as the conversation's last line, small and centered.
  What didn't go through overnight on the Today card (#4124): `MOCK_TODAY_FAILED=1` (with `MOCK_TASKS=1`) adds a task whose Agent didn't start, a failed scheduled run and a skipped one, with `failed_total` 5, so the row says how many in all and "2 more"; without it the card is as before.
  Message card states (#4797): `MOCK_TASKS_MESSAGE=filed` marks the card about the widget task as handled elsewhere (`filed_at`): no Send, the draft greyed out with a line saying it wasn't sent. `MOCK_TASKS_MESSAGE=sent` marks it as replied from the card (`sent_at`).
- Typing a model ID (#1644, `docs/assistant/model-config.md` "Typing a model ID"): `MOCK_CUSTOM_MODELS=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py` lists IDs typed before after the Claude Code / Codex lists (`custom: true`), takes Forget (`DELETE …/models/custom?model=`) and remembers a start's or a `…/modes` typed model; `=forgetfail` makes Forget answer 500 (the row comes back), `=empty` gives Claude Code an empty list ("Enter Model ID…"), `=fail` a 502 for every list; `GET /mock/custom_models` shows what is remembered (`?reset=1` puts the first IDs back). `TEST_RUNNER_PERSONA_CUSTOM_MODELS_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/CustomModelUITests` walks New Agent (typed IDs, Forget, the reason under a bad ID, the pick in the start), a running session's "Other Model…" (`POST …/modes`, then the menu ticking it with the ID once the session runs it) and the Persona chat's launcher menu (against `=1`), New Agent's "Enter Model ID…" beside a list that couldn't be read and its clear button (against `=fail`), the empty list (`=empty`) and a refused Forget (`=forgetfail`); each test skips the other modes. `TEST_RUNNER_PERSONA_SHOTS` / `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` save the screenshots. The mock lists `typed_models` in its features in every mode (the chat's "Other Model…" needs it). The web's: `node web/e2e/custom-model.mjs <web URL> --hub <mock URL> [--shots <dir>]` follows the mock's mode: New Agent, a session's popover and the chat's launcher menu (`=1`), a refused Forget in New Agent and the popover (`=forgetfail`), New Agent without a list (`=empty`, `=fail`); light and dark.
- Claude Code ultracode (#1307, `docs/apple/ios.md` "Claude Code ultracode"): `MOCK_ULTRACODE=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py` (the states are listed in the mock's help: on, queued, turned off by a resume, the node's reason, off, offline, Codex at `ultra`, Mac Studio's old Claude Code). `TEST_RUNNER_PERSONA_ULTRACODE_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/UltracodeUITests` checks the model menu's switch and its line, the marks on the model button and the Agents rows, the requests (`GET /mock/ultracode`; each test resets the states with `?reset=1`), the resume line's Turn Back On and ×, a refusal, and New Agent (off each time, grayed for a too-old Claude Code). `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save the screenshots; switch the simulator's appearance with `simctl ui`.
- Launcher health in New Agent (#1643, `docs/server/launcher-health.md` "In the App"): `MOCK_LAUNCHER_HEALTH=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py` gives every computer's launcher list `work_gw` blocked as not in the terminal and `team_gw` blocked on its key (claude checked fine),
  a start that runs `work_gw` fails with the hub's coded sentence, and `macbook/f1643000-0000-4000-8000-000000000001` is a chat that already failed so. `MOCK_LAUNCHER_HEALTH=default` also makes `work_gw` Model Settings' New Agent default.
  `MOCK_LANG=en|ja` writes the hub's start-failure sentence in that language (from the hub's own catalogs; default zh-Hans), for screenshots in that language. With `MOCK_TERMINAL_PANES=1` the computers have free shells for Run Agent Here.
  iOS / Mac: `-showNewChat YES -openLauncherPicker YES` opens the launcher list once the computer's launchers are in, `-showLauncherHelp <name>` that launcher's help over it (`-openLauncherDetails YES` with Details open); `-openSession macbook/f1643000-…` shows the failed start; with `MOCK_TERMINAL_PANES=1`, `-tab dev -showRunAgentHere macbook` opens Run Agent Here for that computer's first herdr space (`-showRunHereLauncher work_gw` with it picked, since a picker menu's items take no synthesized tap); `-tab dev -showPushAsk macbook` asks that space's Git ▸ Push question (#1883). With `MOCK_GIT_READ_FAIL=1`, `-openPane git -showGitReload YES` refreshes a session's Git tab and loads earlier commits once after the first read, both failing (#1889).
  Web: `node web/e2e/launcher-health.mjs <web URL> --shots <dir> [--locale en-US]` against the same mock (wide / 390 × light / dark: the grayed chips and their lines, How to fix, the chat's coded failure, and, with `MOCK_TERMINAL_PANES=1`, the Run agent here menu).
- MCP forms and links on iOS (#2322, `docs/apple/ios.md` "MCP forms and links"): `MOCK_ELICITATION=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py` puts a form and a link on the PR review chat; `GET /mock/hello` returns the last `client_hello`'s `features` / `platform` / `version` and the `X-Persona-Client` of the last `GET /api/v1/interactions`. `TEST_RUNNER_PERSONA_ELICITATION_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/ElicitationCardTests/testFormAndLink` checks that the hello declares the cards with the header's platform and version, the form's order, a bad value blocking Submit, every kind of field in the answer (`GET /mock/resolved`), and the link's Continue waiting for Open Link and the app's link card. Against `MOCK_ELICITATION=unsupported`, `TEST_RUNNER_PERSONA_ELICITATION_UNSUPPORTED_HUB=… -only-testing:PersonaUITests/ElicitationCardTests/testUnsupportedFormDeclinesAndLinkCancels` checks the hand-back note, Decline and Cancel. Answered cards leave the mock: restart it before another run. `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save the screens.
- MCP form pushes on iOS (#2433, `docs/apple/ios.md` "Data flow", interaction pushes): against `MOCK_ELICITATION=1`, `TEST_RUNNER_PERSONA_ELICITATION_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/ElicitationPushTests` posts what the hub's push looks like (`-showTestNotification elicitation -showTestNotificationIn <machine>/<session>`, category `INTERACTION_ELICITATION`), taps it from the background and finds the conversation open with the form; then launches once more without `-pairURL` so the app registers for pushes, and reads the registration's `features` from `GET /mock/devices` (it skips that part when the simulator gives no APNs token). Run on a `--profile stock` simulator: it answers the notification question.
- The composer's @ list (#2319, `docs/apple/ios.md` "Typing @ offers files, folders and sessions"): `MOCK_MENTIONS=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py` has the review agent (`macbook/a1f3c2d4-0000-4000-8000-000000000006`) read `README.md` and `WeatherWidget/Provider.swift` in calls the hub leaves unfolded, so they are that chat's files; its folder is the mock's file tree. `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/MentionMenuTests` checks the rows and their order, filtering, an e-mail address opening nothing, browsing `@Sources/` and picking a folder and then a file, a session quoted and sent with the message, the + menu's @ and a new conversation's sessions. `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save the screenshots (iPhone and iPad); `-openMentionMenu YES` opens the list by hand.
- New Agent's attachments (#2321, `docs/apple/ios.md` "Attachments in New Agent"): `MOCK_UPLOAD_KBPS=96 PORT=<dedicated port> python3 scripts/mock/mock_hub.py` makes an upload take half a minute. `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/NewAgentAttachmentsTests` attaches with `-openNewAgentAttachments` (a simulator's photo and file pickers can't be driven) and checks Start Chat waiting for the upload, the add menu, the first message's photo and path (`GET /mock/sent`; `/mock/uploads` also names each upload's computer), another computer taking the files out with its note, a Codex launcher refusing a photo, and a to-do handoff keeping its context block first. `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save the screenshots; switch the simulator's appearance with `simctl ui`.
- Recent setups in New Agent (#2134, `docs/apple/ios.md` "Recent setups in New Agent"): `MOCK_MACHINES=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py`, `TEST_RUNNER_PERSONA_RECENTS_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/RecentSetupsUITests` starts three Agents through the form (MacBook in Allow Everything with a model, Codex on Mac Studio, the lab machine after `POST …/machines/lab/reconnect`; `/mock/reset` takes it offline again), reopens New Agent and checks the three cards (the offline one not pickable), a machine card restoring its model, two cards filling every field with nothing started (`GET /mock/started`), Remove from Recent and Clear Recent Setups. `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_RECENTS_APPEARANCE=light|dark` save the screens; switch the simulator's appearance with `simctl ui`.
- Away recap in message flow (#1360): `MOCK_RECAP_LONG=1` adds eight turns before "Review PR #42 changes", taller than one screen, ending in a recap. `TEST_RUNNER_PERSONA_MOCK_HUB=<address> xcodebuild test … -only-testing:PersonaUITests/RecapInFlowTests` checks expanded recap between reply/input, scrolling away, tap-to-collapse, remaining closed after reopening (`-openRecapsFresh YES` forgets closures). `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save expanded/collapsed × bottom/scrolled-up, four shots. `MOCK_RECAPS=1` keeps the earlier two as small bars when three are folded to one line.
- Truncated output: `-openSession macbook/a1f3c2d4-0000-4000-8000-000000000002 -openAllWork YES -showFullOutput card` (expand Run card,
  "Truncated, view full output") or `sheet` (open full-output sheet, mock `/items/{id}/full` returns it).
- Stable content when scrolling up (`ChatView` freeze): `MOCK_STREAM=1` adds a step/reply every 2 seconds after opening first session, 12 times;
  `-openSession macbook/a1f3c2d4-0000-4000-8000-000000000001 -openAllWork YES -showFreezeProbe YES` scrolls up and logs the middle row's position every 2 seconds,
  in App container `Documents/freeze.txt` (`xcrun simctl get_app_container $SIM <bundle id> data`); final `RESULT:` should be 0pt movement.
- The running work line naming its step (#2987, `WorkNow` in `docs/apple/ios.md`): `MOCK_LIVE_STEP=bash|command|reads|parallel|thinking|thinking-folded` puts the weather-widget chat a minute into a new turn whose newest step is still going; `-openSession macbook/a1f3c2d4-0000-4000-8000-000000000001` shows the folded line. `bash` a command Claude described (its description alone, cut at its end), `command` a long command with no description (cut in its middle, the clock whole), `reads` three files read at once ("Reading 3 files"), `parallel` a search with two reads beside it ("+2"), `thinking` a thought after the calls came back, pushed `MOCK_LIVE_STEP_PUSH` seconds (default 3) after the chat's latest page (a page folds finished work, a push never is); `thinking-folded` has that thought in the page from the start, inside the fold (`fold.latest_thinking`).
- New Persona chat (#511): mock pushes session list immediately, transcript starts empty, first sentence appears/pushes after `MOCK_ASSISTANT_TRANSCRIPT_SECONDS` (default 1).
  After sending, chat retains that sentence (never "No messages yet"), selects new sidebar chat with Working spinner. `MOCK_ASSISTANT_START_SECONDS=<seconds>` delays POST;
  inspect bubble and "Persona is getting ready…" while waiting. Covered by `scripts/mock/test_assistant_mock.py`.
- A new chat's first message whose start failed (#1368): `GET /mock/start-fail?n=<count>&code=<code>&delay=<seconds>` cuts the next n starts off (`502`, default `assistant_in_doubt`, delivered unknown) and delays every start by `delay`; `n=0&delay=0` puts it back. UI suite `FirstMessageFailedTests` (`TEST_RUNNER_PERSONA_MOCK_HUB`, screenshots in `PERSONA_SHOTS`, `PERSONA_SHOTS_SUFFIX` appended): failed bubble, its menu, Resend spinning up and opening the conversation once, Delete back to an empty welcome.
- Lark multi-account (#1321): `MOCK_LARK_ACCOUNTS=1` adds "Me", "Work account", one signed-out account to `/api/v1/lark/accounts`; `ol_account=2` reads Work chats, top Inbox item is from Work. `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>` runs `LarkAccountsFlowTests` (Messages account switch, Work chat, Settings › Lark accounts; `PERSONA_SHOTS`); web: `scripts/test/web_lark_accounts_e2e.py <web URL> [--shots <directory>]` (usage at top).
- Lark encrypted messages (#689, #1664): `MOCK_LARK_NATIVE_OLK=1`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>` runs `LarkNativeEncryptionFlowTests` (Messages › Design review, choose external display, unlock/send/retry; Inbox can't send encrypted drafts as plain messages; no Save Password after unlock). `PERSONA_SHOTS` stores in an untracked directory outside the repository. Multi-account's second account pins "Quarterly planning", shelf-only (`feishu-pin-w1`), absent from list (#1342).
- Messaging sources (`messaging` extension point, #2138): `MOCK_MESSAGING=1`, `GET /api/v1/messaging` lists `lark` (extension `larkext`); `/api/v1/messaging/lark/…` follows `docs/extensions/extension-messaging.md`. `scripts/mock/lark_messaging.py` reads mock `/api/v1/lark/…` and converts, applying extension display rules for encrypted external/unlocked display, recall/edit history. Settings is full extension UI `POST /api/v1/extensions/larkext/-/ui/display`; hello/health add `messaging` feature, and every `{type: lark}` push also emits `{type: messaging}`. Can combine other fixtures (`MOCK_LARK=1`, `MOCK_LARK_TRAINER=1`, `MOCK_LARK_NATIVE_OLK=1`…). `python3 scripts/mock/test_lark_messaging.py` verifies schemas and processing.
- Hub without Lark gateway extension (#1530): default features include extension-provided `lark`, `lark_moments`; `MOCK_NO_LARK=1` removes them and `lark_attachments`, all `/api/v1/lark/…` return 404, no Lark connector. Messages, Settings › Connectors › Lark, translation's Lark chat option vanish; Lark docs/Moments links open as regular web pages.
- Lark /agent context (#876): `MOCK_LARK_AGENT=1` turns the "Schedule Friday…" request into Lark /agent (instructions + 7 context items in `<persona-context>`, only request outside). `-openSession server/b2e4d6f8-0000-4000-8000-000000000003`, `-showContexts YES` expands context group and first item.
- Persona sidebar: `MOCK_ASSISTANT_SIDEBAR=1` adds 36 Recent chats (above displayed 30, showing View All); first three Needs you (red), unread (blue), Working (spinner).
- Follow latest (`ChatFollowTests`, `MOCK_LONG_HISTORY=1`): long chat opens at bottom; new replies follow when already there (Persona replies first stream via `delta`); scrolling up shows "Back to latest",
  replies don't pull you down; tapping returns to bottom and resumes following. Agent opens tested normally, Unread (`-showAsUnread YES`), saved position (`-chat.read.<session> <row id>`), and cold-start with disk cache far from end (mock `/mock/paging?on=1` mimics hub pagination, `/mock/delay?s=3` shows cache first, #597; `/mock/reset` disables both).
  `GET /mock/say?text=…&user=…&stream=1` pushes replies to all open chats. App `-showScrollState YES` reports list state via accessibility
  `chat.scrollState`: bottom / follows / frozen / arrow / newestShown / withheld… (`visible` draws it). XCUITest misreads inverted-list row positions.
  `testRealHubChats` captures real-hub before/after (`TEST_RUNNER_PERSONA_REAL_PAIR`, etc., skip if absent; only own sessions, opening marks read).
- Half-written Markdown in a streaming reply (#2916, `docs/apple/ios.md` "Streaming"): `GET /mock/say?preset=repair&stream=1&steps=1&ms=0&upto=<n>`
  streams that reply into every open chat only to its first n characters and leaves it open there (no transcript line), so a moment can be
  photographed for as long as needed; the same without `upto` writes it. With the preset as written, `upto` 175 is inside a numbered list
  item's code fence, 277 a table's header row just ended, 333 a half-written row, 443 an open `**` with half a link in it, 524 inside a
  ```` ```mermaid ```` fence. A short Persona chat shows it best: `-tab assistant -openAssistant server/b2e4d6f8-0000-4000-8000-000000000002`.
- Terminal stream: `MOCK_TERMINAL_STREAM=1` (hello `terminal_stream`) sends a fake Claude full frame after subscription, then one frame/second advancing spinner;
  `MOCK_TERMINAL_FIXTURE=apple/PersonaTests/Fixtures/terminal-observe.jsonl` replays real herdr frames (colors, boxed Chinese, emoji, links, 200-character line).
  `-openPane terminal`, `-terminalWrap NO` disables wrapping, `-terminalPages 1` loads one page (mock `/screen/before` gives colored history).
- Full herdr UI (web, `docs/integrations/terminal.md` section 24): `MOCK_TERMINAL_FULL=1` (hello `herdr_client`, `herdr_open` supplies fake sidebar/tab strip/two panes, single column when narrow; Ctrl+Y emits OSC 52; `GET /mock/herdr_client` inspects received `herdr_*`).
- Full-UI focus sync (web, `docs/integrations/terminal.md` section 28): the same mock advertises `herdr_focus` (`MOCK_TERMINAL_FULL_NOFOCUS=1` removes for old hub); page `herdr_focus` echoes with `persona`. `GET /mock/herdr_focus?pane=w1:p2[&session=<id>&source=client|persona|other&mine=1|0&view=<id>]` moves focus like node notifications. `scripts/test/web_herdr_focus_e2e.py` (usage at top, two mocks/two pages). Playwright sync API dispatches page events only during its calls: use `page.wait_for_timeout`, not `time.sleep`.
  `MOCK_TERMINAL_FULL_EXEC="herdr session attach <temporary session>"` runs real client in PTY (only your own temporary session), `MOCK_TERMINAL_FULL_END_AFTER=<seconds>` ends view later, `MOCK_TERMINAL_FULL_REFUSE=forbidden|unsupported|too_many` refuses.
  E2E `scripts/test/web_herdr_full_e2e.py` (four mocks/four Vites, usage at top), real herdr `scripts/test/web_herdr_full_live.py`; full-UI herdr/Persona conflicts (right-click, Ctrl+B, mouse, paste/drop, #2700): `scripts/test/web_herdr_full_conflicts_e2e.py` (one mock/one Vite, usage at top), live temporary session `scripts/test/web_herdr_full_conflicts_live.py`; Go live: `PERSONA_LIVE_FULL_SESSION=<temporary session> go test -tags herdrlive -run 'TestLiveFullClient|TestLiveFocusPane' ./internal/herdr/`.
- Standalone terminal (web, `docs/integrations/terminal.md` section 19): `MOCK_TERMINAL_PANES=1` (hello `terminal_panes`, `GET/POST /api/v1/machines/{mid}/panes` returns fake spaces/tabs/panes; `pane:` opens fake zsh; `panes/agent` in a listed shell adds an idle session bound to it and answers its id, which `/mock/reset` removes).
- Phone terminal takeover: `MOCK_TERMINAL_CONTROL=1` (hello `terminal_control`, fake Claude arranged to App columns × rows, typed text in input,
  `GET /mock/terminal_input` inspects bytes); `MOCK_TERMINAL_BUSY=1` shows "Another program on the computer is using this terminal"; `-terminalSize phone|mirror` selects mode.
  `TerminalTypingTests/testDrivingTheTerminalSendsItsBytes` needs this mock; other terminal UI tests fix `-terminalSize mirror`.
- Attending a meeting (#2981, `docs/assistant/meeting-minutes.md` "Attending a meeting"): `PORT=<dedicated port> python3 scripts/mock/mock_hub.py` lists `meeting_attendance`, has `meeting_attendance_default` in `GET /api/v1/assistant` (`MOCK_MEETING_ATTENDANCE_DEFAULT=listener` starts there; a bad value is 400, an answer-only device 403), takes `attendance` / `attending_session` on uploads (the `200` says the role in effect and `attendance_problem`; `detail.meta` has both) and answers `POST`/`GET …/live` and `POST …/ask` with the contract's errors in its order. The attending session takes every live line at once, and a question gets a made-up answer at once (`speak` for a participant, a heard question from the live lines of the 45 s before `at`, the same answer for the same `id`). `POST /mock/meeting_attendance` with `{"feed": "busy"|"unavailable"|"not_allowed", "answer": "slow"|"fail", "answer_seconds": 3, "kb_read_only": true}` changes that (only the keys sent; `"ok"` puts one back), and `GET /mock/meeting_attendance` shows every recording's role, live log and asks; `POST /mock/recording_minutes` forgets them with the recordings. `MOCK_NO_MEETING_ATTENDANCE=1` is a hub without it (no feature or setting; `…/live` and `…/ask` are 404 without `recording_not_found`). `python3 scripts/mock/test_meeting_attendance_mock.py` checks it.
- Meeting conversation (#3862, `docs/assistant/meeting-minutes.md` "Phase 1: the meeting conversation"): the same mock also lists `meeting_conversation`. An upload with `room` makes a room meeting (its `200` carries `room`, at least `{"state": "none"}`; the top-level `attendance` / `attending_session` are ignored beside it, and `private` is taken only while there is no mark); while it attends, its room conversation (an assistant conversation, origin `meeting`, the header first) comes up a moment after the upload, each `POST …/live` with new lines is a batch to it, and `…/ask` goes to it (`via`, `heard`, `spoken`; a made-up reply in the room). Earlier meetings are an answer's material only once marked `private: false`, and the next batch tells the room what the material answered. The first upload with `ended_at` and `complete` sends the end batch and turns the room `ended` (`409 meeting_ended` for asks and for messages into it). `PUT …/private`, `POST …/just-me` (to the main chat) and `POST …/followup` (a conversation of origin `meeting_followup` seeded with the meeting) answer in the contract's order, Just me and the follow-up behind the hub's operation receipts keyed by the body's `id`; `GET …/live` has `revs` and takes `parts`, `detail.meta` has `private`, `room` and `followup`, and a content-free `meeting_live` event goes out at most once a second per recording. `/mock/recording_trim` deletes the room. `POST /mock/meeting_conversation` with `{"room": "slow"|"unavailable", "problem": "node_too_old", "room_seconds": 2, "end_seconds": 180}` changes how rooms come up and how long an upload with `ended_at` alone waits for the end; `GET` shows every meeting's state. `MOCK_NO_MEETING_CONVERSATION=1` is a manager without it (no feature, `room` and `private` ignored, the three routes 404 without a code). `python3 scripts/mock/test_meeting_conversation_mock.py` checks it.
- Meeting photos (#3862, `docs/assistant/meeting-minutes.md` "Phase 3: photos"): beside `meeting_conversation` the same mock lists `meeting_photos` and answers `POST …/photos`, `GET …/photos`, `GET …/photos/{n}` (the image) and `DELETE …/photos/{n}` with the contract's limits and errors in its order (1 MiB decoded, 4096 px on the long side, `text` 8000 and `caption` 200 characters, the same `id` returns the stored photo and another body under it is `409 op_id_reused`, a deleted photo's `id` is `410 photo_deleted`, `413 photos_full` at 50, `409 transcript_expired` once trimmed, `404 photo_not_found`; deleting a photo already deleted answers `{"deleted": false}`). A photo of a meeting whose room attends goes into the room (once it is `ready`, when it is still coming up; the image and a line from the meeting, both with `client_msg_id` `photo-<id>`; `forwarded: true`), and the room's `note` arrives a moment later as a `meeting_photo_note` call; deleting a forwarded photo sends the room the withdrawn line (`client_msg_id` `photo-withdrawn-<id>`). Minutes cite each photo that has words (`"photo": n`) and are made again when one is added or deleted, `/mock/recording_trim` keeps only the cited photos, the follow-up's seed lists the photos still there (a trimmed recording's has its minutes only), search finds a recording by its photos' words (`photos.md`), and `photos` moves in `meeting_live` and in `GET …/live`'s `revs` (`?parts=photos`). `POST /mock/meeting_photos` with `{"note": "none", "note_seconds": 1, "on": false}` leaves photos without a note, changes how long the note takes, or turns the feature off; `GET` shows every recording's photos. `MOCK_NO_MEETING_PHOTOS=1` is a manager without it (no feature, the routes 404 without a code). `python3 scripts/mock/test_meeting_photos_mock.py` checks it.
- Voice mode (docs/assistant/voice.md 6.8, #3508): `MOCK_VOICE=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py` (`scripts/mock/voice_mock.py`: `/info` at protocol 2 and a scripted socket; a protocol 1 `start` still gets the old Android script; `MOCK_VOICE=setup|p1` for an extension that is not set up / too old; `GET /mock/voice2` shows what the app sent).
  `TEST_RUNNER_PERSONA_VOICE_HUB=http://127.0.0.1:<port>` runs `VoiceModeTests`: four matrix cases shoot ten `-showVoiceSample <state>` states for Agent / main conversation × standard / AX text, and `testOpenVoiceWalksTheScript` walks the script with `-openVoice silent`.
  `TEST_RUNNER_PERSONA_SHOTS=<dir>` saves screenshots; switch the appearance with `simctl ui <udid> appearance light|dark` (`TEST_RUNNER_PERSONA_VOICE_APPEARANCE` only names the files) and the style with `TEST_RUNNER_PERSONA_UI_STYLE=beautiful`. A simulator's microphone may not work (lessons/voice.md "The iOS voice mode"), which is why the real path uses `silent`.
- Settings › Extensions: `MOCK_EXTENSIONS=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py`; `TEST_RUNNER_PERSONA_EXTENSIONS_HUB=http://127.0.0.1:<port>` runs `ExtensionSettingsTests` (list, rejected form, Running after secret), `ExtensionSlotsTests` (Settings home card/entry, usage page, machine section), `ExtensionMotionUITests` (a page drawn again ends on the new values, #37), `ExtensionControlsUITests` (row controls: a switch flips and stays, a confirm cancelled and confirmed, a failure put back, disabled, a slider and a stepper sent; both styles, light and dark, in one run, #827).
  Machine and launcher pickers, integer steppers (#2385): add `MOCK_EXTENSION_PICKERS=1` (extension "Example 聊天", its auto-reply launcher one the hub no longer has); `TEST_RUNNER_PERSONA_EXTENSIONS_PICKERS=1` with the same hub runs `ExtensionPickerTests` (the /agent switch, a machine and a launcher picked by name, a step, the missing launcher marked, saved).
  Extension packs (#4072, #4122): add `MOCK_EXTENSION_PACK=1` (Example 合集 installed with four members, one not installed; uninstalling it with its members answers 409 `partial_uninstall` and changes nothing; Example 套件 in the catalog to preview); `TEST_RUNNER_PERSONA_EXTENSION_PACK_HUB` runs `ExtensionPackTests` (the pack's page, the uninstall confirmation, the partial uninstall's card and the install preview, members by name, in light, dark and the largest text). `TEST_RUNNER_PERSONA_PHASE=before` takes the same steps on an older build for before screenshots.
  Restart mock before each round (in-memory state). Screenshots as above: `TEST_RUNNER_PERSONA_SHOTS`, `simctl ui … appearance`, naming `TEST_RUNNER_PERSONA_EXTENSIONS_APPEARANCE`, both styles.
  Extension install progress (#3307): `MOCK_EXTENSIONS=1 MOCK_EXTENSION_OPS=fail PORT=<dedicated port> python3 scripts/mock/mock_hub.py`, `TEST_RUNNER_PERSONA_EXTENSIONS_OPS_HUB=http://127.0.0.1:<port>` runs `ExtensionInstallProgressTests` (background preview steps, auto-opening on approval, stuck at Start, leave/return, cache retry completes directly, uninstall failure/retry).
  Extension CLI/login (#4507): fresh `MOCK_EXTENSIONS=1 MOCK_EXTENSION_OPS=1 MOCK_EXTENSION_AUTH=1 PORT=<port>` mock and old hub `MOCK_EXTENSIONS=1 MOCK_EXTENSION_OPS=1 MOCK_EXTENSION_AUTH=installed MOCK_EXTENSION_AUTH_OLD=1 PORT=<port 2>` each time; `TEST_RUNNER_PERSONA_EXT_AUTH_HUB` / `TEST_RUNNER_PERSONA_EXT_AUTH_OLD_HUB` runs `ExtensionSignInTests` (Install command-line programs in confirmation, Install CLI step/programs, automatic QR sign-in after install, polling success, sign-out, CLI section; old hub only warning + extension's login page, no login section).
  `MOCK_EXTENSION_OPS=1` all succeeds; `MOCK_EXTENSION_OPS_STEP=<seconds>` slows for screenshots. Without OPS, `MOCK_EXTENSION_INSTALL_DELAY=<seconds>` shows old-hub Installing.
  Extension settings Review (#3692): `MOCK_EXTENSIONS=1 MOCK_EXTENSION_OPS=stall MOCK_EXTENSION_OPS_STEP=3 PORT=<port>` (the first preview of each extension stalls half-way through its download; `/mock/reset` arms it again) and an old hub `MOCK_EXTENSIONS=1 MOCK_EXTENSION_PREVIEW_DELAY=8 PORT=<port 2>` (a synchronous preview answers that late); `TEST_RUNNER_PERSONA_EXTENSIONS_REVIEW_HUB` / `TEST_RUNNER_PERSONA_EXTENSIONS_REVIEW_OLD_HUB` run `ExtensionSettingsReviewTests` in light and dark (`TEST_RUNNER_PERSONA_LOOKS`): the operation card under Updates, the stall with Retry, the Update sheet, the upgrade going in, and the card at the largest text on this page and on the market page, with the market page's header (#4058); the old hub's spinner until the sheet.
  First launch on a new simulator asks notification permission; test dismisses it.
- Git pull: `MOCK_FILES=1 MOCK_GIT_PULL=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py`; `TEST_RUNNER_PERSONA_GIT_HUB=http://127.0.0.1:<port>` runs `GitPullUITests`, from 28 commits behind through refresh success, recheck, failure/retry.
  `TEST_RUNNER_PERSONA_SHOTS=<directory>`; actually switch `simctl ui <udid> appearance light|dark`, both styles; `TEST_RUNNER_PERSONA_GIT_APPEARANCE` only names files.
  `GitPullTests` with `FakeHub` checks POST, decode, feedback, repeated taps. Server `TestGitPull*` uses temporary local remotes with isolated HOME/Git config to test unfetched latest commits, fast-forward/divergence, modified/staged/untracked files, existing stash, failure; never pulls real worktrees.
- File viewer (`docs/apple/ios.md` ("File viewer")): `MOCK_FILES=1` provides fake `/Users/me/code/weather`: small file per language,
  3 MB JSON, 20K-line Go, 4000+-line README, 5 MB log; `GET …/file` (2 MB/page) and `…/stat` serve it. "Add a Home Screen widget to the weather App"
  final reply links several files and two nonexistent paths. `FileViewerUITests` needs this mock. Timing/fps: build `-O`
  (`SWIFT_OPTIMIZATION_LEVEL=-O`, separate DerivedData), run `FileViewerPerfTests` with `PERSONA_FILE_PERF=1`, read App
  `Library/Caches/file-perf.log`. Use App constant-speed scrolling, not XCUITest drags: each gesture captures a main-thread accessibility snapshot, itself a 40–100 ms long frame.
  Uninstall before changing mock message text: item-id caches don't replace old text under the same id.
- Spreadsheet viewer (web #1310): `MOCK_FILES=1 MOCK_SHEETS=1` adds `data/stations.csv` (3000 rows, quotes/newlines/Chinese), `reports/季度报告.xlsx` (quarterly report; three sheets, merged title, widths, numeric/percent formatting); open in file panel. `file?stream=1` returns only binary descriptions; content via `file/download`.
- Office preview (web #1345): add `MOCK_OFFICE=1`; `reports/` gains handmade `季度报告.docx` (quarterly report: headings/tables/images/footer), `季度回顾.pptx` (quarterly review: theme/master/layout placeholders/bullets/images/shapes/tables/charts/notes), `旧版说明.doc` (legacy notes; mock `file/convert` returns PDF), `路线图.pages` (roadmap; returns `no_converter`).
- Mermaid (web #1376): `MOCK_MERMAID=1` adds flowchart, sequence, pie and one invalid diagram to final widget reply.
- Jupyter (web #1407): `MOCK_NOTEBOOK=1`, `notebooks/收入分析.ipynb` (income analysis) has Markdown/math, HTML table, stdout/stderr, PNG and ANSI-colored error.
- HTML preview (web #1413): `MOCK_HTML=1`, `reports/周报.html` (weekly report) has styles, a text-changing script (default "Script did not run"), remote image (blocked), local `chart.png` and `周报.css` (weekly-report stylesheet; loaded, toolbar "Loaded 2").
- Media (web #1422): `MOCK_MEDIA=1` adds WAV, scripted SVG, browser-unrenderable HEIC (`file/thumb` draws stand-in) under `media/`; `MOCK_VIDEO_FILE=<webm/mp4>`, `MOCK_SENT_VIDEO=<same>` show file-page video/chat card (WebM safest for headless Chrome).
- Compare (web #1437): `MOCK_COMPARE=1` adds changed `reports/季度报告.docx` (quarterly report), `reports/季度回顾.pptx` (quarterly review), `assets/banner.png`; `git/blob` serves staged/HEAD versions.
- Images seen this turn (web #1540): `MOCK_TURN_IMAGES_WEB=1`: final collapsed-work turn of Review PR #42 has 9 (Read/screenshots/web/MCP/Codex view_image); release-notes turn 1 card; working widget turn 2.
- A picture that did not load (#182): `MOCK_IMAGES_FAIL=1` (or `GET /mock/images`) makes lazy chat pictures answer 503 until `GET /mock/images?ok=1`; with the variable set, pages also leave the pictures' bytes out (as the hub does for `lazy_images`), so a tool step's picture says "Image didn't load. Tap to retry" too; with `MOCK_TURN_IMAGES=many` and `-openImagesFresh YES` (drops the pictures kept on disk), `ImageViewerTests/testAPictureThatDidNotLoadSaysSoWithRetry` walks fail, tap, Retry and Close in light and dark.
- A tile that did not load (#559): `GET /mock/images?asked=1` reads how many lazy pictures were asked for, changing nothing. With `MOCK_TURN_IMAGES=many`, `ImageViewerTests/testATileThatDidNotLoadRetriesWithoutOpeningTheViewer` checks that a tap on a wide and a narrow viewed-image tile asks the hub again and brings the picture without opening the viewer; run `testASentPhotoThatDidNotLoadRetriesWithoutOpeningTheViewer` alone with `MOCK_CHAT_IMAGES=many MOCK_IMAGES_FAIL=1` added (it skips otherwise; that mock has a second strip of viewed images).
- The chat's newest end (#1108, #2016): with `MOCK_TURN_IMAGES=many MOCK_QUEUE=1`, `ChatBottomGapTests` checks that at the end of a conversation the newest message sits `ChatView.newestGap` (48 pt: a row gap and the list's room at its end) above the footer's first row, with the keyboard up, with queued messages and with an upload's bar, light and dark, and that the jump-to-latest arrow stays over the list; it writes `gap-*.png` to `PERSONA_SHOTS`. Run it on an SE too.
- The review page (#151): with `MOCK_REVIEW=1 MOCK_ECHO_SENT=1`, `ReviewPageTests` opens ⋯ › Review Changes on the widget chat, picks a range of lines, starts a review with a comment, sends it and finds the sent message's `Code comment 1` block (`/mock/sent`) and its card in the chat, light and dark; it also checks the clean and not-a-repository states (`/mock/review?mode=`), and draws the page, a comment and the finish sheet at the largest accessibility text size. `ReviewLogicTests` covers the rows, the picked range, the order, the message and the `Diff:` labels.
- Comment blocks (#151): with `MOCK_COMMENTS=1 MOCK_ECHO_SENT=1`, `CommentBubbleTests` checks that the widget chat's review sent from the web shows its three comment blocks as cards (light and dark, a tap opens one), and that `-showSampleComments YES` puts comments over the box, × drops one and Send writes the rest as the web does; it writes `comments-*.png` to `PERSONA_SHOTS`. The format itself is checked by `MessageCommentsTests` and the web's `comments.spec-fixtures.test.ts` against `spec/fixtures/comments`.
- New Chat in the Persona tab (#1749, #2189): with the default mock, `NewChatTopBarTests` taps the middle of the top bar's New Chat (where a finger lands) in the shell from the main conversation and an ordinary one, with the keyboard up, from the history drawer and right after starting conversations, and expects the welcome each time; a miss prints the shell trace (`-probes YES`). The shell on the main conversation runs over several launches (`TEST_RUNNER_NEWCHAT_RUNS`, default 5), since builds 209 and 213 lost about one tap in five: the page drawn while `assistantCurrent`'s observers were told read the old conversation (`AssistantCurrentTests` pins the order). Use a fresh mock for each run of the class: each run starts three conversations, and after two runs the drawer's Recent no longer lists the one the drawer case opens.
- Links in file previews (`FilePreviewLinkTests`): `MOCK_FILES=1 MOCK_FILE_LINKS=1 MOCK_PORTS=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py`, `TEST_RUNNER_PERSONA_FILE_LINK_HUB=http://127.0.0.1:<port>`. Open README from a chat file block and the shell Files page; use `-openSession` for the target chat rather than relying on a saved tab or an offscreen assistant handling global DEBUG arguments; links clickable immediately before closing file. Closing returns to original file position, reopening same link works, no stale link pops up after closing. Dev preview must actually show webpage text forwarded through hub.
  Run both styles in actual light/dark; `TEST_RUNNER_PERSONA_SHOTS=<directory>` saves images, `TEST_RUNNER_PERSONA_FILE_LINK_APPEARANCE` only names them.
- Video preview: synthesize 120-second >2 MiB H.264/AAC MP4 (e.g. `ffmpeg -f lavfi -i testsrc2=size=640x360:rate=30 -f lavfi -i sine=frequency=440:sample_rate=44100 -t 120 -c:v libx264 -b:v 3M -g 30 -pix_fmt yuv420p -c:a aac -movflags +faststart /tmp/preview.mp4`); `ffmpeg -i /tmp/preview.mp4 -c copy /tmp/preview-tail.mp4` creates tail-index version. `MOCK_FILES=1 MOCK_VIDEO_FILE=/tmp/preview.mp4`, optionally `MOCK_VIDEO_DELAY=0.01` per 32 KiB network chunk. `GET /mock/video-reads` reports Range starts/ends, bytes sent, active/cancelled; `?reset=1` clears. Mock correctly returns 206, Content-Range, ETag, If-Match 412, never counts a full response as Range success.
  `VideoFileUITests` checks chat-file open/close/reopen in same process; `TEST_RUNNER_PERSONA_VIDEO_HUB` dedicated mock. Switch actual `xcrun simctl ui <udid> appearance light|dark`; both styles × both appearances, inspect screenshots; `TEST_RUNNER_PERSONA_VIDEO_APPEARANCE` only names them. `TEST_RUNNER_PERSONA_VIDEO_FIXTURE=/tmp/preview.mp4` enables `VideoFileTests/nativeVideoPlaysSeeksAndCleansUp`: starts without full download, seeks to 90 seconds/resumes, reads only needed later ranges, cleans temporary cache; repeat tail-index version. `ffprobe -select_streams v:0 -show_packets -show_entries packet=pts_time,pos,flags -of json` finds 90-second keyframe `pos`; pass `TEST_RUNNER_PERSONA_VIDEO_SEEK_BYTE` to verify exact byte coverage. Without fixture integration test skips, not passes. `VideoFileResourceTests` covers direct middle/tail reads, chunk reuse, boundaries, version/length errors, cancellation cleanup; `VideoFileHTTPTests` covers auth/Range, ignored ranges, short chunks, old node errors; adding `TEST_RUNNER_PERSONA_VIDEO_HUB` runs real HTTP middle/tail/412 regressions. node/hub `TestDownload*` verifies bounded large-file reads, Range, old nodes, invalid ranges, changed files, cancellation.
- Sending video (#981, docs/protocol/protocol.md ("Sending video")): AVAssetWriter synthesizes a 6-second H.264 MOV (no ffmpeg required, gradient with sweeping bar, no real video); `xcrun simctl addmedia <udid> <file>` adds to Photos.
  Give another to `MOCK_SENT_VIDEO=<file>` (chat card sent by "another device", file only on computer); uploaded videos are stored by mock and downloadable.
  `VideoSendTests` uses real PHPicker: pick video in Agent and new assistant chats, inspect upload progress/card, check `<persona-file>` in `/mock/sent`, open system player from card;
  another test opens computer-only card, downloading first. `TEST_RUNNER_PERSONA_VIDEO_SEND_HUB`, style via `TEST_RUNNER_PERSONA_UI_STYLE=beautiful`,
  `TEST_RUNNER_PERSONA_SHOTS=<directory>`; `MOCK_UPLOAD_KBPS=640` makes progress visible. `VideoAttachTests` synthesizes one-second video, checks passthrough, H.264 MP4 conversion preserving duration, rejection when too large.
  Final video-fix verification (2026-11-03): 544 iOS unit tests + 4 targeted file/video UI cases per style passed; foreground chat-file video open, close/reopen across four actual light/dark × style combinations, screenshots inspected. Synthetic 4,116,551-byte video fully downloaded, playback advanced, seek to 8 seconds and cleanup passed. Full server suite passed against isolated real Postgres; iOS (including watch/extension dependencies) and Mac built with Xcode 27.1. Exact Xcode 26.0, devices, original user video unverified—not evidence of release or fixing original case. Initial unsigned keychain failure, hidden-shell assistant executing `openFile` breaking old file UI (recorded in #412), merged fixtures displacing file block, and appearance labels disagreeing with screenshots remain invalid/failed records; assertions weren't weakened.
- Avatars (`docs/apple/persona.md`): `MOCK_PERSONAS=demo` uses `scripts/assets/make_persona.py demo` to draw original demo avatar "Mint" on startup;
  `MOCK_PERSONAS=<directory>` lists its packs (other avatars, packs not committed). `PersonaPickerTests` needs `MOCK_PERSONAS=demo,builtin` for custom Mint and Milk Frog alongside Mochi; it checks selection across relaunch. Run in light and dark with `TEST_RUNNER_PERSONA_LOOK_APPEARANCE=light|dark` and the matching simulator appearance; `TEST_RUNNER_PERSONA_LOOK_SHOTS=<directory>` saves captures.
  `LookPickerTests` (independent avatar/theme/style choices) needs directory mock containing a colored pack id `doudou`;
  `PERSONA_LOOK_SHOTS=<directory>` stores theme/style pages under Settings › Appearance and Settings home (actual simulator light/dark). It changes saved `ui.style` / `ui.color`, then restores Liquid Glass + Follow interface style.
  Direct combination: `-ui.style clay -ui.color persona -openPersona doudou`.
- Hub version/update (`docs/operations/hub-update.md` ("In the App")): `MOCK_HUB_UPDATE=available|waiting|notice|docker|docker-pinned`: new version with auto-update off,
  waiting on two busy Agents, first "Automatic updates enabled" notice, Docker new image (`:latest` or pinned tag). Unset: `GET /api/v1/hub/update` 404, old hub, absent everywhere.
  `GET /mock/hub_update?mode=…` changes/restarts live. `-tab settings -openHubUpdate YES`, `-openSkills YES` opens pages; notice in Agents.
  `HubNoticeUITests` (independent notice buttons, Update now asks confirmation) needs `TEST_RUNNER_PERSONA_HUB_UPDATE_HUB=<mock URL>`.
- Real takeover without deployment: build darwin personad on hub machine, run local test hub (fake HOME, `HERDR_SOCKET_PATH` real herdr socket,
  dedicated `persona_termlab` database over SSH tunnel, `machines` only one `kind: local`), pair simulator, open lab pane terminal (measurements in `docs/integrations/terminal.md` section 11 used this).
- Terminal disconnect: `MOCK_SCREEN_FAIL_AFTER=3:14` returns 503 after 3 seconds of terminal reads, recovers 14 seconds later;
  `-openSession macbook/a1f3c2d4-0000-4000-8000-000000000001 -openPane terminal`; red "Last updated N seconds ago · Retry" should appear, then disappear on recovery.
  Timing starts at first request, not mock startup; if old App polls during build, start App before mock.
- Gap healing (`docs/protocol/protocol.md` ("Item continuity")): `MOCK_GAP=1`, uninstall/reinstall App, `-openSession macbook/a1f3c2d4-0000-4000-8000-000000000001`.
  Starts with 8 rows; writes 6 more after 2 seconds but pushes only last 2 (`from_seq` 12); log should show `items after_seq=8` about 1.5 seconds later,
  work shows "Tool 5". Use timeout-bounded waits, not success-only `until grep`.
- Hand to AI per source (#2121): `MOCK_INBOX_EVENTS=1 MOCK_EVENT_MUTES=1` gives three events their mute options. The two mail notifications ("构建失败：主分支" and "会议室变更通知") share one sender, so handing one off preselects that target, marked Last time, on the other (`InboxHandOffBarTests`). With `MOCK_INBOX_EVENTS=legacy MOCK_EVENT_MUTES=1` the same two open as plain item pages, and `InboxHandOffSheetTests` checks the Hand to AI sheet the same way.
- GitHub thread page (#1055): `MOCK_GITHUB_INBOX=1`, `-tab mail -openInboxItem github:review12` opens fictional example-org/example-repo#12:
  header (status/branches/three labels), Markdown description, request-changes/approve reviews, inline comment/reply, three checks (failed test logs), events.
  Comments are in `/mock/sent` and appended to timeline. `github:ci7` (CI failure) isn't a PR, opens hub-collected text. Data: `gh_thread_page` in `mock_hub.py`.
- Lark conversation page: `-tab mail -openInboxItem lark:f3:300` (Weekend hiking group) opens full chat: three days/date separators, over a page before first unread (scroll up, `has_older`),
  images, quoted/emoji replies, bots, 75-reply thread with paged sheet; 12 unread from message 300, read through 299 (`read_position`).
  `MOCK_LARK_LOGGED_OUT=1` makes lists/chats/threads return 502 "Lark gateway isn't signed in to Lark" (`code: lark_not_logged_in`, like hub), testing errors. Fictional `_lark_f3` in `mock_hub.py`.
  Read receipts (`POST …/read-states`): own f3 messages 282 all read, 296 partly, 306 none; DM f1 "Han Meimei" (`-openInboxItem lark:f1:100`) has own read/unread (103)/no receipt (104); live new mock messages are unread.
  Avatars: Tom's f3 loads, weather assistant returns 404 (initial fallback), others none.
  Mentions: f2/f3 members (`GET …/members`, Tom/Mia avatars, Ajie's group nickname "Jay"); sends/replies with `mentions` store `@name` text as `at` segments.
  Thread group (#1087): `MOCK_LARK_TOPIC=1`, `-tab mail -openInboxItem lark:f4:405` opens Release group (also in Messages): bot posts three topics two minutes apart (individual cards), long >320 pt collapsible card, own topic, linked topic, 0/1/2/6 replies, recalled topics (gateway retained and unretained), edited topic (latest reply recalled), for the "Fengling Yueying" style. Like gateway, replies return topic first then latest `limit`. Don't combine `MOCK_LARK=1`, which takes over Lark reads and hides Release.

### Readiness scenarios (`MOCK_READINESS`, #287)

New-user onboarding states ([onboarding.md](../assistant/onboarding.md) 4.4–4.6, section 5) share these fixtures across web Playwright, Android and iOS / Mac screenshots. Start with `MOCK_READINESS=<scenario>`:
adds `assistant_readiness`, `machines_add`, `machine_trust`, `builtin_presets` to `features`; `GET /api/v1/assistant/readiness[?probe=1]`, `POST …/readiness/dismiss`,
`POST /api/v1/assistant/warm` (with `readiness`) return hub-shaped data/text (read `server/internal/i18n/locales`, using `Accept-Language`, Chinese if absent).
`POST /api/v1/assistant/conversations` rejects with scenario codes; `GET /api/v1/launchers/presets` lists built-in providers (directly from `server/internal/assistant/presets_builtin.json`), then mock plugin presets.
Add/confirm computer (`MOCK_ADD_COMPUTER`) is enabled too; readiness follows machines: in `zero`, add, wait for connection, confirm, then `computer` becomes `ok`.

| Scenario | Computer | Readiness | Start chat |
| --- | --- | --- | --- |
| `zero` | None | `computer` todo `no_machine`, others `unknown` | 409 `no_machine` |
| `offline` | Demo MacBook (`mbp`) offline (`error_code: link_offline`) | `computer` problem `node_unreachable` | 503 `node_unreachable` |
| `pending` | `mbp` online, pending (`trust: pending`) | Same as `zero`: hub doesn't give pending computers to assistant | 409 `no_machine`; Agent start there (`POST /api/v1/sessions`) 409 `machine_pending` |
| `no_claude` | `mbp`, no `claude_version` | `claude` problem `claude_missing` | 409 `claude_missing` |
| `not_logged_in` | `mbp` | `model`, `first_message` problem `not_logged_in`; sole main-chat turn "Not logged in · Please run /login" (`turn_error_code`) | Normal |
| `bad_key` | `mbp` | As above, `auth_failed` ("Invalid API key"), button `edit_provider` | Normal |
| `full` | `mbp` | All `ok`, one main-chat reply | Normal |
| `demo` | Usual three mock machines | All `ok` (assistant on `server`), other data unchanged, for "after getting started" screenshots | Normal |
| `unverified` | `mbp` | `model` unknown `model_unverified` (`mode` still `full`); `?probe=1` makes it `ok` | Normal |
| `launcher_missing` | `mbp`, launcher `cc_glm` gone | `claude` problem `launcher_missing` | 409 `launcher_missing` |
| `model_route` / `rate_limited` | `mbp` | `model` problem (`rate_limited` doesn't block, `mode` is `full`) | Normal |
| `node_outdated` | `mbp` node too old (`error_code: node_outdated`) | `computer` problem, no button | Normal (without configured model; with model real hub returns 409 `node_outdated`, screenshot via `MOCK_START_ERROR=node_outdated`) |
| `assistant_offline` | `mbp` | Readiness/warm/start all 503 `assistant_offline` | 503 |

Except `demo`, only `mbp`, no Agents or other assistant chats (new-user state). `MOCK_START_ERROR=<code>` overrides scenario and rejects every start
(`no_machine`, `machine_pending`, `claude_missing`, `launcher_missing`, `node_unreachable`, `node_outdated`, `assistant_offline`) for each error card;
`MOCK_BUILTIN_PRESETS=1` only adds built-in providers and `builtin_presets`. In these cases, `MOCK_PROVIDERS=1` Test Connection returns Anthropic's region-block message and `code: "region_blocked"` for keys containing `region` (#4482, text from hub). Like hub, at most 3 pending computers (fourth 429 `too_many_pending`).
`MOCK_INBOX_SOURCES=none` leaves no Inbox sources (no items/mail accounts, Lark unconfigured, GitHub signed out); combine `zero` for zero-computer/zero-source empty states (#4485, #4480). `mock_scenarios.py` names them `readiness-<scenario>` for live switching.
For the probe's client boundaries (#1892), `MOCK_READINESS_PROBE_DELAY=<seconds>` delays only `?probe=1`, and `MOCK_READINESS_PROBE_ERROR=<HTTP status>` refuses it with a localized reason. With `MOCK_READINESS=not_logged_in`, run `ReadinessUITests/testTryMessageWaitsForAnAnswerOrShowsFailure` using `TEST_RUNNER_PERSONA_PROBE_EXPECT=slow` and a 35-second delay: the failed probe appears after the old 30-second client timeout, its reason remains beside an enabled Try a message button, and the test checks the elapsed time. Use `TEST_RUNNER_PERSONA_PROBE_EXPECT=error` with `MOCK_READINESS_PROBE_ERROR=502` to check the HTTP failure reason. With `MOCK_READINESS=bad_key`, `TEST_RUNNER_PERSONA_PROBE_EXPECT=model_error` clicks the model row's alternative Try button and requires the 502 reason on that row, not the first-message row. The usual mock-hub and screenshot runner variables apply.
Gmail sign-in (#2442): `GET /api/v1/mail/oauth/google/config` says `configured: false` and `google/start` answers 400 `google_not_configured` (text contains "not configured"), so the add-Gmail page offers the app-password form; `MOCK_GOOGLE=1` says `configured: true` and `start` answers with a made-up URL, so the page shows Sign In with Google.

iOS add-computer (`AddComputerTests`, #4196): `python3 scripts/mock/mock_scenarios.py --port <port> --control <control port>`,
`TEST_RUNNER_PERSONA_ADD_HUB=http://127.0.0.1:<port> TEST_RUNNER_PERSONA_ADD_CONTROL=http://127.0.0.1:<control port>`. Tests switch `MOCK_READINESS=zero` / `MOCK_ADD_COMPUTER=seed`,
`MOCK_PAIR_SECONDS` as needed: zero → add → wait → confirm → online, Linux row, expiry/regeneration, pending/error_code in list, rejection removes row, Agents zero-computer state; `TEST_RUNNER_PERSONA_SHOTS=<directory>` saves light/dark.

Built-in preset wizard (`ProviderPresetWizardTests`, #4197): with a scenario or `MOCK_BUILTIN_PRESETS=1`, Test requests carrying built-in `preset` return hub text according to words in the key (`bad` 401, `forbid` 403, `nopath` 404, `busy` 429, `slow` timeout, `region` region block).
Adding with `preset` follows hub naming (`deepseek`, duplicate `-2`) and display names; with `assign`, returns `assigned` / `kept`. `MOCK_ASSIGN_KEPT=assistant:customized,…` retains listed purposes for the supplied reasons.
Start `mock_scenarios.py` as above, set `TEST_RUNNER_PERSONA_PRESET_HUB=…`, `TEST_RUNNER_PERSONA_PRESET_CONTROL=…`: A, already using Claude Code (`full` test message succeeds, `not_logged_in` fails); B, only a key (401 → replace key → test passes → save, main chat `kept`); C, where to get a model (sign up → link card → close to key entry), plus disabled Advanced import with zero computers; D, Grok's Responses direct API (tested key, no assign switch, direct-API note on key and done). `TEST_RUNNER_PERSONA_SHOTS=<directory>` saves screenshots.

Drift guard: `server/internal/assistant/readiness_scenarios_test.go` builds the same fake-hub scenarios and saves real readiness responses (three languages) and start refusals in
`testdata/readiness_scenarios/<scenario>.json`; `scripts/mock/test_readiness_mock.py` compares each mock response. After readiness/text changes: `UPDATE_GOLDEN=1 go test ./internal/assistant -run TestReadinessScenariosGolden`,
review diff, update `scripts/mock/readiness_mock.py`.

## UI tests (`scripts/app/ui-test.sh`)

`apple/PersonaUITests` drives the real App in a simulator against the configured hub (not part of default unit tests):
`SIM=<udid or name> ./scripts/app/ui-test.sh [machine/session]` (slim boot through `sim-boot.sh`, see "Simulators" below). Currently tests edge-swipe back with expanded/collapsed top bar.
Long-press message toolbar (`MessageMenuTests`: release after 0.4 / 0.8 / 1.5 / 2.5 seconds leaves it open; tap list/scroll closes) uses `scripts/mock/mock_hub.py`, not real hub:
`TEST_RUNNER_PERSONA_MOCK_URL=http://127.0.0.1:18760 xcodebuild test … -only-testing:PersonaUITests/MessageMenuTests`.
Select Text copies only a segment (`SelectTextTests`: Agent reply with code block, main-chat reply with bold/list; `MOCK_SELECT_TEXT` has a user message with `__init__.py`, `2*3*4`, `# …`, and a reply with fences/math/indented code/separator; double-tap first word, clipboard exactly that word; `testQuoteThePickedWords` picks a word, taps Quote first in the edit menu, and checks the composer quote and the sent bubble carry only that word). Use `MOCK_CHAT_READ=1 MOCK_SELECT_TEXT=1 MOCK_ECHO_SENT=1`,
`TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>`, style `TEST_RUNNER_PERSONA_UI_STYLE=beautiful`, `TEST_RUNNER_PERSONA_SHOTS=<directory>` (named by simulator appearance).
Run a new UI test against unfixed code first to confirm it fails, then apply the fix.

### Simulators: always `scripts/app/sim-boot.sh`, slim by default (simslim, #2287, #2801)

**Create, reuse and boot simulators through `scripts/app/sim-boot.sh`**, not direct `simctl create` / `simctl boot` or letting `xcodebuild test` create one.
It reuses/creates by name, disables unused services with [simslim](https://github.com/mobai-app/simslim) purpose-specific profiles, verifies (`simslim verify`),
checks required capabilities (`simslim doctor --requires …`), prints udid to stdout. `sim.sh`, `ui-test.sh`, `relay_sim_e2e.sh`, `diag/stream-scroll/run-ios.sh` already use it.

```sh
SIM=$(scripts/app/sim-boot.sh "Persona Shots <issue number>")       # create if absent (iPhone 17 Pro, latest compatible runtime)
SIM=$(scripts/app/sim-boot.sh --profile watch --device-type "Apple Watch Series 11 (46mm)" "Persona Watch <issue number>")
SIM_PROFILE=stock SIM=<udid> ./scripts/app/ui-test.sh           # scripts select profile from environment
```

Local measurements (2026-11-05, same iPhone 17 Pro / iOS 27.0, reboot then idle 90 s, `simslim measure`, machine load 300–1000):

| | Processes | Memory (phys_footprint) |
| --- | --- | --- |
| Stock | 244 | 10.87 GB |
| `default` | 86 | 2.34 GB |
| `default` with widgets (Live Activities) also disabled | 88 | 2.77 GB (no saving, so default keeps them) |

Profiles (`apple/simslim/<name>.json`, reasons in each `description`):

| profile | Purpose | Kept beyond disabling everything |
| --- | --- | --- |
| `default` | UI tests, screenshots, ordinary App use | widgets (Live Activities, Dynamic Island, widgets), photos (picker/save), `com.apple.apsd` (push), `com.apple.linkd` (App Intents: Live Activity buttons, Shortcuts) |
| `watch` | Paired iPhone + Apple Watch (both use it) | All default + connectivity (pairing/WatchConnectivity), messaging (identityservicesd), health (HealthKit/phone health tools) |
| `live-activity` | Real APNs Live Activities (push-to-start, `TestManualLiveActivityPush`; not yet run against real APNs) | All default + store (App Store/push/media) |
| `stock` | No slimming: "no system prompt" assertions, keyboard-height screenshots, Siri / Spotlight / dictation, or ruling out slimming effects | Everything |

Persona has no Universal Links (no associated domains), so web isn't kept; no in-app purchases, so no storekit.

- **Never reboot an already-running simulator** (another Agent may use it): verify slim ones; unslimmed/profile-mismatched ones remain unchanged with warning and `in-use-not-slim`.
  Only use `--reslim` if it's yours (reboots). Don't `simslim on / off` others' devices, or use `simslim watch` (changes all local simulators).
- Without simslim, normal `simctl boot`, install hint, `stock-no-simslim`. Install: `brew install mobai-app/tap/simslim`,
  or `go install github.com/mobai-app/simslim/cmd/simslim@v0.14.0` (pinned; version reports dev). Outside PATH, use `SIMSLIM=<path>`.
- Last stderr line: `sim-boot: slim|stock|stock-no-simslim|in-use-not-slim|failed→stock <profile> <udid>`; include in reports.
  Slimming failure runs `simslim off` to restore stock, then continues (`failed→stock`); tests still run. Usage errors (missing profile/udid, duplicate names) exit 2 without changes.
- Actual slimming: `scripts/app/slim-sim.sh <udid> [profile JSON]` (one boot for shut-down devices; running mismatched devices reboot), usually not called directly.
- Creation, `simctl erase`, runtime changes lose per-device slimming; rerun sim-boot.sh. Afterwards `xcrun simctl shutdown <udid>`, task end `xcrun simctl delete <udid>`.
- If a test fails only slimmed, rerun on a new `--profile stock` device (or `simslim off <udid>` on yours) before blaming code.
- Temporarily enable services: `simslim enable <udid> <category or launchd name>` on running device, no reboot, lasts until next `on` or reboot.
  Permanent: add to profile (`simslim enable … --save apple/simslim/<name>.json`), then `--reslim`. `on --no-reboot` only disables more, never reenables. Categories: `simslim profiles`; features: `simslim doctor --list`.

| Test / feature | Profile or extra services |
| --- | --- |
| `LiveActivityActionTests`, `LiveActivityShots` | stock first. iOS 27.0 (2026-11-05, #2801): stock 1/2 passed (first after reboot failed, no activity on lock screen), default 1/5. Unknown slimming versus load instability; don't draw conclusions from slim results yet |
| `PhotoSaveTests`, `VideoSendTests`, `LarkAttachmentTests` | default (photos enabled) |
| `NotificationActionTests`, `relay_sim_e2e.sh` (local notifications / `simctl push`) | default (apsd kept) |
| `TestManualLiveActivityPush` (real APNs) | live-activity |
| `QuickLookFileTests` share sheet stuck/slow | `--keep com.apple.peopled,com.apple.suggestd` |
| Manual Spotlight, file-picker Recent | search |
| Manual dictation, recording transcription, Shortcuts | siri + `--keep com.apple.mobileassetd` (linkd already kept) |
| "No system prompt" tests (`PasswordPrompt.assertNone`: `SecretFieldTests`, `ChannelSettingsTests`, `EngineChoiceTests`, `ExtensionMarketTests`, `ExtensionSettingsTests`, `LarkNativeEncryptionFlowTests`, `PermissionInputUITests`), keyboard-height screenshots | stock. Disabling web/icloud/siri may prevent Save Password prompts entirely or alter candidates; slim passes aren't regression evidence |
| Manual health tools/watch pairing | watch |

Pitfalls:
- First boot after create is slow (high load: slim including first boot 439 s, stock 167–285 s; later boots 12–28 s); `--reslim` on running stock takes 12 minutes at load 300+.
  For repeated creation, first make a slim template, `simslim clone <template udid> <name>` (keeps profile; `simctl clone` doesn't, rerun sim-boot.sh).
- Under high load, newly opened Apps (even system Settings) may be white for a minute, stock too; wait before screenshotting, not a broken slim profile.
- iOS 27.1 runtime lacks device types like iPhone 17 Pro; `simctl create` reports Incompatible device. sim-boot.sh picks latest compatible runtime; override with `--runtime`.

Chat composer (#2200): confirm port free, then `PORT=<dedicated port> python3 scripts/mock/composer_hub.py`.
This separate entry reuses fictional data, only changes main/Agent chat models to short/long GPT IDs, never other lanes' fixtures.
`TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<dedicated port> xcodebuild test … -only-testing:PersonaUITests/ComposerKeyboardTests`
checks no App keyboard-dismiss button in compact and regular-width iOS navigation, input/newlines, gesture dismissal retaining draft, sending, sidebar-header collapse, model-label/menu consistency.
Style `TEST_RUNNER_PERSONA_UI_STYLE=beautiful`; actual `simctl ui <udid> appearance light|dark`,
`TEST_RUNNER_PERSONA_SHOTS=<directory>` saves, `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` only names files.
Fresh dedicated simulator paired only to this mock; never target real main chat. Fixture regression:
`python3 -B -m unittest discover -s scripts/mock -p 'test_composer_hub.py'`.

输入框展开（#5685）：同一隔离 mock，运行 `ComposerExpandTests` 和 `ComposerGrowthTests`。所有 `xcodebuild` 前加 `~/.local/bin/xcode-slot`，用 `-parallel-testing-enabled NO`，小屏和大屏依次验证，不同时启动两台模拟器。`testAdaptiveThresholdAndFourStates` 在实际输入中寻找阈值，验证未达阈值无按钮、到阈值右上角按钮、停止长高、展开 / 收起不改文字、光标恢复、删除后按钮消失。`TEST_RUNNER_PERSONA_UI_LANGUAGE=zh-Hans|en|ja` 验证两种按钮的无障碍标签；`TEST_RUNNER_PERSONA_EXPECTED_LINES=<行数>` 可断言指定设备的实际阈值。用 `xcrun xcresulttool export attachments --path <结果.xcresult> --output-path <截图目录>` 导出 `01-below-threshold`、`02-at-threshold`、`03-expanded`、`04-collapsed` 四张附件，再人工检查。深浅色由 `simctl ui <udid> appearance light|dark` 切换。键盘截图使用 `sim-boot.sh --profile stock`；每台跑完就 shutdown，任务结束删除自建模拟器。真实横屏用支持横屏的 iPad；当前 iPhone 的 `Info.plist` 只允许竖屏，不能把设置了 `PERSONA_ORIENTATION=landscape` 当作已经旋转。测试会断言 App 窗口宽度大于高度。部分运行时的 `app.screenshot()` 在旋转后会出现错向 / 黑边，功能测试通过不等于截图可用，不用这种截图做视觉验收。

Notification buttons (`NotificationActionTests`) use mock, not real hub:
`PORT=18791 python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18791 xcodebuild test … -only-testing:PersonaUITests/NotificationActionTests`.
Long-press SpringBoard banner, tap Deny, inspect mock `GET /mock/resolved` for resolve.
`LiveActivityActionTests` also uses mock: start sample activity, lock, tap Deny. English system authorization button is Allow;
authorization persists, so schedule notification only once App goes background or it appears while test still waits for permission.

Direct terminal typing (`TerminalTypingTests`: tap opens keyboard, text/Return sent in order and coalesced into few requests, key-bar ctrl applies to next typed character, keyboard button dismisses) also uses mock.
`MOCK_TERMINAL_INTERVAL=0.02` replays recorded frames rapidly (default one/second), with `MOCK_TERMINAL_FIXTURE=apple/PersonaTests/Fixtures/terminal-observe-mixed.jsonl`
(long mixed Chinese/English reply) or `-permission.jsonl` (permission dialog) for terminal shots.
Terminal keys/selection (`TerminalKeysTests`: tap/double-lock/hold ctrl/alt/shift, typed ctrl text, ⌃C, key names/order, long-press Copy, double-tap shortcut sends, input completion) also uses mock:
`PORT=18792 python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18792 xcodebuild test … -only-testing:PersonaUITests/TerminalKeysTests`
(mock `GET /mock/keys` returns recorded keys).

Search results open chats with only one title line, remain after return, and find/open Agents inside collapsed spaces (`SearchOpenTests`, with non-search control and edge-swipe back;
`-openAgentsFresh YES` avoids old collapse state) against ordinary mock:
`PORT=18793 python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18793 xcodebuild test … -only-testing:PersonaUITests/SearchOpenTests`.

Assistant `ask_user` renders as Question (`AskUserRowTests`, #677): tool row says Question + first question, not raw name; expanded questions/options check user choices (including multi-select),
unanswered says No answer. `-openAllWork YES -showToolDetails YES` expands work/tool rows (inverted-list taps unreliable):
`MOCK_ASK_USER=1 PORT=18797 python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18797 xcodebuild test … -only-testing:PersonaUITests/AskUserRowTests`,
shots `/tmp/persona-ask-user-*` (`TEST_RUNNER_SHOT_SUFFIX` distinguishes appearance/style).

To-do details No Agent currently (#1110): `MOCK_TODO_OFFLINE=1` adds backup project on disconnected NAS to Weather App 1.3 release, session unknown;
`-openSession macbook/a1f3c2d4-0000-4000-8000-000000000002 -openTodoDetail YES`; backup says No Agent currently, weather doesn't.

To-do details Decisions (#1618): the mock always serves `GET/POST /api/v1/todos/{id}/decisions` and `PATCH …/decisions/{did}` as the hub does (every to-do empty: the empty state).
`MOCK_TODO_DECISIONS=1` gives Weather App 1.3 release a decision answered in an Agent's question, one revised and the one that revised it; `-openSession macbook/a1f3c2d4-0000-4000-8000-000000000002 -openTodoDetail YES -showDecisionHistory YES`
opens it with the revised one shown struck through; `-openSession studio/a1f3c2d4-0000-4000-8000-000000000003 -openTodoDetail YES` opens the blog to-do (none recorded).
Record / Revise / the row's ⋯ write into the mock (the to-do's `updated_at` moves, `todos` is pushed); `MOCK_LEGACY_HUB=1` answers the route-level 404 and the section is hidden. `python3 -I scripts/mock/test_todo_decisions_mock.py` checks the routes.

Port forwarding (`PortForwardTests`, `docs/operations/ports.md`): `:<port>` badges in chat header/replies include page titles; stopped gray. Open loads mock's page through
`…/ports/{port}/connect` (`GET /mock/ports` records connections); gray says No longer listening:
`MOCK_PORTS=1 PORT=18791 python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18791 xcodebuild test … -only-testing:PersonaUITests/PortForwardTests`;
light/dark × compact shell / regular-width sidebar, `TEST_RUNNER_SHOT_SUFFIX`, shots `/tmp/persona-ports-*`.

Dev server preview on the iPhone (`PreviewUITests`, #150, `docs/web/preview.md` "iPhone"): ⋯ › Preview lists the mock's launch.json servers and the
Agent's Storybook port; web opens in the link card (the mock forwards a running preview port to its own page server), the bar goes back, attaches a
screenshot, and picks the Save button into an Element comment card; api's Start… shows `env PORT=8080 go run ./cmd/api` and opens once its port answers:
`MOCK_PREVIEW=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/PreviewUITests`,
`TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps each screen, `TEST_RUNNER_PERSONA_LOOKS=light,dark` runs it in both appearances in one go. `-openPreviewServers YES` opens the list as the chat appears, `-showPreviewPicking YES` starts picking in a forwarded page (screenshots).

Own-hub links open in App (`HubPageLinkUITests`, #2696): mock `/s/…?at=…` opens Agent chat with target message visible, `/todos?id=…` opens to-do details;
external links remain web cards. `MOCK_HUB_LINKS=1 PORT=18796 python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_HUB_LINKS_HUB=http://127.0.0.1:18796 xcodebuild test … -only-testing:PersonaUITests/HubPageLinkUITests`;
`TEST_RUNNER_PERSONA_SHOTS=<directory>`, `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save shots. URL unit tests `PersonaTests/HubPageLinkTests`.

A link tapped again opens its own page (`LinkSheetReopenTests`, #4074): Persona's main chat links 示例页面 (example.com); in the sheet the page's link leads on to iana.org
and the sheet is closed there; tapping 示例页面 again shows example.com. Needs the internet. A synthesized edge swipe does not start WebKit's back gesture in the simulator,
so that the reopened page has no history is checked by the unit tests.
`MOCK_LINKS=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_LINKS_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/LinkSheetReopenTests`;
`TEST_RUNNER_PERSONA_SHOTS=<directory>`, `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save each step. Pool unit tests `PersonaTests/WebViewPoolTests`.

Telegram reply box (`ConnectorThreadReplyTests`, #1190): sent replies appear immediately; no confirmation-card notice or Allow:
`MOCK_TELEGRAM=signed_in MOCK_TELEGRAM_INBOX=1 PORT=18857 python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_MOCK_HUB_TELEGRAM_THREAD=http://127.0.0.1:18857 xcodebuild test … -only-testing:PersonaUITests/ConnectorThreadReplyTests`.

QR login (`QRSignInTests`, #1351): `MOCK_WECHAT=signed_out PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_MOCK_HUB_QR=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/QRSignInTests`: Settings › WeChat › Sign In shows QR and Save QR Code,
mock confirms sixth poll, auto-switches to connected. Restart mock before another round (e.g. dark), state in process; `MOCK_WECHAT_WAIT=1` stays QR for shots. `MOCK_WECHAT_QR=link` removes matrix only, `none` removes link too (unrenderable step, #1693). `testAStepItCannotDrawSaysSo` uses another `MOCK_WECHAT=signed_out MOCK_WECHAT_QR=none`, `TEST_RUNNER_PERSONA_MOCK_HUB_QR_NONE=http://127.0.0.1:<port>`; page explains inability, Restart, no Next.
Hub decides own-device replies need no cards (mock doesn't distinguish devices); `TestConnectorThreadReplyFromTheUsersDevice`.

Secret fields never prompt Save Password (`SecretFieldTests`, #1598): `MOCK_EXTENSIONS=1 MOCK_BOT=off MOCK_SCREEN_SECRET=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_SECRETS_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/SecretFieldTests`: provider API keys (manual/extension), Lark bot App Secret,
mail password/app code, terminal password; after entry/navigation no system Save Password (`PasswordPrompt.assertNone`), values reach mock. Terminal card disappears after answer; restart for rerun.

Channels (`ChannelSettingsTests`, #1529, #2195, `docs/extensions/channels.md` ("UI")): `MOCK_CHANNELS=1 MOCK_EXTENSIONS=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_CHANNELS_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/ChannelSettingsTests`: Channels row/summary before Connectors, no channels in extensions, About only version/build with assistant settings accessible,
three sections (Persona / Connected / Available to install), WeChat account/destination/features, token binding (invalid inline error), Lark bot unbind/re-pair six-digit code,
From WeChat below first main-chat message. Restart mock for dark (binding state in process); `-openChannels YES` / `-openChannel <id>` direct opens.

Sent as You (`SentModelTests`, `SentAsYouTests`, #5278, `docs/extensions/connectors.md` ("Sent as the user")): `MOCK_SENT=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/SentAsYouTests`: the row after Connectors, how each send was let through, a row's details and a text of exactly 4000 characters,
the chat that asked, Load Earlier, the connector chips and a chip with nothing recorded (Show All), and the empty / no database / no full access / older hub / unreachable states. Each test first puts the mock back to its rows
(`GET /mock/sent?mode=1`, which also forgets recalls), then switches it with `mode=empty|nodb|forbidden|old`. Recall (#3219, `testRecallAsksThenTakesTheMessageBack`): row 66 has **Recall…** and the mock takes it back,
row 60's recall is refused (502 `recall_refused`, the red text under the button) and row 54 carries the **Recalled** tag; shots `sent-qa-recall` and `sent-qa-recall-refused`. `TEST_RUNNER_PERSONA_SHOTS=<dir>` writes `ios-sent-as-you-light.png` and `-dark.png` (`testScreenshots`). `-tab settings -openSentAsYou YES` opens the page directly.

Browser logins (#1304): `MOCK_BROWSER=ready PORT=<dedicated port> python3 scripts/mock/mock_hub.py`, `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>` runs `BrowserLoginsTests`: machine websites, pick cookies.txt (`-openCookieFile <path>` replaces file picker), none selected by default, expired disabled, import/clear; `TEST_RUNNER_PERSONA_SHOTS=<directory>`.

Send/terminal-dialog safety (`SendSafetyTests`) uses mock:
`PORT=18771 MOCK_SCREEN_DIALOG=1 MOCK_PROMPT_CHANGED=1 MOCK_SEND_REFUSE=input_draft MOCK_RESTART=1 python3 scripts/mock/mock_hub.py &` (`MOCK_RESTART=1`: op receipts, a repeated id gets its first answer),
`TEST_RUNNER_PERSONA_SAFETY_HUB=http://127.0.0.1:18771 xcodebuild test … -only-testing:PersonaUITests/SendSafetyTests`:
tap dialog option, hub gets `option` not keys and Dialog changed notice; existing computer draft, Clear and send retry carries
`clear_draft` under a new `client_msg_id`, and Resend after Not Now reaches the computer again under a new one (#6425; `GET /mock/resolved`, `GET /mock/sent`). Screenshot `-openSendMessage <text>` sends after 3 seconds (`-openSendDelay <seconds>` overrides).

Terminal questions of several (`ScreenStepsCardTests`, #2970): `MOCK_SCREEN_STEPS=1 PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_STEPS_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/ScreenStepsCardTests`. The mock's three-question dialog moves as the terminal does
(← / →, an answer moves on, the node submits the review tab after an answer that names its option): Previous back to the first question with its answer marked, Next on,
the last answer as keys and the review card before Submit, in light and dark; and a review with a question left (`GET /mock/screen-steps?reset=review`) whose button goes back to it.
`GET /mock/screen-steps` shows where the dialog is, the answers and every resolution; `TEST_RUNNER_PERSONA_SHOTS=<dir>` writes `steps-*.png`.

New Persona chat from elsewhere (`NewConversationEntryTests`, #527): Inbox Hand to AI immediately shows sent message, not No messages yet;
late transcript shows it once. `PORT=18927 MOCK_SOURCES=1 MOCK_TODO_OFFLINE=1 MOCK_ASSISTANT_TRANSCRIPT_SECONDS=8 python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18927 xcodebuild test … -only-testing:PersonaUITests/NewConversationEntryTests` (main chat and new chat in supported navigation; Agent targets require those two fixture flags, and the test checks they are present).
The main chat goes through the mock's `POST /api/v1/inbox/{id}/handoff` (#1194; what went is in `/mock/sent_to`, its transcript copy carries the `client_id`); `MOCK_HANDOFF=old` makes
the mock a hub without that route (404). A hand-off whose answer got lost (`POST /mock/fault` lost_receipt) keeps the sheet and says to look in the chat first; sending again goes under
the same `client_id` (one hand-off in `/mock/sent_to`) and adds how to send it anyway (`testAHandOffThatMayHaveGoneSaysToLookFirst`, light and dark shots in `PERSONA_SHOTS`).

Weak/offline network (`ConnectionTests`, `docs/apple/ios.md` ("Connection status")): `PORT=18760 python3 scripts/mock/mock_hub.py &`,
`TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18760 xcodebuild test … -only-testing:PersonaUITests/ConnectionTests`: 16-second outage, title unchanged first 8-second grace (`docs/operations/hub-restart.md`), then Connecting,
restored on return; 0.2-second outage (~1-second reconnect) never changes title. Offline send says Auto-send when connected, received once after recovery,
even further drops/background (no background reconnect/send until foreground)/App relaunch (disk outbox). Server restart dedup: `TestSendRetryAfterRestartAndRefusal`.
Planned restart (#1634): `MOCK_RESTART=1`, `GET /mock/restart?gap_ms=1500&missed=todos,inbox` emits `hub_restarting`, closes all WS with 1001;
new connections queue during gap like socket activation backlog, sequenced `missed` broadcasts replay after hello. `&drop_post=1` executes next op-id write without reply; retry same id returns first result.
`GET /mock/restart` shows restart count, resume per connection, op-id requests/GET counts (no extra `GET /api/v1/interactions`, etc. after resume).
Controls: `GET /mock/down?s=<seconds>` drops WS and closes unanswered new connections; `GET /mock/delay?s=<seconds>` delays all requests/pongs, 0 disables,
also startup `MOCK_DELAY=<seconds>`. Kill only listener: `kill $(lsof -i TCP:18760 -sTCP:LISTEN -t)`—
`lsof -i :18760` also lists connected simulator App, killing both loses App. Shots `-showConnection …` (`docs/apple/ios.md`).

File sending (`FileUploadTests`: Files in + menu, progress/cancel above input, finished path appended after typed text without sending) uses mock:
`PORT=18771 MOCK_UPLOAD_KBPS=1024 python3 scripts/mock/mock_hub.py &`, `TEST_RUNNER_PERSONA_UPLOAD_HUB=http://127.0.0.1:18771 xcodebuild test … -only-testing:PersonaUITests/FileUploadTests`
(`GET /mock/uploads` lists received files; `MOCK_UPLOAD_OLD_NODE=1` old node). `-openUploadDemo <filename>` uploads fake 24 MB file after 3 seconds.
System file-picker selection isn't automated; XCUITest unreliable there.

### Machine pages (`MachinePagesTests`, `MachinePagesLiveTests`)

Actually tap every machine entry: Settings › Machines (list, pull-refresh, card→details, Agent→chat, Show All for 36 Agents, offline explanation, reconnect),
Agents top-left menu (group/filter by machine, all), project machine name→machine projects (project→page, ⓘ→details, offline empty state),
new chat (offline disabled, directory browser/recent/launchers, POST contents, errors above start button; models: `/model` list/search, only supported effort after selection, hardcoded `--model` launcher locked, Codex model selection no effort; mock `…/launchers/{name}/models` via `MOCK_MODELS_SECONDS`).

- Mock: `PORT=18773 MOCK_MACHINES=1 python3 scripts/mock/mock_hub.py &` (extra offline Lab host, `POST …/reconnect` makes online;
  Mac Studio 36 Agents; `GET /mock/hits`, `/mock/reconnects`, `/mock/started`, `/mock/reset`; cwd ending `/broken` starts return 502),
  `TEST_RUNNER_PERSONA_MACHINES_HUB=http://127.0.0.1:18773 TEST_RUNNER_PERSONA_SHOTS=<directory> TEST_RUNNER_PERSONA_STYLE=light xcodebuild test … -only-testing:PersonaUITests/MachinePagesTests`,
  repeat dark (`simctl ui <udid> appearance dark`, `PERSONA_STYLE=dark`), every-step shots in `PERSONA_SHOTS`, failures attach image/tree.
- Real API: local test hub; **don't reconnect/create/close on production hub**. Build personad `GOOS=darwin` on hub machine, copy back;
  `docker exec persona-postgres createdb -U persona <own database>`, tunnel `ssh -L <local port>:127.0.0.1:18661`; fake HOME `~/.persona/hub.json`
  (0600) only `listen`, `token`, `database_url`, two machines: local and unreachable ssh. `herdr --session <name> server` creates isolated herdr,
  `HERDR_SOCKET_PATH` points to it, not active one. Create `code/demo` in fake HOME, then
  `TEST_RUNNER_PERSONA_LIVE_MACHINES_HUB=… TEST_RUNNER_PERSONA_LIVE_MACHINES_TOKEN=… TEST_RUNNER_PERSONA_LIVE_MACHINES_CODE=<fake HOME>/code xcodebuild test … -only-testing:PersonaUITests/MachinePagesLiveTests`.
  Recent directories come from actual local Claude records, so `-cwd.<machine id>` sets browser origin. Afterwards stop hub/tunnel/herdr and drop database.
- Other lanes may `pkill -f mock_hub.py` during cleanup: copy long-running mock under another name (`/tmp/<lane>_fakehub.py`), wrap while loop to restart if killed.
- XCTest failure terminates App, cancels requests; hub logs that `POST /api/v1/sessions` as 502 even though node started session (node doesn't follow hub cancellation).
  For isolated 502, first check if test killed App.
- What this computer can do (`MachineHealthTests`, #3018): `MOCK_DOCTOR=stale PORT=<dedicated port> python3 scripts/mock/mock_hub.py &`,
  `TEST_RUNNER_PERSONA_DOCTOR_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/MachineHealthTests`: MacBook machine page Two features need attention→Fix→confirm→All good→expand checks;
  `TEST_RUNNER_PERSONA_SHOTS=<directory>`, `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark`, three images. Restart mock before rerun (remembers repairs).

### Agents list (`AgentsListTests`)

Idle-space ordering (`QuietSpaceOrderTests`, #2443): `MOCK_QUIET=order PORT=<port>`,
`TEST_RUNNER_PERSONA_QUIET_ORDER_HUB=<mock URL>`, `-only-testing:PersonaUITests/QuietSpaceOrderTests`.
Fixture tab order 41 / 52 / 1 minutes ago must expand as 1 / 41 / 52; cards, compact list, collapse/reopen covered.
`TEST_RUNNER_PERSONA_UI_STYLE=glass|beautiful` plus actual light/dark covers ordinary cards/capsules; `TEST_RUNNER_PERSONA_SHOTS=<directory>`, `TEST_RUNNER_PERSONA_STYLE` filename-only.

Quick filters (All / Needs you / Unread / Working, counts follow machine filter), compact list (≥2.5× card rows, persisted across restart), space-title collapse (state counts remain,
persisted; collapse/expand all; collapsed-space ⋯ Open Space), space ⋯ Mark All as Seen (N), browsing Unread keeps seen entries until reselecting filter,
confirmed Mark All as Seen empties filter, phone filter bar scrolls as first row (`testFilterBarHugsTheTop`: search collapsed initially, pull opens, no fixed duplicate after scrolling;
no blurred gap at bottom/pull-refresh/chat-return/background-return/tab switch/search-return-to-top; `testFilterBarHugsTheTopBehindARestoredChat`: cold restored chat→back→pull search→scroll).
At rest, frames check filter bar touches navigation/search, **and screenshot pixels verify actual drawing**: UIKit can move contents without changing XCUITest frame,
so frame-only checks missed TestFlight 35 / 38 gaps. Search tests first pull open drawer, not mistake collapsed search for absent list.
Long-scroll uses `XCTOSSignpostMetric`; simulator reports duration, not dropped frames; inspect device too.

- Mock: `PORT=18775 MOCK_AGENTS=1 python3 scripts/mock/mock_hub.py &` (~130 sessions: platform 3 Needs you / 35 Unread / 12 Working,
  plus mostly/all-idle spaces; `seen` WS or POST changes Unread→Seen, `GET /mock/seen`, `/mock/reset`),
  `TEST_RUNNER_PERSONA_AGENTS_HUB=http://127.0.0.1:18775 TEST_RUNNER_PERSONA_SHOTS=<directory> TEST_RUNNER_PERSONA_STYLE=light … -only-testing:PersonaUITests/AgentsListTests`, repeat dark.
- `-openDraftsFresh YES` empties every chat's saved draft at launch: `SelectTextTests` read a copy by pasting it into the composer (the runner may not read the app's pasteboard), and a long draft left behind makes the composer tall and moves the chat.
- `-openAgentsFresh YES` clears remembered filter/grouping/compact/collapse at launch. Tests must change these via UI, not fixed launch args which override saved values.
- Grouping menu and long-run cap (`AgentsGroupingTests`, #5766): `PORT=<port> MOCK_AGENTS=1 python3 scripts/mock/mock_hub.py`, `TEST_RUNNER_PERSONA_GROUPING_HUB=<mock URL>`; each grouping from the capsule beside the quick filters, By Status kept after a restart, Unread's Show all; `TEST_RUNNER_PERSONA_SHOTS=<directory>` + `TEST_RUNNER_PERSONA_SCHEME=light|dark` + `TEST_RUNNER_PERSONA_STYLE=<name>` for the shots.
- Touch targets (`AgentsTouchTargetUITests`, #3753): `PORT=<port> MOCK_AGENTS=1 MOCK_WORKTREES=1 MOCK_ORCA_CHILDREN=1 MOCK_DESKTOP_APPS=1 python3 scripts/mock/mock_hub.py`, `TEST_RUNNER_PERSONA_TOUCH_HUB=<mock URL>`; every grouping in every display, scrolled from the top, checks that each row, heading and fold control is at least 44 pt tall (the fold chevron 44 pt wide too) and prints the heights it found; a second test taps 3 pt inside the top and bottom edges of a repository header, the machine heading, a state's heading, a Codex chat's Subtasks row and N more idle. `TEST_RUNNER_PERSONA_TOUCH_GROUPINGS=repo,state` and `TEST_RUNNER_PERSONA_TOUCH_DISPLAYS=avatar,list` narrow the matrix (28 lists, 7 groupings × 4 displays, take about 15 minutes); it launches in English.
- Recent flat mode (`RecentAgentsTests`): ordinary mock (no `MOCK_AGENTS`), `TEST_RUNNER_PERSONA_RECENT_HUB=<mock URL>`,
  `-only-testing:PersonaUITests/RecentAgentsTests`. Real menu switch; cross-project activity order, persistence, read/return, idle/offline history, live reorder, machine filter/search.
  Light/dark on default navigation; per-step `TEST_RUNNER_PERSONA_SHOTS=<directory>`, `TEST_RUNNER_PERSONA_STYLE=light|dark` names.
- `NavigationLink` row accessibility identifiers appear twice (cell/button); count/select nth with `app.buttons.matching(identifier:)`.
- `SearchOpenTests` formerly omitted `-dev.openChat ""`: previous open chat restored, swipe-back couldn't reach list; both failed on master too.
- Both `SearchOpenTests` couldn't find weather header/Add weather row on master (#1051): five pending requests filled first screen, lazy list hadn't built rows. `bringAboveTabBar` scroll to them, back to top before search; after return use row to verify list (original scroll position, filter absent from tree).
- `ShellAvatarTests` also omitted `-dev.openChat ""`: prior Agents chat restored, no list header/avatar, also failed on master.
- Compact-list shots (`CompactListShots`, demo not mock): All (many spaces), Mac Studio (many unread/idle spaces), Unread, Needs you, Mac mini (all idle),
  by-state, card comparison; all three text sizes (`-ui.textSize small|standard|large`; `TEST_RUNNER_PERSONA_TEXT_SIZES=standard` one only).
  `TEST_RUNNER_PERSONA_SHOTS=<directory> TEST_RUNNER_PERSONA_STYLE=light … -only-testing:PersonaUITests/CompactListShots`, repeat dark.
  Before/after: put same test file in `git worktree add --detach <directory> origin/main`, same data both sides.
- Computer sections (#2108, `AgentsByMachineTests`): default three-computer mock (`TEST_RUNNER_PERSONA_MOCK_HUB`), repository/project modes section by computer, hub order,
  no computer name repeated in group headers, computer title collapses whole section, machine filter removes headers. Repository/worktree inference: `AgentRepoTreeTests`.
  `MOCK_WORKTREES=1`: weather widget Agent in `.worktrees/widget` (idle peer),
  idle in `.claude/worktrees/fix-crash`, all-idle notes repo on MacBook. Both groupings, light/dark, all three renderings (default, `-ui.style beautiful`, compact).
  `-openWelcome YES` removes prior pairing (Keychain hub beats demo); `-demo.on YES` enters demo.
  All `launchPersona()` / `launchPinned()` launches auto-add `-openWelcome YES` when demo on without `-pairURL`, never reuse previous mock pairing.

### Stopping/closing space tabs (`SiblingTabsTests`)

- Ordinary mock, dedicated port/simulator: `PORT=<port> python3 scripts/mock/mock_hub.py`, then
  `TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/SiblingTabsTests`.
  xcodebuild variables need `TEST_RUNNER_`; test process reads stripped `PERSONA_MOCK_HUB`. Passing only the latter skips the suite.
- Verify swipe actions, long-press menu, Working cancellation, failed close retaining tab/confirmation, current close switches neighbor, other close doesn't switch, last close returns.
  Run existing create/switch regressions too; inspect light/dark. `sibling-*` attachments in `.xcresult`,
  export `xcrun xcresulttool export attachments --path <result> --output-path <directory>`; don't swallow failures writing directly from test process to host `/tmp`.
- `GET /mock/session_close` returns attempts/closed targets; `?fail=1` returns 503 unchanged. `/mock/reset` resets.
  `python3 -I scripts/mock/test_session_close_mock.py` checks retained history, other Agents unchanged, failure/reset.
- Never close real-hub sessions for tests; command exit 0 doesn't replace checking actual run/skip counts.

### Live updates don't interrupt interaction (`LiveUpdateTests`)

Constant hub updates must preserve ongoing actions: Agents ⋯ menu scrolled to machine stays open without jumping, long-press menu remains, mid-list rows stay,
search text/keyboard remain, a row's ⋯ menu stays open (#1244: rows no longer swipe); expanded work card and full-screen screenshot in working chat remain open.
Each case records body counts/main-thread time per push (`PERF` logs/attachments, before/after in `docs/archive/apple/live-updates.md`).

- Mock: `PORT=18797 MOCK_AGENTS=1 MOCK_CHURN=1 python3 scripts/mock/mock_hub.py &`; `GET /mock/churn?n=12&ms=400` pushes n rounds (each machine's session list—working text changes,
  two working Agents alternate newest—machine list, one new step per open chat, reply every 4 rounds). `&steps=N` steps/chat/round; `&final=1` ends turn after pushes (final reply, Working→done); `/mock/churned` progress. `MOCK_CHURN=1` adds first-chat screenshot +5 machines (menu scroll). `&inbox=1` changes Inbox/pending items each round; `&waiting=1` adds waiting Agent,
  answered next round (badge changes; real hub pushes these continually, session-only tests miss badge-driven rebuilds, `docs/archive/apple/live-updates.md` ("It came back")).
- `TEST_RUNNER_PERSONA_CHURN_HUB=http://127.0.0.1:18797 xcodebuild test … -only-testing:PersonaUITests/LiveUpdateTests`.
- Run the default navigation: compact iPhone uses the shell; regular-width iPad keeps its sidebar. `launchPersona()` does not choose navigation.
  `LiveUpdateTests`, `ChatFollowTests`, `ScrollUnderPushTests` must preserve these defaults in both styles.

### Pushes while scrolling (`ScrollUnderPushTests`)

During finger drag/inertia, content only follows finger/inertia despite pushes (`docs/archive/apple/live-updates.md` ("Forced refreshes while scrolling")).
Drag/fling Agents, Agent chat, Persona chat ~20 seconds while mock follows real cadence: `/mock/churn?n=10&ms=2000&inbox=1&waiting=1&real=1`,
every 2 seconds. `&real=1` alternates working text between one/two lines and finishes/restarts one Agent every third round (space reorders).

- With `-perfCounts YES`, `PerfScroll` inspects scrolling views per frame.
  - `List` cells, `ScrollView` SwiftUI row layers.
  - Records whole jumps (`jumps`, ≥100 pt also `big`), relative row movements (`moved`), onscreen removal/redraw (`replaced`),
    interrupted inertia (`stopped`), content-height changes (`resized`).
  - Attributes each anomaly to push types arriving within previous 0.6 seconds (`causes`).
  - Anomalies within 0.3 seconds of releasing held updates (after 0.5-second rest or immediate critical update) are `landed`: designed at-rest updates,
    UIKit may defer layout until next touch, not scrolling interruption.
  - Bodies evaluated during scrolling in `bodies`; all under `perf.counts.scroll`.
- Assert `big`, `replaced`, `stopped`, `defaults` = 0; Agents `moved` = 0. Both layouts/styles (`TEST_RUNNER_PERSONA_UI_STYLE=beautiful`).
  - <100 pt jumps happen without pushes (lazy row sizing/synthetic drags), excluded.
  - Glass-card pages like Machines continuously increase `moved` without pushes (effect layers move relative to content); only inspect it on lists.
- Mock: `PORT=18797 MOCK_AGENTS=1 MOCK_CHURN=1 MOCK_LONG_HISTORY=1 python3 scripts/mock/mock_hub.py &` (both chats long enough to scroll),
  `TEST_RUNNER_PERSONA_CHURN_HUB=http://127.0.0.1:18797`.
- First pair once/quit, then launch target page: changed hub pairing restarts App on assistant, ignores `-app.tab`, `-dev.openChat`.

### Stable return from chats (`AgentsReturnTests`)

- Demo mode, `-probes YES`, read `probe.agents-scroll` (`docs/apple/ios-state.md` ("Probes")).
- Cover compact shell and regular-width sidebar, four trips each: search collapsed/expanded × Back/edge swipe.
- Every return frame through 1 second afterwards must satisfy:
  - First row/navigation bar stable (range and adjacent-frame jumps ≤1pt), same as before leaving;
  - Search retains collapsed/expanded state;
  - Navigation-container y and top safe area unchanged.
- Both styles (`TEST_RUNNER_PERSONA_UI_STYLE=beautiful`). #340: shell return previously slid list down, auto-expanded search, bounced up 52pt.

### Truth and timeliness (`ScreenTruthTests`)

Displayed state must match hub promptly (`docs/apple/ios-state.md` ("Truth and timeliness")).

- `-probes YES`; `probe.agents-list` exports each visible Agent state/last line/questions; `probe.connection` header connection state.
  Each has `at` (last change), compared to push time.
- Mock:
  - `/mock/truth` current authoritative data;
  - `/mock/bump?what=rev|last|needs|ask|resolve` changes one thing, pushes whole-machine sessions/question like hub.
- Cases/limits:
  - Consistent at rest;
  - During touch, Needs you/questions visible ≤1 s;
  - Last line lands ≤0.5 s after release (assert 0.8 s including probe read);
  - Sustained touch still lands ≤5 s (assert 5.5 s);
  - Seeded random sequence (scroll/push/churn/chat navigation/foreground-background/offline pushes) converges ≤8 s; replay `TEST_RUNNER_PERSONA_SEED`;
  - Tap right after fling opens the Agent actually seen;
  - Offline title after 8-second grace (≤11 s), disappears ≤25 s after reconnect: backoff 1,2,4…max 20 s (±20%).
- Both layouts/styles, seeds 7,21,42 each.

### Demo mode (`DemoModeTests`, `DemoHubTests`)

No mock: `TEST_RUNNER_PERSONA_SHOTS=<directory> TEST_RUNNER_PERSONA_STYLE=light xcodebuild test … -only-testing:PersonaUITests/DemoModeTests`, repeat dark.
`testWalkThroughAndLeave` enters demo from pairing (`-openWelcome YES`), walks Agents (filters, compact, Claude send/demo reply, terminal, question answers,
plan, Codex, tables/links), Machines/24-hour charts, Persona (main/sidebar/scheduled tasks), Inbox (Lark/mail),
asserts no "This data isn't available in demo mode" on every page, exits via Settings, still pairing after relaunch. `testStaysAcrossLaunches` verifies demo persists.
Inspect simulator logs for missing `DemoHub` APIs:
`xcrun simctl spawn <udid> log show --last 10m --predicate 'eventMessage CONTAINS "DemoHub miss"'` (empty).
`DemoHubTests` decodes each API via `APIClient`, checks side effects rejected and messages echoed/replied.

- XCUITest still reports `isHittable` for rows covered by filter `safeAreaBar`, taps filter instead (Needs you selected). Scroll below filter/above tabs first,
  drag only one-third screen per step (`swipeUp` overshoots).
- Two Terminal buttons (expanded header under clock, compact header); choose hittable `frame.minY > 40`.

### Assistant live-card snapshots/events (`AssistantLiveViewTests`, #2024)

Dedicated loopback mock: `python3 scripts/mock/assistant_views_hub.py --port 0 --port-file /tmp/view-hub.port`, system-assigned port; read/pass
`TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port> xcodebuild test … -only-testing:PersonaUITests/AssistantLiveViewTests`.
Fixtures `schema/extension/examples/assistant/progress.json`, `progress-updated.json`: initial version 1, `POST /mock/views/update` pushes 2,
`POST /mock/views/stale` pushes 1 without changing latest snapshot. Actual App checks progress updates in place, one feed row, stale cannot overwrite; light/dark with inspected shots.

`AssistantViewsStoreTests` checks hub-group/chat isolation, monotonic versions, create/snapshot reorder, invalid ownership, reconnect, stale requests after switch, old hub without views.
`AssistantViewActionStateTests` checks revision ordering of `view_actions`/`assistant_view_action`, preserving selected content/answered state, old formats/other-event action fields, reconnect send recovery.
`AssistantViewModelTests`, `AssistantViewAPITests`: pinned confirmation version, one send per choice, unknown retries original CID, other-device records win, no auto-version-change/retry after 409 `view_changed`, ordinary actions can't override with reply. `AssistantActionSourceTests` strictly parses final source tag; code examples/bad tags/extra fields retained verbatim. `WatchAssistantFeedTests`: new fallback remains at creation location, stale can't overwrite, HTML never executes.

### Main-chat rich UI interactions/shared samples (`AssistantRichCardTests`, #2024)

`python3 scripts/mock/assistant_rich_ui_hub.py --port 0 --port-file /tmp/rich-ui-hub.port` runs an isolated loopback fixture, never real main chats/accounts. Pass port through `TEST_RUNNER_PERSONA_MOCK_HUB`. Watch shots use another instance with `--watch`, only synthetic assistant session; pass its `-pairURL`, `-openWatchItem session`, never alter running iOS fixture.

- Forms: fill, native confirmation, submit once, selected/source display, feed restores submitted values after restart.
- Body cards: shared fence parsing creates item id/ordinal key; another-device selection event prevents resubmission.
- Confirmation race: server version changes without event; stale confirmation returns `view_changed`, no reply sent; draft retained, new confirmation submits new version.
- `testAllSharedSamplesAreVisibleAndCaptured` reads decision/comparison/progress/form/chart/HTML samples individually. Pin each `AppStyle` via `TEST_RUNNER_PERSONA_UI_STYLE`, run light/dark and inspect. HTML without execution-security acceptance must use fallback; API feature isn't permission to execute.

### Assistant HTML cards (`HTMLCardTests`, `AssistantHTMLCardTests`, #2025)

No hub: `-showSampleHTMLCard YES` opens two samples (manual Run, auto-run when visible), inspect light/dark. `-showSampleHTMLCard open` link-confirmation sample.
`xcodebuild test … -only-testing:PersonaUITests/HTMLCardTests` clicks Choose B: native confirmation shows exact outgoing text, no second prompt after send (one reply/card); Run/Stop switch correctly.
Links also confirm first; cancellation suppresses further prompts in that run, rerun may ask again. Selected cards still open links; links don't consume the one reply choice.
`AssistantHTMLCardTests` uses real sandbox WKWebView: local listener receives no fetch/XHR/images/styles/iframe/WebSocket/beacon/`window.open`/navigation;
`document.cookie` empty, `window.persona` immutable, only current instance's main-frame `reply` / `open` to `persona` accepted. Unsandboxed same-page positive control reaches port.
Controlled rule-list compilation proves stale completion can't replace newer page, destroyed view doesn't load late compilation. No rules, no assistant HTML. Visibility/tab/foreground lifecycle releases WebViews; `onAppear` doesn't establish continued visibility.

### Mac HTML sandbox normal matrix (#2025)

First `eval "$(python3 scripts/persona_config.py env)"`, then `python3 scripts/test/mac_html_security.py --output /tmp/<new acceptance directory>`. Directory must not exist; never overwrite success/failure evidence. Driver extracts request types/Coordinator unchanged from current `AssistantHTMLCard.swift`, records full-file/extraction/test hashes; reuses security tests, replacing UIKit window with hidden nonactivating AppKit window, adding idle listener, five resource-probe rounds, bounded Mac network positive controls.

Build/tests use `~/.local/bin/xcode-slot`, temporary HOME/cache. Queue budget 3600 seconds separate from run budget (125 queue timeout, 124 execution timeout). Worker detaches once from Agent process tree to avoid port-discovery interference; no User-Agent filtering or stopping real node. Listener/JS callbacks have deadlines; outer harness records/cleans actual PID/PGID, prints success/failure/timeout terminal states. Afterwards removes only this `.build`, HOME/cache; source/manifest/log/XML retained.

Normal matrix proves exact Coordinator networking/navigation/bridge/public termination-callback recovery only, not full SwiftUI/`NSViewRepresentable` wiring, actual OS process killing, immediate CPU/physical-memory recovery or hard quotas. Shared login sessions never run literal loops; that needs a disposable Mac VM/dedicated login externally destroyable. Passing normal matrix doesn't enable Mac execution gate, change production or trigger TestFlight.

### Multiple hubs (`MultiHubTests`, [multi-hub.md](../protocol/multi-hub.md))

Three mock hubs model two groups, one with two hubs: A alone (Home), B/C linked (Work), C owns MacBook. Both groups reuse machine/session/interaction ids,
so wrong-hub routing is obvious. Mesh fields (`MOCK_HUB_ID`, `MOCK_HUB_NAME`, `MOCK_MESH_PEERS`, `MOCK_MACHINE_OWNER`,
`MOCK_MACHINE_NAMES`, `MOCK_MACHINES_KEEP`, `GET /mock/mesh`) documented at file top; unset output unchanged (`scripts/mock/test_mock_hub_mesh.py`).

```sh
PORT=18760 MOCK_HUB_ID=hub-a MOCK_HUB_NAME=Home MOCK_TURN_IMAGES=many python3 scripts/mock/mock_hub.py &
PORT=18761 MOCK_HUB_ID=hub-b MOCK_HUB_NAME=Work \
  MOCK_MESH_PEERS='[{"hub_id":"hub-c","name":"Work 2","endpoints":["http://127.0.0.1:18762"]}]' \
  MOCK_MACHINE_OWNER='{"macbook":"hub-c"}' MOCK_MACHINES_KEEP=server,macbook \
  MOCK_MACHINE_NAMES='{"server":"Work server","macbook":"Work MacBook"}' python3 scripts/mock/mock_hub.py &
PORT=18762 MOCK_HUB_ID=hub-c MOCK_HUB_NAME="Work 2" \
  MOCK_MESH_PEERS='[{"hub_id":"hub-b","name":"Work","endpoints":["http://127.0.0.1:18761"]}]' \
  MOCK_MACHINE_OWNER='{"server":"hub-b"}' MOCK_MACHINES_KEEP=server,macbook \
  MOCK_MACHINE_NAMES='{"server":"Work server","macbook":"Work MacBook"}' python3 scripts/mock/mock_hub.py &
TEST_RUNNER_PERSONA_HUB_A=http://127.0.0.1:18760 TEST_RUNNER_PERSONA_HUB_B=http://127.0.0.1:18761 \
  TEST_RUNNER_PERSONA_HUB_C=http://127.0.0.1:18762 TEST_RUNNER_PERSONA_HUB_A_TURN_IMAGES=1 \
  xcodebuild test … -only-testing:PersonaUITests/MultiHubTests
```

- Shots: `-pairURL <A> -pairURL2 <B> -pairURL3 <C>` (DEBUG; same address/credentials retains group id across restart), plus
  `-tab dev` merged Agents, `-tab settings -openHubs YES`, `-tab settings -openMachines YES` grouped Machines,
  `-openHub <group id or index>` current hub. `-openSession` accepts `group id|machine/session`, otherwise finds by session.
  On Xcode 27.2 put `--` before direct `simctl launch` DEBUG arguments so they reach `UserDefaults`; `MultiHubTests` uses the same separator.
- Same-group failover: Settings › hub shows In use; `GET /mock/down?s=40` or stop by port. Within ~10+ seconds another group member becomes In use, title unchanged.
- Rejected group: `GET /mock/reject?s=30` on every member drops its sockets and answers 401 to API handshakes (`s=0` clears). Apply it to every member so the group cannot fail over. `testRevokedGroupSaysPairingExpiredAndOffersPairAgain` verifies the caution dot, reason, Pair Again, and the other group staying In use; screenshots wait for the system icon-change alert to finish disappearing.
- One-group zero change: build DEBUG before in detached worktree, same mock/group args/status bar/clock, 14 pages × light/dark;
  repeat after, pixel compare. Repeated shots from same build identify noise (system spinner/Liquid Glass highlights vary per launch); exclude noise, inspect remaining differences.
  Only allowed change: hub row added to Settings Server section.

## SSH tunnels (`apple/Packages/PersonaSSH`, [network.md](network.md#经-ssh-隧道连-hub直连不通时的后备))

**Package tests** (real sshd + mock hub, no root, no local SSH-config changes):

```sh
cd apple/Packages/PersonaSSH
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer swift test   # Command Line Tools lacks Testing module
```

Each case starts `/usr/sbin/sshd -D` in a temporary directory (current user, random loopback port, own host key/authorized_keys with `restrict,port-forwarding,permitopen=`),
plus mock hub (`PORT=0`). Covers success/20 concurrent requests, first-use fingerprint confirmation matching `ssh-keygen -lf`, changed fingerprint rejection/no autoretry,
unauthorized key, unreachable target, denied permitopen, unreachable server, automatic redial after killed session, server restart/backoff recovery with same local port, WS hello/ping/pong, no listener after stop.
About 7 seconds. `swift test` output arrives at end; `PERSONA_SSH_TRACE=<file>` logs fixture starts/stops live.

**End-to-end** (real hub): dedicated local sshd as above, `ListenAddress 127.0.0.1`, authorized_keys only `permitopen="<hub address>:18660"`;
simulator DEBUG args:

```sh
# base64 SSHRoute JSON (Shared/HubTransport.swift); raw JSON would be parsed as plist
SEED=$(python3 -c 'import base64,json;print(base64.b64encode(json.dumps({"host":"127.0.0.1","port":<sshd port>,"user":"<user>",
  "target_host":"<hub address>","target_port":18660,"host_key":"SHA256:…","always":False}).encode()).decode())')
scripts/app/personactl.py launch -- -pairURL "persona://pair?url=<unreachable direct address>&token=<token>" -seedSSHTunnel "$SEED" -tab settings -openSSHTunnel YES
```

Read App-generated public key via `personactl.py find --id ssh.publicKey` (or test key `-seedSSHKey`). Unreachable pairing URL (e.g. `http://hub.invalid:18660`) tests fallback; `"always": true` with real hub URL tests Always use SSH.
Kill sshd session (`pgrep -P <sshd>` plus descendants; privilege-separation socket in child) for reconnect; replace host key for rejection.

**Tunnel-pairing UI** (`SSHTunnelPairingTests`, skips without env below): pairing address/token→Connect via SSH tunnel/server→system fingerprint confirmation/Trust and Save→Connect.

```sh
TEST_RUNNER_PERSONA_SSH_HOST=127.0.0.1 TEST_RUNNER_PERSONA_SSH_PORT=<sshd port> TEST_RUNNER_PERSONA_SSH_USER=<user> \
TEST_RUNNER_PERSONA_SSH_TARGET_HOST=<hub address> TEST_RUNNER_PERSONA_SSH_TARGET_PORT=18660 TEST_RUNNER_PERSONA_SSH_FP=SHA256:… \
TEST_RUNNER_PERSONA_SSH_KEY=<test key seed base64> TEST_RUNNER_PERSONA_SSH_HUB_URL=http://hub.invalid:18660 TEST_RUNNER_PERSONA_SSH_HUB_TOKEN=<token> \
xcodebuild test … -only-testing:PersonaUITests/SSHTunnelPairingTests
```

## iOS compatibility verification (unchanged App, hub changes)

After hub pairing/auth/WebSocket changes, connect current unchanged iOS App to local test hub (used for 2026-10-31 P0, `docs/operations/testing.md`):

- Build by simulator id (`-destination id=<udid>`, like `scripts/app/sim.sh`). Don't `-sdk iphonesimulator`: it builds watch target as iPhone too,
  both products `Persona.app`, Multiple commands produce. Without team use `CODE_SIGN_STYLE=Manual CODE_SIGN_IDENTITY=- DEVELOPMENT_TEAM=`.
- DEBUG `-pairURL` skips pairing-token/device-token exchange (`DeviceCredentials.swift`). Verify upgrade by launching with `-pairURL` once
  (Keychain config), then cold-launch no arguments for real `upgradeCredentials()`; inspect new `device_tokens` row and `pair_tokens.used_at`.
- iOS 27 `simctl openurl persona://pair?...` prompts Open in Persona, not script-clickable; don't pair this way.
- Simulator Keychain survives uninstall: second pairing may use old credentials; use new simulator or disconnect in App first.

## herdr integration

`./scripts/test/test-herdr.sh` starts independent headless `herdr --session persona-test-<pid> server`, unrelated to active herdr,
stopped by name afterwards. `herdrlive` tests compare socket/CLI, events; launch claude in new temporary directory,
verify trust dialog detected (`dialog_open` refusal), option answers (rev check, keys from current screen), empty input allowed, draft refused
(`input_draft`), unrelated text not cleared, clearing seen draft allows send. Without local Go, cross-build test binary on hub/copy back.
Requires herdr/claude locally (claude starts but receives no message).

First-prompt image delivery has a separate opt-in real-Agent test (`server/internal/node/firstprompt_live_test.go`,
#4794). Start your own named `herdr --session persona-firstprompt-<unique> server`; set `HERDR_SESSION` to that
name and `HERDR_SOCKET_PATH` to its socket, never the active server's. Then, from `server/`, run:

```sh
PERSONA_LIVE_FIRST_PROMPT=1 PERSONA_LIVE_LAUNCHER=<launcher> \
  go test -tags herdrlive -run '^TestLiveFirstPrompt$' ./internal/node -v -timeout 6m
```

It starts disposable Claude Agents in temporary directories/profiles, with no Persona hooks or hub/node service
changes, and sends a synthetic 768×768 PNG plus multiline text through `firstPrompt`. A launcher's first Bypass
Permissions confirmation is accepted only in its disposable test profile. The normal case uses real
`agent.prompt`; the fault-injected case pastes into the real terminal but omits initial Enter and swallows one
recovery Enter. Both require exactly one user prompt and image in the transcript, a model reply, and an empty
input box. `PERSONA_LIVE_ARTIFACTS=<existing directory outside the repo>` keeps redacted ANSI captures and JSON
receipts. Stop the test server by its exact session name afterwards. A fault-injected pass proves recovery, not
that the normal case reproduced the original timing race.

## Codex integration

`./scripts/test/test-codex.sh` tests real Codex question/approval cards (`docs/integrations/codex.md` ("Questions and approvals")), independent `persona-codex-<pid>` herdr
(stopped by name), temporary git repo, same node `codexAnswer` code, checks rollout/screens per case:
async questions (empty input/draft/0.163 expanded panel/already answered on computer), approvals (direct/scrolled/other tab foreground/transcript pager
with denial reason/already answered), question then approval, two Plan questions (option/free text).

- `PERSONA_CODEX_BINS="codex /path/to/other/codex"` versions, e.g. temporary npm `@openai/codex@0.163.0`;
  `PERSONA_CODEX_ARGS="-m <model>"` usable authenticated model (ChatGPT login not all models); `PERSONA_CODEX_ROUNDS` default 2;
  `PERSONA_CODEX_COLS=64` also narrow-window runs (`stty cols`, headless splits don't narrow); `PERSONA_CODEX_CASES=a,b` selected cases.
- Uses own `~/.codex` login, real tiny model calls. Trust writes `~/.codex/config.toml`, rollouts in `~/.codex/sessions`;
  both cleaned on exit. No local Go: cross-build on hub/copy back.
- `PERSONA_CODEX_DAEMON_BINS="<0.163 codex>"` daemon route (`docs/integrations/codex.md` ("Sessions on the daemon")): tests own
  `codex app-server --listen unix://<temporary directory>/app.sock`, restartable child, never active daemon. TUI `--remote … -C <directory> -s read-only`,
  node discovery disabled, only this app-server, fake hub answers/withdraws cards. Async/blocking questions, command/edit/permission approvals, denial reasons,
  computer-first/phone-first/simultaneous answers, restart, draft send; first `PERSONA_CODEX_BINS` (e.g. 0.153) alongside confirms terminal route.
  Permission tests enable `features.request_permissions_tool` in test app-server.
- Phone start (`TestLiveCodexStart`, `docs/integrations/codex.md` ("Starting from the phone")): real `session.start`, first message quotes/newlines/`$`/backticks/Chinese/thousands of characters.
  Each daemon binary via `--remote` on own server (app-server route), same with `-c` (terminal route, Connection explanation), first BINS (0.153).
  Each round: reply ≤60 s, correct thread, exact rollout first message, one session/pane,
  correct route, second phone message received after first reply; missing-command launcher errors immediately. Needs local personad (`codex-start`), built by script.
  Don't run no-argument 0.163; it joins/starts active daemon. `PERSONA_CODEX_TESTS="cards daemon start"` selects, default all.
- Each round/case prints `LIVE <version> round <n> <case>: ok in <seconds>`, failure screen; final `RESULT all: passed|FAILED`.
  Codex model-service errors (e.g. Selected model is at capacity, not Persona) log `SERVICE-RETRY` and rerun once;
  each RESULT reports retry count—include in stability reports, not pretend absent.

## New-user copy guard (`scripts/test/test_onboarding_copy.py`, #287)

Onboarding copy (guide cards, main-chat errors, login, add computer, model wizard, empty states) must not mention hub.json, token (pairing code allowed), jq, personad, TLS, node, herdr,
launcher, localhost, `~/.` ([onboarding.md](../assistant/onboarding.md) 8.1). `SCOPES` at file top: web/hub i18n in three languages, Android prefixed keys in `values` / `values-zh` / `values-ja`,
Chinese literals in some iOS/Mac Swift files (Apple has no catalog yet). Placeholders like `{name}`, `%1$s` excluded.

- Legitimate advanced/details wording goes in `ALLOW`, with location/reason.
- Known issues being fixed go in `PENDING`, issue/design work item (Android #4032, iOS M2–M4). Fixed entries trigger Remove from PENDING; no new violations added.
- Add new onboarding catalogs/key prefixes to `SCOPES`.

CI Scripts tests includes it; local `cd scripts/test && python3 -m unittest -v test_onboarding_copy`.

## Privacy scan

`python3 scripts/privacy_check.py`: repository contents/paths must not contain this machine's private values from `~/.config/persona/config.json`:
hosts, hub addresses, machine names, Apple IDs, emails; `~/.config/persona/private-words.txt` (one per line: internal projects/people/tools,
maintained locally); backtick values in `CLAUDE.local.md`. Also any private IPv4 (except documentation ranges like 192.0.2.0/24) and real-domain emails
(except `example.*`, public mailboxes, public `site/` contact). Reports type and origin only, never value:
`private value (5 chars) from config machines.id, config machines.ssh`, `from private-words.txt`, `from CLAUDE.local.md`, `from the home directory`;
a key that is not a snake_case word or is itself a private value shows as `*` (`config app_store.provisioning_profiles.*`).
Staged and range scans read the files' Git objects in one `git cat-file --batch`, NUL-separated with `-z`; Git before 2.36 has no `-z` there,
and the same requests then go one per line (#3224).
**Ordinary-word config values** (#1065): a machine id like a room's name matches every variable, comment and environment variable of that name
(`_` is a word boundary), blocking whole PRs on that machine. If such a value names nobody, add a `!<word>` line to `private-words.txt`
(case-insensitive); the hint after the hits appears only when one of them can be taken back this way. The waiver goes by value, so it also covers
an ssh alias or URL host spelled the same; it only takes back a config value made of ASCII letters. Anything with a dot, digit, hyphen, `@`, `/` or space
(hosts with a domain, addresses, emails, paths, ids), a word listed plainly in either `private-words.txt`, and the home directory stay private.
A `!` line that is not ASCII letters only is searched as written, with a warning naming its line, not its value. The waiver lives where the values are
read, so every scan that takes them from `privacy_check.py` honours it (the hooks, `merge-pr.sh`, `release.sh`, `scripts/release/check_bundle_privacy.py`).
**Names blocked on every machine** (#4967): specific vendor and internal-repository names never enter the repo, in any spelling (case, hyphens, underscores, spaces, camel case). Rules live in `BANNED_NAMES` in the script, their only location in the repo.
CI enforces them without private config. Needed capabilities belong in extensions that supply their own names; the main repo uses generic wording ("relay", "extension-provided IM connector"). Test examples concatenate pieces to avoid matching themselves.
A word listed only lowercase/uppercase/titlecase isn't matched across an internal lowercase→uppercase camel boundary (`fooBar`): ordinary-word identifiers (tokens/SF Symbols) can accidentally contain sensitive words, falsely blocking repo scans/web `prebuild` (#3819). To block camelcase too, list a boundary-bearing form (`FooBar`).
**Only tracked files** (`git ls-files`): `git add` new files first or they're missed (2026-11-05, a new design doc's machine id escaped to PR). Every mode reads what Git records: a symlink is its link text, never the file it points to; `--staged` and `--range` include type changes (a file turned into a symlink); a tracked file the worktree lacks (sparse checkout, skip-worktree, deleted but not staged) is read from the index.
Not private: loopback (`127.0.0.0/8`, `::1`, `localhost`, including protocol/ports, even configured, e.g. `http://127.0.0.1:<port>` or database `127.0.0.1:<port>`; private addresses with ports still reported), documentation/IETF protocol reserved ranges (`192.0.0.0/24`, SDK uses `192.0.0.192` as cloud metadata), configured Xcode under `/Applications` (same on Macs), defaults already in `config.example.json` (e.g. external group), repo Go module path (GitHub username included),
backtick paths/commands/config keys in `CLAUDE.local.md` (only ID/host/email/IP-like values count), and `site/` contact/site domain
(`app_store.feedback_email`, `privacy_url`, already public for App Store/OAuth). Docs say hub machine/another Mac;
example machine ids `server` / `laptop`. `release.sh` scans before packaging;
precommit hooks: `git config core.hooksPath scripts/hooks` (`pre-commit` staged content, plus `scripts/assets/gen_tokens.py --check` and the token drift tests when `spec/tokens`, `apple/Shared/Design` or the generated Android tokens are staged; `commit-msg` commit message, `pre-push` changed files/messages against the first of `origin/HEAD`, `origin/main`, `origin/master` that shares history with the pushed commit, every file and message of an orphan branch).
**Message lines** (#1107): `--message <file>` checks every line, Markdown headings included (a PR's title and body become the squash commit's message as they are).
`commit-msg` passes `--commit-msg`, which checks only what `git commit` records: when git opened an editor it skips the comment lines (hints, branch, paths) and everything from the scissors line down (the `commit -v` diff);
a `-m` / `-F` message keeps its `#` lines, so they are checked. It follows git's comment string and `commit.cleanup` (`commit_msg_lines`), but cannot see `--cleanup` or `-v` on the command line; `pre-push` still reads every recorded line.
**Block again before merge** (#3696): `scripts/hooks/merge-pr.sh <PR number>` reads the PR once (`gh pr view --json`), fetches its base and head, runs `privacy_check.py --range <fetched base sha>..<head> --message <file>` on the squash message (`<title> (#N)`, blank line, body),
then only passes to `gh pr merge --squash --match-head-commit <head> --subject <title> (#N) --body-file <body>` (exact scanned head and message merge; an edit after the scan does not reach `main`) and deletes a same-repository branch through the API. `--range <base>..<head>` checks changed head-file contents and commit messages, not worktree, in seconds. Tests: `scripts/test/test_merge_pr_sh.py` (fake `gh`).
Why not CI: private values come from local configs unavailable to CI, which only checks generic private-IP/email/npm rules; merge machine must possess values to block them.
2026-11-05–06 six scheduled releases failed initial repo scan because machine-name docs had already been merged directly with `gh pr merge`.

### npm registry guard

Parse full JSON in every `package-lock.json` / `npm-shrinkwrap.json`, checking v1 nested `dependencies` and v2/v3
`packages` `resolved` entries. Remote downloads only official public npm (`https://registry.npmjs.org`, default port/443);
reject other hosts, HTTP, credentials, query/fragment, control characters, backslashes. Valid relative `file:` dependencies/workspace links allowed;
absolute paths, remote authorities, encoded disguised remote paths rejected. Broken JSON, duplicate keys, nonstring `resolved` error; NUL can't make lockfiles skipped as binary.
Independent of local config/private words, reports file/line/type only, never URL. `.invalid` placeholders in rejection tests, no real internal domains.

- Web manual: `cd web && npm run check:privacy`.
- Build `prebuild` automatically scans repo, used for web deployment/public publishing.
- External repository refs: `python3 -B scripts/test/test_external_service_refs.py` (CI privacy job too). Tracked text cannot contain `nwverse/<other repo>` (including `#n`/links) or placeholder paths into another checkout's integration or test-fixture directories. External services connected through APIs only reference public contracts (OpenAPI/docs/error codes). Public dependencies in `ALLOWED_REPOS` with reasons; organization alone (`"publisher": "nwverse"`) doesn't count.
- Python regressions: `python3 -B -m unittest discover -s scripts/release -p 'test*privacy*.py' && python3 -B -m unittest discover -s scripts/test -p 'test_privacy*.py'`; not in `npm test`.
  Includes public Go dependency constants/classification, verification/replace/tool-failure refusal, personal values not hidden by public constants.
  Isolated HOME/Git-config temporary repos test real hooks/staged-worktree differences, never main worktree index/history.
- hooksPath is clone-local; new clones must run `git config`; files existing doesn't mean enabled.

## Expand / collapse (`UnfoldTests`, #1123)

DEBUG `-unfoldTrace YES` measures each unfold/fold (`UnfoldTrace`, `toggleInPlace`, work `toggleWork` / `toggleRun`): for 1.6 s after tap,
per-frame tapped-row position (`ChatPin`), late frames, longest frame, and drawn views with `-perfCounts YES`, in `probe.unfold`.
Cases toggle work at latest/middle, thinking/tools/BeautifulUI tool capsules inside work; logs `UNFOLD <scenario> <what>: moved … step …`.
At 6 s after launch also measures untapped `idle` control. Mock `MOCK_LONG_HISTORY=1 MOCK_AGENTS=1`.

- Don't query App during measurement: each XCUITest accessibility snapshot occupies main thread; polling 0.1 s makes taps "drop" 15–20 frames, quiet 2.2 s then reading gives 0–3.
  Disable `probe.chat-rows` 10 Hz too (`-unfoldTrace` only draws `probe.unfold`).
- Video frames authoritative (`scripts/diag/stream-scroll/vidpick.swift`); App positions can differ by one frame.
- Measure frame times optimized ("Jank measurement" below); default Debug numbers only relative comparisons.
- Mac no UI tests: `-unfoldTrace YES -showUnfoldLab YES` auto-toggles newest work twice, moves 40 pt into history, twice again;
  `UNFOLD …` to console (`NSUnbufferedIO=YES` or process kill loses buffered output). Same mock, `-openSession` Agent chat.

## First message in new chats (`FirstMessageTests`)

After sending, every 50 ms measure message→input distance (`FIRST <case> <seconds> <distance>`), assert stable (#1911): new assistant chat (New Chat page→chat takeover),
new Agent session (first send while empty, Claude starts after `MOCK_START_SECONDS`), then streamed reply. Mock `MOCK_ASSISTANT_START_SECONDS=2 MOCK_ECHO_SENT=1`.
Video `simctl io recordVideo`, extract `scripts/diag/stream-scroll/vidpick.swift`.

## Scrolling during streaming (`StreamingScrollTests`, frame-by-frame video)

Screenshots/probes aren't enough for streaming stability (probes read SwiftUI positions one beat off screen, docs/lessons/ios.md); record simulator and measure frames.
`SIM=<udid> OUT=/tmp/stream-shots scripts/diag/stream-scroll/run-ios.sh [cases…]` starts mock per case, records video, retrieves `-scrollTrace`;
`scripts/diag/stream-scroll/analyze.sh /tmp/stream-shots` measures displacement/backward jumps/large one-frame changes (reply disappears), plus last-row/input distance in following cases;
`vidpick.swift` extracts timestamps. Cases: long streaming follow, clear→final message, history during stream, ↓, upward drag during stream, running turn, working Agents. Mac: docs/apple/mac.md ("Scroll experiments").

## Jank measurement (dropped frames)

App measures frame intervals (Time Profiler for attribution, simulator attach requires `--device`, see Agents structural pushes). DEBUG `-showAutoScroll YES` starts after 12 seconds,
scrolls four history segments then four back at fast-swipe speed (3000pt / 2.5s). `FrameStats` writes
`Documents/frames.json`: hitches (>half-frame late), hitch ms/second (ratio), p50/p95/p99, longest frame.

- Optimized Debug (`SWIFT_OPTIMIZATION_LEVEL=-O SWIFT_COMPILATION_MODE=wholemodule`, separate derivedData),
  default -Onone numbers meaningless; same instrumentation on old commit in worktree for control.
- Long chat, `-loadAll YES`; ≥3 runs/version (simulator variance).
- Main-thread attribution: `sample <pid> 18 1 -file out.txt` (built-in, no Instruments authorization), aggregate Main Thread.

2026-10-26 optimization, long "Codex requirements leader" chat, 3 runs: hitch 149–245ms/s→156–171ms/s, longest 0.5–2.4s→0.5–0.9s,
own main-thread samples ~1,900→~400; remaining SwiftUI/CoreText text measurement/glyph drawing for new rows.

### Persona page (long main chat)

`MOCK_LONG_ASSISTANT=1`: 60 turns, each tools + long headed/list/table/code reply; `-app.tab assistant -showAutoScroll YES -perfCounts YES`.
Five segments, `Documents/frames-<segment>.json`: intervals, main-thread `busy_*`,
>50 ms frame times `long`, view `bodies`, push main-thread `push_ms`:

| Segment | Action | Script coordination |
| --- | --- | --- |
| idle | Wait 8 s | `GET /mock/churn?n=14&ms=500`, 14 rounds |
| type | Add one draft character/120 ms, 40 total | — |
| stream | 6 s | When `frames-type.json` appears, `GET /mock/say?stream=1&steps=60&ms=80&text=<long reply>` (hub delta every 80 ms) |
| sweep | Original 8 animated jumps (`scrollTo(point:)`, SwiftUI estimates many row heights at first frame, 50–200 ms, unlike finger scrolling) | — |
| glide | Move list `UIScrollView` per display frame, 2000 pt/s, history 4 s/back 4 s, closest to finger scrolling | — |

2026-11-01 before (master e51a1a8)/after, iPhone 17 Pro iOS 26.5 simulator, 3–4 runs each:

| Segment | Metric | Before | After |
| --- | --- | --- | --- |
| idle (14 pushes) | Total push main-thread time | 190–228 ms | 43–67 ms |
| idle | Main-thread frames >16 ms | 7–14 | 1–4 |
| idle | Reply/bubble bodies | 638 / 603 | ~30 / 12 |
| stream | Frames >16 ms | 21–40 | 7–12 |
| stream | `ChatView` bodies | 33 | 1 |
| glide | Dropped frames (>1.5 frames) | — | 0 |
| type | Main-thread frames >8 ms per character (40 chars) | 24–35 | 2–3 (after `LayoutIsland`) |

Changes: Equatable user bubbles/replies/work/input (previously all visible/lazy-buffer rows recalculated every push);
closed sidebar avoids `assistantConversations` (all sessions/machines); independent header status dot (header/menu no push rebuild);
`StreamChunks` splits streaming reply so finished blocks aren't reparsed/laid out, theme margins restore spacing; already-latest follow stops writing `ScrollPosition`.
Typing formerly relaid whole page/root per character (input/list same VStack, lazy list remeasures). Input now has separate
hosting controller for compatible desktop composers (`LayoutIsland`, `DesignSystem/LayoutIsland.swift`; BeautifulUI's externally attached input menu becomes untappable there; pass environments by value, replacing the whole environment removes accessibility controls, `ComposerAccessibilityTests`), whole page only relayouts when height changes (wrap/new notice).
iOS shell and regular-width card inputs retain their original tree: single-line→key-bar card, separate hosting controller makes long-text accessibility snapshots hang, so this isolation optimization does not apply.
`ComposerGrowTests` verifies multiline growth, above keyboard, shrink to one line, compact and regular-width iOS navigation.

### 全页面性能 UI 测试（#5730）

`./scripts/app/perf-test.sh [agents|assistant|inbox|settings|machines …]` 用优化 Debug 构建、各套件独立 mock 和共享模拟器锁运行。指定已有的专用模拟器 `PERF_SIM`，`PERF_ONLY=PerfMachinesTests/testMachineDetailScroll` 可只运行一项；`PERF_SKIP_BUILD=1` 只在确认 DerivedData 与当前源码一致时使用。编译和执行均经过 `xcode-slot`，输出目录已存在时拒绝覆盖。

| 测试文件（`apple/PersonaUITests/`） | 入口及主要跳转 |
| --- | --- |
| `PerfAgentsTests.swift` | Agents 列表、筛选、开对话、长历史/图片/输入、工作详情、子 Agent 往返、提问卡后滚动、终端、文件/代码、图片翻页、链接卡片 |
| `PerfAssistantTests.swift` | 主对话滚动/流式/输入/推送、侧栏、切会话/归档、tab 往返、任务坞、欢迎页、助手资料/定时任务、知识库、待办 |
| `PerfInboxTests.swift` | 收件箱滚动/搜索/推送、飞书与邮件详情往返、群聊/话题/线程/消息页 |
| `PerfSettingsTests.swift` | 设置滚动及 push/pop、扩展/市场/详情/搜索、定时任务及运行记录 |
| `PerfMachinesTests.swift` | 机器网格、详情 push/pop、完整历史滚动、选点/范围切换、两页前台推送和指标轮询 |

`PerfTestCase.swift` 统一 `launchPinned()`、配对、模拟器系统提示处理、原始 frame 附件及各测量入口。需要 mock 的性能测试在普通 UI 测试中跳过；跳过不算测量成功。当前清单覆盖本轮盘点的主要场景，不等于 App 的每个设置叶子页都已有独立性能测量。

Every measured run also has `main_thread_blocks`: the DEBUG watchdog's count, longest duration and five longest sampled
main-thread stacks (#4264). `PerfAssistantTests/testWelcomePageIdle` fails when this count is not zero; the other scenarios
attach the evidence without enforcing the 100 ms budget. A missing report fails that assertion rather than passing as no blocks.

机器和收件箱的 `measureVerifiedGlide()` 在 App 内固定持有一个真正可滚动的 UIKit 列表，避免 XCUITest 元素位置查询随树变化漂移；一轮显式 warm-up 加 N 轮正式样本，并验证样本归属、帧数、时长和实际位移。其他手势、输入和跳转入口仍是诊断测量：XCUITest 查询/手势开销混在窗口内，报告标成 `legacy_unverified`，不能作纯 App 的 60 FPS 证据。

`scripts/app/perf_report.py` 优先导出 xcresult 原始 frame 附件，保存各轮与来源；缺样、失败、工具错误不写成通过。FPS 是 CADisplayLink 回调 cadence，不是 GPU 呈现帧率；hitch 是自定义迟到时间，run-loop busy 不是 CPU 时间。看最低 cadence、最差 hitch 和最长帧，不能只看中位数。`--final-acceptance` 只检查样本完整性，不证明稳定 60 FPS。回归测试：`python3 scripts/app/test_perf_report.py`。

### Whole App (any page)

DEBUG `-showPagePerf YES`: after 10 s measures 8 s idle (`frames-page-idle.json`; writes `Documents/page-idle-started`,
then script `GET /mock/churn?n=12&ms=500` keeps pushes in segment), largest visible scroll view glides down 3 s/back 3 s at 2000 pt/s,
`frames-page-glide.json`. Target args: Work `-tab dev -app.tab chats`, Inbox/Machines `-openShellPage inbox|machines`,
Agent chat `-openSession <machine>/<session>`. `MOCK_AGENTS=1` ~130 Agents reveals list cost.

2026-11-01 iPhone 17 Pro simulator, 2 runs/page:

| Page | Total main-thread time for 12 pushes | Frames >16 ms (idle/glide) |
| --- | --- | --- |
| Work (130 Agents) | ~375 ms→82–88 ms | 15→1–4 / 1–2 |
| Inbox | 19 ms | 6 / 8 |
| Machines | 44 ms | 2 / 7 |
| Long Agent chat (`MOCK_LONG_HISTORY=1`) | 37 ms | 6 / 6 |

Work change: hub pushes sessions separately per machine, previously each recalculated session-reading pages. Coalesce arrivals within 25 ms
(`AppModel.applyPendingSessions`), ~4 SwiftUI updates→1 per round.

### Lark pages (chat/Inbox, #674)

`-showPagePerf YES -perfCounts YES`; `MOCK_LARK_RICH=1` makes every third f3 history message a card/image/rich text, like busy work group.
Chat `-tab mail -openInboxItem lark:f3:300`, Inbox `-tab mail`. After `page-idle-started`,
`GET /mock/lark_churn?feed=f3&n=14&ms=500&new=1`: hub-like `{"type":"lark"}`, new other-person message each round; Inbox add `&inbox=1`.
`GET /mock/lark_churned` round count.
`MOCK_INBOX_DELAY_MS=<milliseconds>` delays `/api/v1/inbox` like slow hub; background refresh during pushes mustn't jump list (#1947).

2026-11-03 P-19 (every chat push refetched latest page, decoded 40 messages on main). iPhone 17 simulator, before/after 3 runs in same period:

| Segment | Metric | Before | After |
| --- | --- | --- | --- |
| idle (14 pushes/new messages) | Main-thread frames >16 ms | 2 / 4 / 0 | 0 / 1 / 0 |
| idle | Main-thread frames >8 ms | 16 / 13 / 12 | 4 / 8 / 7 |
| idle | Dropped frames (>1.5 frames) | 3 / 0 / 0 | 0 / 1 / 0 |
| idle | Main-thread `LarkMessages` decode samples (`sample`, 1 ms) | 43 | 0 |
| glide | Dropped frames | 8 / 7 / 13 | 7 / 9 / 8 (scroll unchanged) |

Changes: chat/thread pages decode off-main (`APIClient.getDecodedOffMain`);
open chat receives own change at most once/second (`LarkSync.chatGap`, `LarkChangeGate`), bursts coalesce, first after quiet immediate.

2026-11-03 P-20 (appearing/disappearing rows wrote page `@State`, recalculating whole body). master `995e33a5` (after #777),
2 alternating rounds ×3 runs each, load 16–60:

| Segment | Metric | Before | After |
| --- | --- | --- | --- |
| glide | Dropped frames | 17 / 10 / 21, 8 / 10 / 13 | 6 / 4 / 5, 3 / 3 / 5 |
| glide | Main-thread frames >16 ms (6 total) | 48 | 11 |
| glide | Main-thread busy p95 | 10.6–15.4 ms | 6.9–7.8 ms |
| idle (14 pushes) | Dropped frames | 1 / 0 / 0, 0 / 1 / 0 | All 0 |

Changes: seen records, read-report timing, receipt bookkeeping in `@State` class `LarkChatBook`, mutation doesn't invalidate view;
visible new-message count/receipts remain state, written only on change. Mock read reports identical in both measurements.
Remaining drops mainly SwiftUI layout of new card rows: cards-only 5–7, image/rich-text-only 0–4.

### Agents structural pushes (#343)

2026-11-05 master `870b0873f`, iPhone 17 Pro, `MOCK_AGENTS=1` (140 Agents, 19 Working), `-showPagePerf YES`,
11 alternating before/after rounds (each churn changes space order/list structure), medians, high load 60–79.
Another 11 rounds at load 25–60 had extra `.equatable()` around constructed rows (lessons/ios.md); parenthesized:

| Segment | Metric | Before | After |
| --- | --- | --- | --- |
| idle (12 pushes) | Hitch time (ms/s) | 27.6 (26.6) | 18.9 (10.4) |
| idle | Dropped frames | 12 (11) | 9 (5) |
| idle | Longest-frame main-thread time | 32.4 ms (33.6) | 25.0 ms (23.5) |
| glide | Hitch time (ms/s) | 13.8 (10.2) | 16.7 (11.3), unchanged scrolling, within noise |
| 40 pushes (Time Profiler, one each) | Main-thread total | 1234 ms | 825 ms |

Changes: structural list body rebuilt row modifier chains (swipe/menu/link), one-fifth of each push;
`KeyedRow` keeps unchanged `SlotRowKey` chains (row `FeedRow` still reads live last line/time).
Compute filtered Agents once (invisible phone filter was constructed and filtered all Agents twice), no `@StoredValue` reads inside each Agent closure;
`AppModel.siblings(of:)` sorts only observed spaces, not all per push.
Contemporaneous long/streaming chats, Inbox, Knowledge measured near 0 (chat sweep artificial large jumps), unchanged.

Time Profiler works: `xcrun xctrace record --template 'Time Profiler' --device <udid>
--attach <pid> --time-limit 20s`, pid from simctl launch; simulator attach needs `--device`.
`xcrun xctrace export --xpath '…/table[@schema="time-profile"]'` XML, aggregate main-thread stacks containing `UIApplicationMain`; without device says process not found.

## Sidebar dragging (Persona tab)

Per-frame main-thread timing without Instruments (local xctrace stuck on authorization): DEBUG `DrawerProbe` (`-showDrawerProbe YES`), drag start through
0.6 s after release, run-loop afterWaiting/beforeWaiting (after Core Animation commit) measures busy time + CADisplayLink intervals,
separate drag/post-release stats in `Documents/drawer-probe.jsonl`; `DrawerPan` logs rejected/dropped touches and reasons.

- Gestures: `DrawerSwipeTests`, `MOCK_DRAWER=1` adds wider-than-screen code block, diagonal/bubble/after fling/code-block drags.
- Don't benchmark under XCUITest: accessibility adds per-frame SwiftUI work. `-showDrawerAutoDrag <rounds>`, simctl launch without XCUITest:
  each round open/release, close, halfway release/bounce, fling, tap close, same `AssistantDrawer.dragBegan / drag / letGo` per display frame.
  Optimized Debug (`SWIFT_OPTIMIZATION_LEVEL=-O SWIFT_COMPILATION_MODE=wholemodule`, separate derivedData); old worktree
  same `DrawerProbe.swift` and three named methods.
- Sidebar contrast (#4496): `SidebarContrastTests` measures left-half Inbox-row luminance difference (normally >0.9, white-on-light only a few percent), repeated toggles/home-return,
  `-showLookBlink 3` switches window appearance/layout/back every 3 seconds like background snapshots. `testRowsFollowALookChange` needs `TEST_RUNNER_PERSONA_FLIP_LOOK=<file>`;
  test writes light/dark, harness `simctl ui <udid> appearance` switches/deletes (skip if absent). `TEST_RUNNER_PERSONA_SCHEME=light|dark` adds `-ui.scheme`.
  Deterministic `DrawerSidebarLookTests`: window appearance wins over mismatched page environment.
- Main-thread sample `sample <pid> 30 1`; locate own simulator `pgrep -f "Devices/<udid>/.*Persona.app/Persona"` (other lanes run App too).

2026-10-31 before master e4fca3b/after, iPhone 18 Pro Max 60 Hz, long chat, 10 per action (5 rounds ×2 launches):

| Action | Version | Count | Drag main-thread p50/frame | Drag p95 | First frame | Longest drag | Longest after release | Drops (drag/after) |
|---|---|---|---|---|---|---|---|---|
| Open | Before | 10 | 6.6 ms | 27.6 ms | 28.5 ms | 107.3 ms | 13.8 ms | 14 / 0 |
| Open | After | 10 | 3.8 ms | 5.2 ms | 2.5 ms | 28.8 ms | 30.7 ms | 2 / 2 |
| Close | Before | 10 | 6.7 ms | 9.1 ms | 4.3 ms | 15.2 ms | 29.8 ms | 0 / 18 |
| Close | After | 10 | 4.0 ms | 4.8 ms | 3.8 ms | 15.1 ms | 21.5 ms | 1 / 2 |
| Halfway release | Before | 10 | 6.4 ms | 24.0 ms | 24.3 ms | 35.5 ms | 23.3 ms | 12 / 20 |
| Halfway release | After | 10 | 3.8 ms | 5.0 ms | 2.2 ms | 14 ms | 8.1 ms | 0 / 0 |
| Fling | Before | 10 | 10.8 ms | 25.9 ms | 23.2 ms | 35.6 ms | 8.7 ms | 12 / 0 |
| Fling | After | 10 | 3.8 ms | 12.8 ms | 2.2 ms | 15.2 ms | 15.1 ms | 1 / 1 |

First frame = drag start, formerly builds sidebar/hides tab bar (safe-area whole-page relayout); longest post-release formerly final close frame restoring tabs.
Total drops (>1.5 frames) 76→9.
Real XCUITest drag (`testMeasureDrags`, `PERSONA_DRAWER_MEASURE=4`): after, 16 drags main-thread p50 ~4 ms, max 15, all flings open;
before first fling failed (495pt/s below old 600 threshold), first frame 18.6 ms, close 40–50 ms.
Simulator runs Mac CPU, absolute times smaller than device; compare relative changes. 120 Hz device has 8.3 ms/frame.

## Push replay (`TestPushReplay`)

Compare same real pushes before/after hub session-list/Live Activity timing changes:

1. Record on hub: `cd server && go run ./cmd/pushrec 20m ~/push.jsonl`. App-like hub connection (token from `~/.persona/hub.json`, never printed; `listen` address, `PERSONA_LISTEN` / `PERSONA_LISTEN` override),
   no phone/desktop registration or chat opening, `{"t": milliseconds, "msg": original}` per message. Contains real session titles/last lines, 0600, **stays on that machine**, never copied/committed.
2. Replay `PERSONA_PUSH_RECORDING=~/push.jsonl go test ./internal/hub -run TestPushReplay -v`: before-recorded/after-generated
   counts/bytes per minute; timestamped Live Activity changes pass through coalescing/throttling, report notifications/hour with App closed and longest island/hub mismatch, fail beyond limits.
3. Real `broadcastSessions` decides sends, matches byte-comparison results (decode retains fields); parallel incremental App (`sessions_delta`) counted.
4. Background `AgentSync` fetches: `python3 scripts/diag/agentsync_replay.py ~/push.jsonl`, recorded rev/state (45/min→9/min, `docs/apple/ios.md` ("Information architecture")).

19-minute recording 2026-11-02 (battery investigation, `docs/apple/live-activity.md` ("Push cadence"), `docs/server/server.md` ("Traffic pushed to the app")):

| | Before | After |
| --- | --- | --- |
| `sessions` (old App, whole list, unchanged skipped) | 36.9/min, 2605 KB/min | 29.7, 2452 KB |
| `sessions` (new App, incremental) | Same | 29.7, 79 KB |
| All messages (new App) | 104.9/min, 2616 KB/min | 97.7, 90 KB |
| Connection session snapshot | 602 KB | 227 KB (no Codex history) |
| Live Activity (App closed) | 188/hour, mismatch max 2.5 s | 115, max 29.2 s (60-second throttle: 60, 58.8 s) |
| Live Activity (App open) | Same | 0 |

## Session list reads (#3759)

How often a client reads `GET /api/v1/sessions`, and what kind of read, while it sits open on a paging hub.

- The mock in paging mode with a list about the size of a busy hub: `MOCK_SESSIONS_LIST=1 MOCK_AGENTS=2 PORT=<port> python3 scripts/mock/mock_hub.py`
  (266 sessions, a 207 KB complete list). Its `since` answers from a journal of the last 128 revisions (changed, added and
  removed rows, rows entering or leaving a filter; an older one is `409 revision_expired`), and each `sessions_list` hint
  carries the list's current revision, once per change.
- `GET /mock/sessions_reads` counts the reads by client (the `X-Persona-Client` platform: `web`, `ios-app`) and kind: `full`
  (no paging query), `baseline` (a page without `since`), `since`, `counts`, `lookup`, with bytes, plus the hints sent;
  `?reset=1` starts again.
- `GET /mock/churn?n=120&ms=500&rows=1`: a minute in which one working agent changes every half second (a real hub changes a
  row or two a second); without `rows` every working agent changes every round.
- Web: `node web/e2e/session-list-reads.mjs <web URL> --hub <mock URL> [--max-full 0]` (the mock's default pins keep the
  sidebar on its complete fallback): a quiet minute, then a churn minute; fails when the churn minute has more than
  `--max-full` complete lists.
- iOS: launch with `-pairURL persona://pair?url=<mock>&token=mock -app.tab chats -openAgentsFresh YES` (the Agents tab in the
  default By Repository grouping, which needs every row), then read the counters around the same two minutes.
- A real hub's `traffic` journal lines count by route template, not query: there a complete list and a delta are told
  apart only by `body_bytes / count`.

## Live Activity screenshots (`LiveActivityShots`)

`simctl io screenshot` doesn't capture Dynamic Island/lock-screen activities. `LiveActivityShots` uses `-showFleetSample` for fictional activity,
home compact state (two shots 0.4 s apart for animation), long-press expanded (sometimes retry `expanded2`), lock/wake screenshot (first Allow Live Activities prompt handled), then unlock:

```sh
TEST_RUNNER_PERSONA_FLEET_SAMPLE=needsYou TEST_RUNNER_PERSONA_FLEET_DETAILS=YES TEST_RUNNER_PERSONA_FLEET_ISLAND=custom:assistant,load xcodebuild test … -only-testing:PersonaUITests/LiveActivityShots \
  -resultBundlePath /tmp/shots.xcresult
xcrun xcresulttool export attachments --path /tmp/shots.xcresult --output-path /tmp/shots
```

`PERSONA_FLEET_SAMPLE=current` doesn't launch App, captures current activity (real APNs, `docs/apple/live-activity.md` verification).
Shared island: `scripts/app/island_peer.sh <simulator udid>` installs sample `apple/IslandPeer` (one "8 min" activity),
add `TEST_RUNNER_PERSONA_ISLAND_PEER=dev.example.islandpeer`, extra `-shared` coexistence and `-landscape` (sample foreground landscape, Persona alone on island);
`TEST_RUNNER_PERSONA_FLEET_COUNT=12` changes fake counts to 12 for two/three digits. iPhone 18 Pro/17 Pro differ, screenshot both (`docs/apple/live-activity.md` ("Sharing the Dynamic Island with other apps")).
