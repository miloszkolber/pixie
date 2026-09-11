# Pixie roadmap changelog

This file records roadmap work that has shipped in the checkout. [execution.md](execution.md) remains the status ledger. A completed entry names the implementation commit, observed behavior, verification commands and remaining boundaries. Uncommitted working-tree changes are not completion evidence and are not recorded as shipped work here.

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
- **Native session flow contracts (`dcd9fbd`, superseded by `GO-03` below).**
- **Bounded history and draft continuity (`49ebd15`, superseded by `GO-04` below).**
- **Identity-safe outbox contracts (`509e578`, superseded by `GO-05` below).**
- **Generation-safe native UI state (`1e901e5`, superseded by `GO-06` below).**
- **Bounded residency and idle handoff (`f01f5db`, superseded by `GO-07` below).**
### BRIDGE-01 — Public bridge feasibility

- **Implementation:** `45ca73e` adds npm/standalone capability discovery, explicit asset opt-in, and bounded private-channel checks, with unsupported APIs reported as blockers rather than mocked.
- **Verification:** 9 bridge tests and assistant typecheck pass.
- **Remaining boundary:** live installation probes, asset materialization, and descriptor inheritance remain later-stage work.
- **Fail-closed launcher posture checks (`a7a5146`, `SEC-02` remains open):** pure validation for pre-exec placement, namespaces, scratch/inode, egress, descriptors, descendants, supported architectures, and direct-host/shared-UID rejection. Verified by focused Go security tests and `gofmt`; launcher wiring and live architecture evidence remain open.
- **Composition boundary checks (`6e97cc6`, superseded by `BUILD-01` below).**

### GO-03 — Native session flow contracts and runtime wiring

- **Implementation:** `dcd9fbd` adds create/prompt/images/model/thinking/abort/reopen validation; `3fb04fb` wires session flow, bounded outbox, drafts/history, passive UI state, residency/release, mutation dedupe, generations, and redacted runtime snapshots into the existing Pi-owned runtime without a second agent loop.
- **Verification:** 42 session/flow tests plus 42 runtime/outbox/session tests pass; assistant typecheck clean; `git diff --check` clean.
- **Remaining boundary:** live Pi/native resource and trust evidence, full build, and dual-architecture runs remain later-stage work.

### GO-04 — Bounded history and draft continuity

- **Implementation:** `49ebd15` adds identity-preserving clone/fork, bounded read-only catalog/history, unknown-record preservation, versioned drafts, and external-replacement conflict handling; runtime wiring is included in `3fb04fb`.
- **Verification:** focused history/draft/runtime tests pass and assistant typecheck is clean.
- **Remaining boundary:** live native catalog/clone and restart/upgrade evidence remain later-stage work.

### GO-05 — Identity-safe outbox and continuation

- **Implementation:** `509e578` adds the prepared/dispatching/accepted/settled/uncertain state machine with retry/compaction/continuation gates, duplicate-uncertain blocking, Stop retention, and explicit disposition; `3fb04fb` persists bounded runtime wiring and mutation identities.
- **Verification:** outbox/session/runtime tests pass and assistant typecheck is clean.
- **Remaining boundary:** child transport handshake, native persistence fault injection, and live Pi settlement remain later-stage work.

### GO-06 — Generation-safe native UI state

- **Implementation:** `1e901e5` adds generation-scoped passive state, draft conflicts, and honest unsupported controls; `3fb04fb` projects it through the runtime snapshot.
- **Verification:** UI-state and runtime tests pass; assistant typecheck clean.
- **Remaining boundary:** upstream native UI hooks and live replay evidence remain later-stage work.

### GO-07 — Bounded residency and idle handoff

- **Implementation:** `f01f5db` adds bounded admission, detached-work disposition, generation guards, explicit release/TUI handoff states, and no forced release of active or unknown work.
- **Verification:** focused residency and runtime tests pass; assistant typecheck clean.
- **Remaining boundary:** host process residency, descendant termination, and live idle-TUI handoff remain later-stage work.

### BUILD-01 — Separate assistant and controller compositions

- **Implementation:** `aaa4809` adds `assistant/go.mod`, public `assistant/host`, assistant command, exact local replacement, full-host public-facade wiring, controller-only Docker mode, and assistant runtime packaging; `6e97cc6`/`03a7e15` provide static composition and assistant-build boundary checks.
- **Verification:** 16 build tests pass, assistant and controller Go commands compile with `CGO_ENABLED=0`, assistant typecheck passes, and `git diff --check` is clean.
- **Remaining boundary:** native Go engine implementation, complete runtime closure, production artifacts, and both-architecture builds remain later-stage work.

### BUILD-02 — Assistant-only build boundary

- **Implementation:** `aaa4809` configures assistant runtime files/dist packaging independently; `03a7e15` checks runtime-only files and forbidden controller/UI/worker closure.
- **Verification:** assistant build boundary tests pass and assistant typecheck passes.
- **Remaining boundary:** actual production bundle and independent runtime execution remain later-stage work.

### BUILD/REL static release evidence

- **Implementation:** `2942bf7` adds commit-named release identity checks and `docs/builds/release-checklist.md`; the checker intentionally reports absent workflows/artifacts/digests rather than claiming release readiness.
- **Verification:** 8 release identity tests pass and documented links resolve.
- **Remaining boundary:** REL-01 through REL-04, complete artifacts, provenance, immutable retries, and publication policy remain open.

### GO-02 — Bounded JSONL transport integration

- **Implementation:** `a728344` defines bounded Go/TypeScript framing, admission, correlation, exit, and stall behavior; `64f80e8` wires real child-process transport into the assistant runtime with hello handshake, stderr draining, control reserve, and explicit interrupted/uncertain outcomes.
- **Verification:** 20 transport/runtime integration tests pass, piprotocol Go tests pass, assistant typecheck passes, and `git diff --check` is clean.
- **Remaining boundary:** full child-supervision/race evidence, live Pi handshake, and dual-architecture runtime remain later-stage work.

### EXT-01 / BRIDGE-02 / BRIDGE-03 — Native bridge integration

- **Implementation:** `a47ba78` defines exact native UI rows, public adapter routing, and honest FC15 blockers; `68fb539` wires table-driven dispatch and public adapter events into the existing extension runtime, with private `pi.tools.call` remaining an explicit blocker.
- **Verification:** 21 bridge/native integration tests pass and assistant typecheck is clean.
- **Remaining boundary:** live Pi API proof, exact upstream cancellation hooks, and unsupported `pi.tools.call` remain explicit blockers.

### BUILD-03 / BUILD-04 / PKG-01 — Composition and package boundary checks

- **Implementation:** `aaa4809` adds separate assistant/full-host entrypoints and controller-only Docker mode; `9f17cea` adds artifact/package checks plus systemd units and redacted config fixtures.
- **Verification:** 23 build tests pass; controller commands compile with `CGO_ENABLED=0`; static Docker/package/systemd checks pass.
- **Remaining boundary:** production artifacts, Docker builds, real service lifecycle, and complete doctor/uninstall behavior remain open.

### GO-08 / GO-09 — Administration profiles and compatibility contracts

- **Implementation:** `d68590d` adds declared FC17–FC28 admin-profile descriptors with exact supported/blocked behavior and schema-aware compatibility/rollback contracts preserving tombstones and monotonic claims.
- **Verification:** 9 assistant profile/compatibility tests pass, assistant typecheck and formatting pass.
- **Remaining boundary:** runtime profile wiring, public native API proof, filesystem/topology migration, and released-artifact validation remain open.

### EXT-04 / SEC-02 / COVERAGE-01 — Scoped registration and fail-closed gates

- **Implementation:** `ccb9767` adds scoped native MCP registration with credential/generation/session/module revocation and forged-ID rejection, plus fail-closed FC/X coverage, cutover, resource/performance, and launcher posture checkers.
- **Verification:** focused mcpserver/security Go tests and coverage Bun tests pass; `gofmt` and `git diff --check` clean.
- **Remaining boundary:** live native registration, architecture/runtime resource evidence, and complete FC/artifact/launcher coverage remain open.

### BUILD-03 / BUILD-04 / BUILD-05 — Runtime/service gate fixtures

- **Implementation:** `b593135` hardens mode parsing, controller-only local-Pi rejection, bounded application drain, and systemd private environment/restart policy; `9f17cea` adds Docker/package/systemd fixture gates.
- **Verification:** 34 build tests plus runtime-gate executable fixtures pass; controller Go commands compile with `CGO_ENABLED=0`.
- **Remaining boundary:** full-host native engine, Docker builds, live systemd start/stop/restart, descendant drain, and both-architecture artifacts remain open.

### REL-01–REL-04 / PKG-02–PKG-03 — Release and artifact gate checks

- **Implementation:** `ccb9767` extends release identity and adds valid/invalid release gate fixtures for SHA naming, archives/platforms, labels/digests, provenance, collision/partial retries, and validate-only publication policy.
- **Verification:** release/package gate tests pass and checklist links resolve.
- **Remaining boundary:** workflows, real archives/images, provenance, immutable publication retries, and platform digests remain absent and are intentionally reported as failures by the checker.

### UI-07 / MEWA-03 — Integrated recovery and layout gates

- **Implementation:** `db397ef` and `0350c40` wire five light/dark content probes through the six-slot shell, add inert/focus/overflow behavior, and mount bounded lazy-asset recovery preserving drafts and mutation IDs.
- **Verification:** 32 layout/recovery tests pass, web typecheck has zero errors/warnings, and `git diff --check` is clean.
- **Remaining boundary:** production screenshots, full browser QA, old-browser upgrade evidence, and full suite remain open.

## 2026-09-09 — core closeout except Canvas/Openfig

### LIMIT-01 — Composed caps with shared aggregate admission and Design artifact path

- **Implementation:** `d4f4134` adds `package/internal/controller/aggregate_admission.go` (shared ordinary/control byte budgets), wires the same instance through browser admission (`websocket.go`), replay retention (`replay.go`), and socket output (`socket_output.go`), enforces per-image 4.5 MiB / 24 MiB aggregate / per-file and total text-resource caps at `promptBlocks`/`queuedText` against `piprotocol` limits, and adds `package/internal/design/index_artifact.go` (64 MiB dedicated immutable `documents/<id>/index.json` path via staging plus hard-link publication, JSON-object/no-trailing validation, generated paths only).
- **Behavior evidence:** ordinary traffic cannot consume the 8×64 KiB / 1 MiB control reserve; `session.abort`/`session.uiCancel` stay admitted under ordinary-load saturation; oversized images, aggregate payloads, text, and resources reject before native dispatch; 16–64 MiB Design indexes use the dedicated artifact path while generic JSON state stays capped.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/controller ./internal/design ./tests/go/controller ./tests/go/piprotocol` (all ok from `package/`); targeted LIMIT (6 pass), `piprotocol` prompt caps (4 pass), Design `WriteIndex` (2 pass); `git diff --check` clean.
- **Remaining boundary:** full race suite, both-architecture runs, live X07–X09 cross-boundary evidence in both deployments, and measured-capacity tuning remain later-stage work.

### LIFE-01 — Verified Stop outcomes and lease-owner idle release

- **Implementation:** `d4f4134` makes `Stop` freeze controller dispatch first, emit `stopping`, request bounded native abort, verify generation quiescence, bump `promptGeneration` only on verified `stopped`, and route every unverified path through `forceTerminateGeneration` with `stop_uncertain`, `ForcedTermination`, retained paused outbox, and generation-matched events; `IdleReleaseEligible`/`ReleaseIdleRuntime` verify settled/no-pending/no-dialog/no-liveness state, bump generation, drop residence while retaining history/draft/queue/metadata, and `session.release` requires the caller lease via `ReleaseIdleRuntimeForClient`; Archive/Delete never send `session.cancel`.
- **Behavior evidence:** new/expanded `life_stop_test.go`, `session_admission_test.go`, and `session_test.go` cover dispatch freeze with paused-outbox retention, quiescence-timeout uncertainty with forced termination, eligible-only idle release with history reload, cross-client release rejection, and Archive/Delete never implying Stop.
- **Verification:** targeted LIFE regressions (8 pass) plus full `CGO_ENABLED=0 go test -count=1 ./internal/controller ./tests/go/controller` (both ok); `git diff --check` clean.
- **Remaining boundary:** managed Pi-child teardown beyond transport fencing, live Pi settlement/pins, full race suite, both-architecture runs, and live X10–X11 capacity evidence remain later-stage work.

### GO-08/GO-09/EXT-04 — Admin profiles, compatibility, and scoped native MCP

- **Implementation:** `38e6e1e` makes the assistant `operationSet` exhaustive from `ADMIN_OPERATIONS` (empty negotiated map fails closed, absent map stays v1-compatible), consolidates legacy compatibility through one staged decision with schema-aware rollback that preserves unknown fields and never rewinds ledgers/tombstones, and attaches an instance-owned `NativeMCPRegistry` to the publisher with `Register`/`Authorize`/`Revoke`/`RevokeSession`/`RevokeModule`/`AdvanceGeneration`/`revokeAll` wired into `SetEnabled`/`Restart`/`startLocked`/`Shutdown` and controller Archive/Delete/idle-release/generation-change paths.
- **Behavior evidence:** forged IDs/credentials, swapped module/server/session, stale generations, expiry, and cross-registry replay fail closed; disable/restart/generation advance revoke prior credentials before replacement lifecycle continues; `pi.tools.call` stays explicitly blocked.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./internal/mcpserver ./tests/go/mcpserver ./internal/controller ./tests/go/controller` (all ok); focused native-MCP/registry/session tests pass; `bun test tests/pixie-assistant/admin-profiles.test.ts compatibility.test.ts discovery-compat.test.ts mcp.test.ts adapter-mcp.test.ts` (21 pass); facade/server/protocol tests (17 pass); `git diff --check` clean.
- **Remaining boundary:** live native registration against independently installed Pi distributions, full bridge API proof per FC profile, both-architecture runs, and full race evidence remain later-stage work.

### BUILD-03/BUILD-04/BUILD-05/REL-01/REL-02/REL-03/REL-04/PKG-01/PKG-02/PKG-03 — Composition, native host, and release pipeline

- **Implementation:** `ca67227` authenticates the private assistant WebSocket and readiness with a minimum-length constant-time bearer secret, supervises the selected Pi over bounded JSONL with process-group TERM/KILL and stderr draining, separates full-host (`pixie serve`) from controller-only (`pixie serve --mode controller`, Docker `ENTRYPOINT` with `-tags=controller` and no assistant sources) with mixed-config rejection, stages deterministic `sha-<12>` archives (`build-release.ts`: `-trimpath`, commit-time timestamps, sorted tar, `GZIP=-n`, four `pixie[-assistant]-sha-<12>-linux-<arch>.tar.gz` plus checksums/manifest), splits validate-only image builds from authorized `publish-image`/`publish` jobs with `VERSION`/`REVISION` args and truthful SBOM/provenance gating, retries release assets idempotently by SHA-256 compare without `--clobber`, and gates CI on coverage/package/release checks.
- **Behavior evidence:** new `assistant/host/host_test.go` and `native_child` fake-Pi fixtures cover private endpoint/hello, non-loopback rejection, secret-required readiness/WS, and MethodRPC/official-PiRPC hello paths; `runtime_test.go`, release-build/workflow/identity tests, and Docker/package/composition tests cover mode rejection, archive contents/version output, workflow YAML, and candidate staging checks.
- **Verification:** `CGO_ENABLED=0 go test ./...` (assistant and package, all ok); `bun test tests/build` (49 pass, 269 expects); `bun run check:docs` and `check:filenames` pass; `check-performance`/`check-coverage`/`check-package-artifacts`/`release-identity`/`release-gate` correctly fail closed without live artifacts; `build-release.ts --dry-run` reports validate-only with publication disabled; `git diff --check` clean.
- **Remaining boundary:** both-architecture final binary runs, runnable per-platform image runs with published index/platform digests, real `sha-<12>` tag/Release publication (requires explicit `PIXIE_RELEASE_ENABLED` authorization on a clean tree), live systemd start/stop/restart/mode-switch/upgrade/rollback/uninstall, and Gates 1–5 live evidence remain later-stage work. No publication was performed here.

### SEC-02/PERF-01/DOC-01/DOC-02/COVERAGE-01/CUTOVER-01 — Fail-closed gates and factual docs

- **Implementation:** `e19d8ee` adds launcher/delegation admission tests with no permissive fallback, `check-performance.ts` (5 fresh-process samples, ordered p50/p95 matching samples, full process tree, content-filled UI, decoded/buffer/worker/scratch fields, `live:true` required), `check-docs.ts` (static links/paths/commands/security qualifiers only, no prose-to-runtime promotion), and strengthened `check-coverage.ts`/cutover gates (named profiles, actual/live flags, explicit approved reductions; legacy removal refused by default).
- **Behavior evidence:** `launcher_delegation_test.go` covers both architectures and profiles against delegation/placement mutations; performance/coverage/package/release checkers report absent live evidence as failures rather than passing vacuously; operating docs pass static checks without claiming runtime behavior.
- **Verification:** `CGO_ENABLED=0 go test -count=1 ./tests/go/security -run TestLauncher` (pass); `bun test tests/build` (49 pass); `bun run check:docs` and `check:filenames` pass; `bun run typecheck` passes; `git diff --check` clean.
- **Remaining boundary:** full X12/X13 both-architecture descendant/scratch/inode/egress/descriptor evidence, repeated full-process amd64/arm64 measurements, disposable-env install/health/upgrade/rollback/mode-switch doc examples, FC/X live native/bridge/UI/artifact evidence, and Gates 1–5 live results remain later-stage work.

### Post-closeout verification repairs

- **Packaging regression test (`distribution.test.ts`):** the test still asserted the pre-BUILD-02 `src/main.ts` layout while the shipped package contains only `dist/` plus `patches/` (source is excluded from production artifacts by policy). The assistant `build` script now also emits `dist/server.js`, and the test asserts `dist/main.js` plus `startHost` from `dist/server.js`. Verification: `bun test tests/pixie-assistant/distribution.test.ts` (1 pass).
- **Evidence anchors (`execution.md`):** three references did not match any changelog heading (BRIDGE-01 carried a commit hash, GO-03/GO-05 used truncated titles). All 67 references (47 unique anchors) now resolve to changelog headings under the GitHub anchor algorithm.
- **Suite state after repair:** `bun test tests/pixie-assistant` 204 pass / 0 fail; `bun test tests/contracts` 12 pass; `bun test tests/webui` 367 pass / 23 fail (all 23 are the pre-existing `NameTooLong` data-URL harness failures in renderer tests, unrelated to roadmap changes); `bun test tests/pi-native-parity` 116 pass / 2 fail (both fail identically in a throwaway repro: RPC children spawned under Bun 1.3.14 crash during pinned-SDK bundle init with `TypeError: webidl.util.markAsUncloneable is not a function` from the bundled `undici`/`CacheStorage` path, before agent logic runs — a Bun runtime vs pinned-SDK incompatibility, not a product-code regression).

### Post-closeout maintenance

- **BUILD-02 follow-up (`94a4f55`):** the production assistant build now emits `dist/server.js`, and the distribution regression verifies the shipped `dist/` layout and `startHost` export instead of the excluded source tree. Verification: `bun test tests/pixie-assistant/distribution.test.ts` (1 pass).
- **REL-02/REL-04 follow-up (`1802cd9`):** validate-only container builds receive `VERSION=sha-<12>` and the full `REVISION` source SHA as build arguments, while remaining non-publishing. Verification: `bun test tests/build` (50 pass, 274 expectations).
- **DOC-01/DOC-02 follow-up (`c99ca6e`, `be0bae6`):** all execution-to-changelog anchors resolve, current security code references are re-pinned, and the pinned-SDK/Bun `undici` child-runtime parity failure is documented without treating it as a product fix. Verification: `bun run check:docs`; `bun run check:filenames`; `bun run typecheck`.

### Committed slices with tasks left open (Canvas/Design hardening)

- **Canvas service with X04 durability hardening (`678e0be`, `CAN-02` remains open):** session-scoped Canvas service with transactional revisions, mutation ledger with fingerprint idempotency and conflict handling, tombstones, quota reservation before staging, restart recovery, per-step publish fault hooks with reconcile-without-backup-restore, and controller attach/revoke wiring. Verified by `CGO_ENABLED=0 go test -count=1 ./tests/go/canvas` (17 top-level pass including 5 fault subtests) and `CGO_ENABLED=0 go test -count=1 ./internal/controller -run Canvas` (4 pass); `go vet` and `gofmt` clean. Faults are in-memory hooks with disposable state, not storage failures through the real controller on both deployments and architectures; contained egress-denied rendering stays absent and the deterministic launcher remains an explicit non-sandbox fixture.
- **Design service with X09/cursor/readiness hardening (`9f202d4`, `FIG-02`/`FIG-03`/`FIG-05` remain open):** instance-wide Design slot with bounded preflight, dedicated 64 MiB index artifact path, transactional upload and removal, shared read-only queries with bounded cursors and real cover bytes, disabled-mode removal without parser, plus large-index, cursor binding and trimming, and parser-missing readiness coverage. Verified by `CGO_ENABLED=0 go test -count=1 ./internal/design ./tests/go/design` (19 top-level pass). The index proof uses synthetic fixture nodes rather than licensed `.fig` files via pinned `openfig-core`; no enclosed worker exists and retained-source reindex stays explicitly unimplemented.
- **Definition-driven Canvas/Design registry and routing (`b6eed6a`, `CAN-04`/`CAN-05`/`FIG-05` remain open):** module ownership consumed from definitions with reserved-route protection, unknown-path JSON-not-SPA behavior, trusted scope descriptors, desired and readiness split, scoped revocation, Design parser-missing readiness, fixture generality, and core-takeover negative coverage. Verified by `CGO_ENABLED=0 go test -count=1 ./tests/go/mcpserver ./internal/controller ./tests/go/controller` (all ok) and `bun test tests/pixie-assistant/mcp.test.ts tests/pixie-assistant/bridge-integration.test.ts` (10 pass). Evidence is `httptest` fixtures, not actual assistant-only plus full-host artifacts on both architectures and deployments with systemd and Docker behavior.
- **Canvas/Design raster-first UI contributions (`4d65e68`, `CAN-04`/`FIG-04` remain open):** session-scoped Canvas and instance-wide Design slot 4 and 5 models, authenticated token-free artifact URLs, version and stale display, compact tool cards without HTML replay, Design draft reference, shared-focus inspector, upload progress and cancel and confirmation, and Mewa template coverage. Verified by `bun test tests/webui/canvas tests/webui/design` (27 pass), `bun run typecheck` (contracts, web `svelte-check` 0 errors and 0 warnings, tests, assistant), `bun run mewa:check` (0.1.2), and `bun run check:filenames` (OK). Host slot wiring, live event delivery, production browser QA, and both deployments remain open; components are presentational pending host integration.
- **Workspace selection and native attach wiring (`ae54ad3`, `CAN-04`/`FIG-04` remain open):** session-scoped Canvas versus instance-wide Design selection, six-slot content tabs, leases, native MCP attach and revoke across lifecycle paths, and assistant Canvas attachment ordering with workspace closure coverage. Verified by the integrated `CGO_ENABLED=0 go test -count=1 ./...` run from `package/` (all packages ok), `bun run check:docs` (OK), and `git diff --check` (clean). Full suite, production build, browser QA, live native adapter, and both deployments remain open; basic chat stays usable with Canvas and Design absent.
- **Remaining blockers:** `CAN-01` has no tested isolated renderer with egress-denied networking, private mounts, PID and user namespaces, cgroup limits, scratch and inode bounds, or canary and host-service probes; `CAN-03` has no real HTML execution (checker PNG only); `FIG-01` has no pinned `openfig-core`, clean-install lockfile reproduction, licensed fixtures, `package/design-worker`, or tested enclosure; `FIG-06` stays correctly open with frame requests failing unavailable and the cover never substituting for frame rendering; standalone Design service with unset authorizers depends entirely on controller composition for authority; preflight fixture names remain broader than a strict complete-`.fig` gate; index hard-link publication has no cross-filesystem fallback. Independent audit rejects marking any `CAN` or `FIG` task complete on this evidence; all `CAN-01` through `CAN-05` and `FIG-01` through `FIG-06` stay open with Gates 5, 6, and 7 unpassed.
