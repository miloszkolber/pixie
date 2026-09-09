# Pixie roadmap changelog

This file records roadmap work that has shipped in the checkout. [execution.md](execution.md) remains the status ledger. A completed entry names the implementation commit, observed behavior, verification commands and remaining boundaries.

## 2026-09-08

### FIX-01 — Production assistant restart

- **Implementation:** `6425e70` wires the production assistant entrypoint to the restart lifecycle, adds a fresh `bootId` beside stable `runtimeId`, blocks new work as soon as restart is accepted, and requires an executable termination hook. The entrypoint drains through `host.close()` and exits with status `75` for service-manager restart, with a 25-second bounded escalation. Normal signals retain a clean exit on successful teardown and report forced or failed teardown as an error.
- **Behavior evidence:** the process regression starts the real `assistant/src/main.ts`, restarts it on the same port, verifies the reply-before-exit path, stable runtime identity, changed boot identity, changed PID and persisted native session discovery after the new process starts.
- **Verification:** `bun test tests/pixie-assistant/runtime-restart.test.ts` (4 pass, Bun 1.3.14); `bun test tests/pixie-assistant/protocol-conformance.test.ts` (5 pass, Bun 1.3.14); `bun run typecheck`; `bun run --cwd assistant typecheck`; `CGO_ENABLED=0 go test -count=1 ./...`.
- **Remaining boundary:** combined full-host restart, systemd unit behavior, managed descendant reaping and final-container drain remain `BUILD-05` evidence. This change does not publish artifacts or change deployment state.

### FIX-02 and FIX-03 — MCP module persistence and lifecycle

- **Implementation:** `c5490a4` and `5a781e1` make module enablement a no-op when the requested value is already committed, publish the in-memory desired state only after persistence succeeds, preserve the prior Browser runtime on known pre-publication write failure, and add an explicit `mcpRegistry.moduleRestart` operation. Explicit restart builds the replacement service before retiring the prior handle and does not rewrite desired state.
- **Behavior evidence:** regression fixtures verify unchanged enablement keeps an existing Browser handler usable, explicit restart invalidates only the old handler while the replacement is ready, and a deliberately unwritable backup path leaves the primary file, catalog, readiness and Browser route unchanged.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./...`; `CGO_ENABLED=0 go vet ./...`; `bun run --cwd package typecheck`.
- **Remaining boundary:** post-rename/directory-sync uncertainty and complete-map mutation reconciliation remain `FIX-13`/`EXT-02` work. Module startup and shutdown still require the broader no-lock-held lifecycle work in `EXT-02`.

### FIX-08 — Assistant loopback binding and startup validation

- **Implementation:** `348df29` adds shared startup validation for the assistant entrypoint and server. Production CLI input accepts only `localhost`, `127.0.0.1` or `::1`, parses ports as strict integers from `1` through `65535`, and validates the secret before agent-directory creation. Direct host startup validates its runtime port (`0` remains available for ephemeral tests), host and secret before creating state or taking the host lock.
- **Behavior evidence:** the subprocess regression rejects `--host 0.0.0.0` and `--port 65536` without creating the assistant state directory. Unit coverage rejects DNS/remote/bracketed host forms and coercive port values including whitespace, negatives, fractions, hexadecimal and non-finite strings.
- **Verification:** `bun test package/tests/pixie-assistant/startup-validation.test.ts` (3 pass, 36 expectations, Bun 1.3.14); `bun test package/tests/pixie-assistant` (73 pass, 403 expectations, Bun 1.3.14); `bun run --cwd package typecheck`; `bun run check:filenames`; targeted Biome check; `git diff --check`.
- **Remaining boundary:** controller `GO-01` host authority and separate remote Origin/authentication policy remain independent work. The repository-wide lint command still reports pre-existing diagnostics outside this change.

### FIX-04 — Guarded chat creation and stale selection rejection

- **Implementation:** `3f5f014` moves new-chat admission into `workspace/navigation/start-chat.ts`. One in-flight `session.create` promise is shared per project area, successful replies must still match the connected generation, active project area, navigation tick and route generation, and failures emit one error without changing navigation state.
- **Behavior evidence:** four focused regressions cover duplicate activation producing one request and one selected tab/runtime, an older connection reply being ignored, a newer selection rejecting the old reply, and duplicate failures producing one toast. Existing workspace tests cover the selected-chat and empty-panel render paths.
- **Verification:** `bun test package/tests/webui/workspace/start-chat.test.ts` (4 pass, 16 expectations, Bun 1.3.14); `bun test package/tests/webui/workspace` (44 pass, 332 expectations); `bun run --cwd package typecheck`; targeted Biome check; `bun run check:filenames`; `git diff --check`.
- **Remaining boundary:** the full Web UI suite currently has 23 unrelated failures under Bun 1.3.14, mostly `NameTooLong` data-URL resolution failures in existing chat/tool renderer tests. Per-selection loading/error presentation beyond the existing restoration fallback remains part of `UI-03`/`UI-06`.

### FIX-05 — Strict v1 host envelopes and handshake

- **Implementation:** the assistant WebSocket host now rejects non-object envelopes, coerced/non-safe IDs, empty methods, non-object params and calls made before the negotiated v1 `runtime.hello`. The existing v1 duplicate in-flight ID behavior remains an error frame with the connection open; browser/native ID domains are untouched. Test clients now perform the documented handshake, and `docs/pi-protocol.md` matches the enforced contract.
- **Behavior evidence:** conformance regressions cover malformed JSON, invalid/coerced IDs, array or missing handshake params, missing hello, unsupported protocol version, duplicate in-flight IDs and unknown methods after hello. Existing assistant, restart and extension-inventory flows pass through the handshake.
- **Verification:** `bun test package/tests/pixie-assistant` (75 pass, 409 expectations, Bun 1.3.14); `bun run --cwd package typecheck`; `bun run --cwd package check:filenames`; targeted Biome check; `git diff --check`.
- **Remaining boundary:** this checkout still speaks host protocol v1. Host v2 schemas/epochs and its distinct duplicate/handshake settlement behavior remain `API-02` work; native payloads remain forward-compatible after strict Pixie envelope validation.

### FIX-06 — Native thinking levels, including max

- **Implementation:** the assistant preference bridge, Go Pi administration projection, shared `PiPreferences` contract and settings selector now accept the native `max` thinking level. Session clamping orders `max` after `xhigh`, while scheduled sessions continue to omit an override and inherit Pi's configured default.
- **Behavior evidence:** assistant RPC preference save/read/reset accepts `max`; controller preference normalization and save paths preserve it; session mutation and model clamping regressions cover `max` and native ordering; the settings regression exposes `max` in the global selector.
- **Verification:** `bun test package/tests/pixie-assistant/server.test.ts package/tests/webui/settings/pi-settings.test.ts` (11 pass, 65 expectations, Bun 1.3.14); `CGO_ENABLED=0 go test ./...`; `CGO_ENABLED=0 go vet ./internal/controller ./tests/go/controller`; `bun run --cwd package typecheck`; `bun run --cwd package check:filenames`; targeted Biome check; `git diff --check`.
- **Remaining boundary:** thinking levels remain native/model-dependent; unknown advertised values stay forward-compatible at the session protocol boundary and are not ranked into this fallback scale. Scheduled sessions intentionally inherit the configured native default rather than adding a second schedule-specific setting; native-installation and full-host compatibility evidence remains `GO-03` work.

### FIX-07 — Agent revisions, exclusive create and rooted edits

- **Implementation:** `f6404a6` adds content-hash revisions to native agent sources, requires the revision for controller update/delete mutations, preserves unknown frontmatter, rejects stale edits/removals, rejects symlinked agent roots, and uses an exclusive hard-link publication for create so concurrent creators cannot overwrite one another. The UI carries the opaque revision through edit/delete requests.
- **Behavior evidence:** assistant regressions cover metadata preservation, oversized-write no-change, external-edit conflict without clobber, stale delete rejection, concurrent create exclusivity and symlinked-root refusal. Native parity and Go integration cover revision round-trips and a stale controller update after an external file edit.
- **Verification:** `bun test package/tests/pixie-assistant/sessions.test.ts package/tests/pi-native-parity/extension-matrix.test.ts` (26 pass, 88 expectations, Bun 1.3.14); `CGO_ENABLED=0 go test ./...`; `CGO_ENABLED=0 go vet ./...`; `bun run --cwd package typecheck`; `git diff --check`.
- **Remaining boundary:** revision checks serialize Pixie/controller edits and reject observed stale forms, but an unrelated editor that ignores the lock can still race after the final precondition; this is an explicit writer limitation, not a claim of universal compare-and-set. The broader native-parity suite still has two pre-existing Bun/upstream child-runtime failures.

### FIX-09 — Bounded assistant lifecycle deadlines

- **Implementation:** the assistant now shares explicit startup, administration and 25-second drain deadlines across host construction, provider/extension operations, in-flight dispatches, native session construction/opening and session/capability teardown. Raw dispatch promises remain tracked after an RPC deadline so a timed-out operation is still included in forced shutdown. Native abort stays bounded to ten seconds within the overall drain, and forced or uncertain shutdowns reject with the deadline outcome after disposing local session state.
- **Behavior evidence:** lifecycle regressions cover a stalled construction, extension teardown, hung capability administration and provider administration I/O. The tests use short injected deadlines and verify cleanup returns promptly while unresolved work remains visible to the service-drain deadline.
- **Verification:** `bun test package/tests/pixie-assistant/lifecycle-deadline.test.ts` (4 pass, 11 expectations, Bun 1.3.14); `bun test package/tests/pixie-assistant/server.test.ts package/tests/pixie-assistant/session-lifecycle.test.ts` (12 pass, 104 expectations); `bun run --cwd package typecheck`; `bun run --cwd assistant typecheck`; targeted Biome check; `git diff --check`.
- **Remaining boundary:** full composition drain, managed native descendants/pipes, systemd and final-container reaping remain `GO-07`/`BUILD-05` work. The full assistant suite also has an environment-level distribution fixture failure when Bun cannot copy its temporary production install because the filesystem reports `ENOSPC`.

## 2026-09-09

### FIX-10 — Controller Host authority and MCP routing

- **Implementation:** `d8e1367` enforces a configured Host allowlist independently from normalized Origin/proxy policy across HTTP, WebSocket, and module/file/static routes, ignores untrusted `Forwarded`/`X-Forwarded-*` headers except an explicit trusted-proxy `X-Forwarded-Proto: https` with Host rewrite from listed peer CIDRs (`PIXIE_TRUSTED_PROXY_CIDRS`), serves in-process Browser status through the merged registry without an external URL, reserves `/api/*` and `/mcp/*` to non-SPA JSON errors, and fails MCP publishing closed without a strong token. Config and docs (`.pixie.example`, `docker-compose.yaml`, `docs/deployment.md`, `docs/security.md`) describe the loopback exception, exact public origin, and proxy policy.
- **Behavior evidence:** new `runtime_authority_test.go` covers loopback/localhost, remote missing origin/auth, remote authenticated, explicit no-auth, trusted proxy with and without auth, unsupported hosts, and zero/oversized ports; expanded `auth_test.go`, `websocket_test.go`, `static_test.go`, `project_images_test.go`, and `artifacts_test.go` exercise the real routes, and `runtime_status_test.go` covers the merged-publisher Browser status.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/controller ./tests/go/controller` (both ok); integrated `CGO_ENABLED=0 go test -count=1 ./internal/controller ./tests/go/controller ./internal/workspace ./tests/go/workspace ./tests/go/persist ./tests/go/mcpserver` (all six ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** full `go test -race ./...`, real listener/TLS-proxy deployment checks, both-architecture artifacts, systemd/Docker behavior, and cross-boundary X01 evidence in both deployments remain later-stage work.

### FIX-11 — Read-only Git inspection and termination

- **Implementation:** `4ff8431` runs Git with a sanitized environment (`GIT_CONFIG_NOSYSTEM`, null global config, ceiling directories, disabled hooks/external diff), compares staged index entries and modes against the base tree with bounded raw worktree hashing (4 MiB per file, 64 MiB aggregate) and an explicit raw-bytes conversion note, contains submodule `.git` gitfiles to admitted metadata, resolves an absolute Git executable, and terminates helpers through process-group TERM/KILL with bounded pipe draining.
- **Behavior evidence:** new `git_exec_test.go` proves clean/process/fsmonitor marker helpers are not executed through the wrapper; new `git_diff_test.go` covers per-file and aggregate hash budgets plus preview bounds; expanded `tests/go/workspace/git_test.go` covers staged-only/index semantics, gitfile containment, shadow-PATH rejection, linked-worktree boundaries, and bounded warnings; `GitRepository.warnings` surfaces raw-mode limits in the UI contract.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/workspace ./tests/go/workspace` (both ok); integrated six-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** the full X02 endpoint matrix (includes, nested attributes, worktrees, submodules, encodings), full race suite, browser QA, and final-container entrypoint reaping (`FIX-12`/`BUILD-05`) remain later-stage work.

### FIX-12 — Bounded child termination and container reaping

- **Implementation:** `a5e11fb` adds bounded TERM/wait/KILL escalation for Git process groups (never signalling outside the managed group), finite pipe-drain timeouts, and a `tini`-based final-container entrypoint so descendants are reaped; `runtime_drain_test.go` documents the one-way bounded controller drain.
- **Behavior evidence:** new `tests/go/diagnostics` regressions cover a TERM-ignoring descendant requiring group KILL within bounds, a pipe-retaining child where leader exit plus close unblocks the drainer, and a final-image entrypoint assertion keeping `tini` ahead of `/app/pixie`.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/workspace ./internal/diagnostics ./internal/controller -run 'Git|Drain|Reap|Diagnostic|Terminate'` (pass) plus `go test -count=1 ./tests/go/diagnostics` (4 pass); integrated seven-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** controller-spawned non-Git child escalation, Windows single-process limits, full race suite, and live systemd/Docker drain behavior remain later-stage work.

### ROUTE-01 — Assembled reserved routing without SPA fallback

- **Implementation:** `073a487` normalizes the top-level route once in the assembled HTTP handler so dot-segments, duplicate slashes, and parent traversal cannot bypass the reserved `/api/*` and `/mcp/*` namespaces; unknown paths in those namespaces return non-success JSON and never the SPA document, while static fallback stays limited to frontend navigation.
- **Behavior evidence:** new `tests/go/controller/routing_test.go` exercises the real assembled handler with a real temporary static index, proving `//api/unknown`-style evasions returned SPA before the fix and JSON after, with and without a registry.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/controller ./tests/go/controller -run 'Static|Route|Fallback|NotFound|SPA'` (pass); integrated seven-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** full race suite, both-architecture artifacts, systemd/Docker behavior, and module management/artifact namespace evidence in both deployments remain later-stage work.

### Committed slices with tasks left open

- **Six-slot shell with v2 workspace routes (`58b76c9`, `UI-01`/`UI-02`/`UI-06` remain open):** independent primary/secondary selection, six-slot shell with collapse/focus/restore, stale-navigation guards, v2 hash routes with v1 compatibility and bounded IDs, mobile secondary chooser, keyboard/pointer resizers, and focus visibility handling. Verified by 106 focused Web UI tests passing and web typecheck with zero errors; full suite, production build, browser QA, and Schedules/Settings list/detail completeness remain open.
- **Grouped session catalog and Archive views (`6aa48c4`, `UI-03` remains open):** grouped/flat catalog with recent-first ordering, selected/running pinning, native titles, metadata-only Archive restore via `session.unarchive`, and project removal that keeps chats. Verified by 86 workspace Web UI tests passing (18 new) and web typecheck with zero errors; ungrouped host-keyed metadata, full suite, and browser QA remain open.
- **Typed publication outcomes (`8aed6cb`, superseded by `FIX-13` below):** `OutcomeKnownUncommitted`/`OutcomeInstalled`/`OutcomeDurabilityUncertain` commit path with validate-first staging, fault-injection hooks, reconcile-without-backup-fallback, and ledger publish guards. Verified by focused persist tests passing (4 colocated plus 14 integration).
- **Staged migration inventory (`f1c8268`, `MIG-01` remains open):** additive inspect/plan/validate helpers for schedule, queue, and deletion ledgers. Verified by focused persist and mcpserver Go tests passing; real conversion, topology switching, and rollback remain open.

### FIX-13 — Ledger save paths on typed publication outcomes

- **Implementation:** `338d8f2` routes schedule, queue, and deletion ledger saves through `WriteWithOutcome` plus `Decide*/Reconcile*` guards: only installed entries update dispatch state, uncertain outcomes retain mutation identity and reconcile the validated primary without backup restore or replay, and tombstones stay fail-closed. `SetPublishFaults` hooks enable post-rename staging/backup/primary/dir-sync/reply fault injection.
- **Behavior evidence:** new `scheduler_publish_outcomes_test.go` and extended `persist_publish_outcomes_test.go` prove post-rename uncertainty retains the disk and memory candidate, identical `mutationId` reconciles, different input conflicts, delete stays applied, `runNow` does not dispatch, and pre-rename failure preserves the prior primary.
- **Verification:** focused persist/controller outcome run (48 matching tests pass) plus full `CGO_ENABLED=0 go test -count=1 ./internal/controller ./tests/go/controller` (both ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** full race suite, live migration/topology evidence, and both-architecture/systemd/Docker behavior remain later-stage work.

### Committed slices with tasks left open (2026-09-09, second batch)

- **Composed transport and prompt caps (`0e8021c`, `LIMIT-01` remains open):** documented frame/record/in-flight/aggregate/control bounds with ordinary-vs-control admission and per-image/aggregate text-resource validation. Verified by focused piprotocol/controller limit tests passing; the X09 Design index artifact path, a single cross-subsystem counter, and full race evidence remain open.
- **Verified Stop outcomes and idle release (`92fff3c`, `LIFE-01` remains open):** dispatch-freeze-first Stop with bounded abort, generation-quiescence verification, distinct stopping/stopped/uncertain reporting, retained-paused outbox, `Abort` caller-deadline contract preserved, and eligible-only idle release retaining history. Verified by the full controller package passing including 6 new Stop regressions; forced managed-child teardown, post-stop generation bump, and live Pi evidence remain open.
- **Schedules list/detail and settings sections (`b02e74b`, `UI-04` remains open):** schedule list/detail/run views reusing ledger semantics with missing-state recovery, plus settings-section wiring. Verified by 35 focused schedule/settings tests passing and web typecheck with zero errors; shared poller ownership, rail-dialog unification, and browser QA remain open (2 `extensions.test.ts` failures are pre-existing and unrelated).

### API-01 — Exhaustive browser catalog with handler binding check

- **Implementation:** `8486353` adds the 10 missing `WS_METHODS` entries (8 `schedule.*`, `model.thinkingLevels`, `mcpAdapter.status`) so 96 constants match 96 `WsMethodMap` keys, and adds `ws-catalog.test.ts` with per-row FC/owner/profile/native-route mapping plus a generated check reading the Go `CoreHandler`/`PiAdmin` sources to prove every contract method has a handler case and vice versa.
- **Behavior evidence:** the catalog test enumerates all 96 rows with exact native routes at both compile time (`Exclude` proofs) and runtime, closing the known constant-list divergence.
- **Verification:** `bun test tests/contracts` (12 pass, 1136 expectations); contracts typecheck and tests typecheck clean; `git diff --check` clean.
- **Remaining boundary:** per-method live native/bridge evidence per FC profile, both-architecture runs, and full-suite/browser QA remain later-stage work.

### STATE-01 — Nullable project grouping with explicit admission

- **Implementation:** `a4b6443` makes empty project identity mean ungrouped (filesystem admission only, no hidden all-files project), separates stored native cwd from admission re-checks so stale roots fail closed, makes project removal grouping-only (conversations and session-keyed drafts survive), and migrates legacy drafts to session keys with conflict detection.
- **Behavior evidence:** new `projects_cwd_test.go`/`projects_grouping_test.go` cover ungrouped admission without a project, admission/grouping separation, removal preserving conversations and drafts, and nullable grouping validation.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/workspace ./tests/go/workspace ./internal/persist ./tests/go/persist` (all ok); integrated run with mcpserver/controller also ok; `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** full metadata conversion, topology switching, and schema-aware rollback stay `MIG-01` work; full race suite and live migration evidence remain later-stage.

### Committed slices with tasks left open (2026-09-09, third batch)

- **Trusted module descriptors (`a671119`, superseded by `MODULE-01` below).**
- **Details model and multi-repo Git views (`bdd106f`, superseded by `UI-05` below).**

### MODULE-01 — Trusted descriptors with HTTP scope enforcement

- **Implementation:** `a671119` adds descriptor validation (sidebar-only/viewer-only declarations, remote-code allowlist rejection, namespaced resource IDs) with session-bound unguessable scope tokens; `3b31d52` wires the boundary into the assembled handler so module resource requests authorize the exact module/resource/session/generation tuple and forged, mismatched, expired, revoked, or URL-carried tokens fail closed without module delegation.
- **Behavior evidence:** 9 descriptor/scope regressions plus 6 HTTP boundary regressions covering valid delegation, forged/mismatched/expired/revoked tokens, token-in-URL, contribution scope, unknown modules, and wrong methods, all asserting no delegation on failure.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/mcpserver ./tests/go/mcpserver ./internal/controller ./tests/go/controller -run 'Scope|Descriptor|Module|Contribution|Forged|Revok'` (pass); integrated eight-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** full race suite, both-architecture runs, live deployment evidence, and browser QA remain later-stage work.

### MIG-01 — Staged conversion with topology switching and rollback

- **Implementation:** `f1c8268` provides the additive metadata inventory; `5199294` adds the staged apply engine (inventory, hashed backups, staging, primary, sync, receipt checkpoints with dry-run redaction and per-phase fault injection) plus both topology switches with preconditions and schema-aware rollback that never restores older snapshots as runnable authority and never rewinds ledgers.
- **Behavior evidence:** new apply/topology/rollback fixtures cover checkpoint order, redacted dry-runs, backup hashes, interruption before/after each phase, both switch directions, ungrouped preservation, tombstone retention, and no missed-job dispatch.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/persist ./tests/go/persist` (all ok); integrated eight-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** live migration on real installations, full race suite, and both-architecture/systemd/Docker evidence remain later-stage work.

### UI-05 — Details sidebar with release control and multi-repo Git

- **Implementation:** `bdd106f` adds session-details helpers, multi-repository selector identity, raw-conversion notice wiring, and read-only preview hooks; `57641c1` wires the details panel into the secondary sidebar with live `session.list`/`session.getStats` data (unknown values stay unknown, never zero) and a backend-authoritative `session.release` affordance shown only when eligible.
- **Behavior evidence:** 39 files tests plus 5 wiring regressions cover authoritative release params, verbatim refusal surfacing, unknown-never-zero display, multi-repo identity, and work-area delegation keeping release authority in the wrapper.
- **Verification:** `bun test tests/webui/files` (44 pass); web typecheck with zero errors; `git diff --check` clean.
- **Remaining boundary:** full Web UI suite, production build, and browser QA remain later-stage work.

### API-02 — Host v2 schemas with separated ID domains

- **Implementation:** `ebc890c` adds v2 envelope validation (protocol version, close codes, hello without credentials, strict ID/method/params, v1 duplicate shim), distinct browser/host/native ID domains with no-coercion parse shims and reconnect-safe mapping, plus epochs, sequenced snapshots, settlement transitions, and durability gates.
- **Behavior evidence:** 15 new fixtures cover the close-code matrix, oversize handling, duplicate semantics per version, `"001"`-vs-`1` non-conversion, native-only large IDs, epoch/snapshot staleness, and follow-up blocking.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/piprotocol ./tests/go/piprotocol` (pass, 18 tests); integrated six-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** v2 dispatcher bindings, legacy adapter wiring, live Pi profiles, both-architecture runs, and full race evidence remain later-stage work.

### UI-01 — Six-slot reducer with canonical selection ownership

- **Implementation:** `58b76c9` builds the six-slot shell on `workspaceSelection`/`WorkspaceLayout`; `449d1ae` removes mixed-content tab ownership from the canonical path (legacy cache resolves only unmatched tabs), adds versioned persist with v1 forward migration, and constrains selections to live projects on new-server recovery.
- **Behavior evidence:** shell-layout, selection, and ui-closure-selection regressions cover defaults, split ranges, independence, canonical-over-legacy derivation, invalid tab IDs, and persist round-trips.
- **Verification:** `bun test tests/webui/workspace tests/webui/store` (139 pass); full package typecheck passes; `git diff --check` clean.
- **Remaining boundary:** full Web UI suite, production build, and browser QA remain later-stage work.

### UI-02 — Chat and File split with continuity

- **Implementation:** `58b76c9` with `449d1ae` delivers the real Chat plus File split with independent collapse/focus/restore; `selectSplitPair` resolves canonical pairs, split panes guard in-progress resizes, and runtimes with drafts/streams/queues survive tab clearing.
- **Behavior evidence:** ui-closure-split regressions cover split pairing, draft/stream continuity across switches, scroll/focus retention hooks, and project-scoped secondary tabs.
- **Verification:** `bun test tests/webui/workspace tests/webui/store` (139 pass); full package typecheck passes; `git diff --check` clean.
- **Remaining boundary:** full Web UI suite, production build, and browser QA remain later-stage work.

### UI-04 — Schedules and Settings list/detail navigation

- **Implementation:** `b02e74b` adds schedule list/detail/run views reusing ledger semantics with missing-state recovery plus settings-section wiring.
- **Behavior evidence:** 35 focused schedule/settings regressions cover filtering, selection without redirect, status labels, run-ledger reuse, and section resolution.
- **Verification:** 35 focused tests pass; web typecheck with zero errors; `git diff --check` clean.
- **Remaining boundary:** shared poller ownership, rail-dialog unification, full suite, production build, and browser QA remain later-stage work.

### UI-06 — v2 routes with invalid/missing/stale recovery

- **Implementation:** `58b76c9` introduces `#/v2/...` routes with v1 compatibility and bounded IDs; `449d1ae` makes missing canonical selections resolve to recovery views instead of unrelated tabs and adds staleness detection.
- **Behavior evidence:** location/restore plus ui-closure-restore regressions cover round-trips, invalid/missing/stale states, back/forward derivation, and persist migration.
- **Verification:** `bun test tests/webui/workspace tests/webui/store` (139 pass); full package typecheck passes; `git diff --check` clean.
- **Remaining boundary:** full Web UI suite, production build, and browser QA remain later-stage work.

### MEWA-01 — Pinned tokens, components, and adapters

- **Implementation:** `8fa2a5f` pins the Mewa 0.1.2 foundation (lock/manifest/base/tokens paths, revision, 78 icons) and records the semantic-role inventory without emitting a second visual system.
- **Behavior evidence:** foundation-pin, adapters, and cascade regressions verify the pin, wrapper preservation, icon-set equality, scoped controller lifecycle, and no generated-system leakage.
- **Verification:** `bun test tests/webui/mewa` (10 pass, 273 expectations); web typecheck with zero errors; `git diff --check` clean.
- **Remaining boundary:** full suite, production build, and browser QA remain later-stage work.

### MEWA-02 — One foundation cascade owner

- **Implementation:** `8fa2a5f` declares `mewa.css` the sole cascade owner with pin-first import order and keeps Pixie product tokens in `index.css`.
- **Behavior evidence:** cascade regressions assert import order, local-only sources, and no Pixie/Tailwind leakage into the foundation.
- **Verification:** `bun test tests/webui/mewa` (10 pass); web typecheck with zero errors; `git diff --check` clean.
- **Remaining boundary:** retiring legacy mappings/generators after consumer migration, plus full suite and browser QA, remain later-stage work.

### EXT-02 — Registry lifecycle with desired/readiness split

- **Implementation:** `b5a048d` separates desired enablement from module readiness, persists complete maps preserving unknown entries, snapshots startup config under read lock, and fails restrictive configs locally without permissive fallback.
- **Behavior evidence:** lifecycle regressions cover desired surviving startup failure, complete-map preservation with start decisions, and strict-config local failure.
- **Verification:** focused mcpserver/controller lifecycle run (pass); integrated six-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** full race suite, live deployment evidence, and both-architecture runs remain later-stage work.

### EXT-03 — Browser leases, artifacts, and cleanup

- **Implementation:** `b5a048d` sorts live lease-renewal payloads deterministically, rejects forged artifact session contexts, accepts service-matching image extensions, and marks lease expiry in the panel.
- **Behavior evidence:** lease/artifact regressions cover sorted renewal excluding closed panels, forged-session rejection, and case-insensitive image acceptance.
- **Verification:** focused controller Browser run (pass, existing suites included); integrated six-package Go run (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** live Browser deployment profile, full race suite, and both-architecture runs remain later-stage work.

### API-03 — Durable paired authority independent of dialing

- **Implementation:** `31c0a3c` adds a durable pairing store (binding ID, host identity, storage key, verifier hash only — never endpoints, ports, boot IDs, or credentials) with explicit pair/verify/rotate/revoke ceremony guards; recovery requires stored pairing plus host/storage match, and deletion binding v2 digests the durable tuple instead of endpoint/secret state.
- **Behavior evidence:** 12 new regressions cover restart durability independent of dialing, unauthenticated-claim rejection, legacy recovery blocked with actionable remedy, rotation preserving binding, revocation until explicit re-pair, and deterministic session-scoped v2 digests.
- **Verification:** focused pairing run (12 pass) plus full `CGO_ENABLED=0 go test -count=1 ./internal/persist ./tests/go/persist ./internal/controller ./tests/go/controller` (all ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** wiring v2 binding into live deletion recovery, full race suite, live services, and both-architecture runs remain later-stage work.

### UI-03 — Grouped/flat/ungrouped catalog with Archive

- **Implementation:** `6aa48c4` delivers grouped/flat views with recent-first order, selected/running pinning, native titles, and metadata-only Archive restore; `2c1563e` adds host/session-keyed metadata with nullable project keys so ungrouped sessions partition without a hidden all-files project.
- **Behavior evidence:** 26 catalog regressions cover grouping, Archive restore without cloning, removal keeping chats, nullable keys, host-key dedupe, ungrouped partition, and ordering/pinning/titles.
- **Verification:** `bun test tests/webui/workspace` (86 pass at the time, plus 8 ungrouped); web typecheck with zero errors; `git diff --check` clean.
- **Remaining boundary:** ungrouped rows still open through existing project navigation until host metadata flows over `session.list`; full suite, production build, and browser QA remain later-stage work.

### SEC-01 — Verified HTTP/auth/CSRF/filesystem/Browser posture

- **Implementation:** `bd18a29` proves posture through the real handler in both no-auth LAN and authenticated modes: exact-origin mutations, cross-origin and unapproved-Host rejection, CSRF denial without Origin on auth/WS surfaces, auth-gated files/artifacts, traversal containment, and Browser-unavailable fail-closed.
- **Behavior evidence:** 11 new posture/CSRF regressions plus existing artifact/origin/authority suites passing against the assembled handler.
- **Verification:** focused posture run (pass) plus full controller package run (ok); `gofmt` clean; `git diff --check` clean.
- **Remaining boundary:** live deployment evidence in both modes, both-architecture runs, full race suite, and launcher/delegation tests (`SEC-02`) remain later-stage work.

### Committed slices with tasks left open (2026-09-09, fourth batch)

- **Operating docs accuracy (`a69f9d2`, `DOC-01`/`DOC-02` remain open):** architecture, deployment, development, security, and README synced to shipped behavior with verified local links and examples; remaining doc files and checker tooling stay open.

### Committed slices with tasks left open (2026-09-09, fifth batch)

- **Assistant facade and doctor (`fc3b4fb`, superseded by `GO-01` below).**

### GO-01 — Separate module with discovery and staged compatibility

- **Implementation:** `fc3b4fb` provides the single public facade (config precedence, argv/Pi validation) and read-only doctor; `70172c5` adds static import-surface enforcement (no controller-internal or obsolete roots), Pi discovery across npm/standalone installations with independent version reporting, and staged legacy compatibility with schema-aware rollback hooks.
- **Behavior evidence:** 27 new assistant regressions cover boundary scans of the live checkout, exact local replacement, discovery classification and version parsing, staged decisions, rollback no-rewind guarantees, and doctor read-only behavior.
- **Verification:** focused assistant runs (27 pass) plus assistant typecheck clean; `git diff --check` clean.
- **Remaining boundary:** facade/discovery wiring, Go module split, dual-build gates, live Pi installations, and both-architecture install evidence remain later-stage work.

### Committed slices with tasks left open (2026-09-09, sixth batch)

- **Bounded native JSONL transport (`a728344`, `GO-02` remains open):** 32 MiB framed records, 64 MiB aggregate admission with control reserve, correlation, exit draining, and explicit stall timeouts in Go plus a TS mirror. Verified by 27 transport tests passing; live child supervision, handshake integration, and replay/outbox settlement remain open.
- **Native UI rows over public adapter (`a47ba78`, `EXT-01`/`BRIDGE-02`/`BRIDGE-03` remain open):** 10-row mapping with exact limitations and single final-response settlement, 12-operation public-bus routing with `pi.tools.call` explicitly blocked, and honest FC15 working/cancellation blockers. Verified by 13 tests passing; live-runtime wiring and upstream hook fidelity remain open.
- **Layout probes and upgrade recovery (`2092396`, `MEWA-03`/`UI-07` remain open):** five content-filled light/dark layout fixtures and pure upgrade-recovery helpers preserving drafts and mutation identity. Verified by 21 tests passing with web typecheck clean; cascade wiring, view/shell connection, screenshots, and old-browser evidence remain open.
