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

### Committed slices with tasks left open

- **Six-slot shell with v2 workspace routes (`58b76c9`, `UI-01`/`UI-02`/`UI-06` remain open):** independent primary/secondary selection, six-slot shell with collapse/focus/restore, stale-navigation guards, v2 hash routes with v1 compatibility and bounded IDs, mobile secondary chooser, keyboard/pointer resizers, and focus visibility handling. Verified by 106 focused Web UI tests passing and web typecheck with zero errors; full suite, production build, browser QA, and Archive/Schedules/Settings list/detail completeness remain open.
- **Staged migration inventory and MCP route tests (`f1c8268`, `MIG-01`/`ROUTE-01` remain open):** additive inspect/plan/validate helpers for schedule, queue, and deletion ledgers plus registry unknown-route ownership tests. Verified by focused persist and mcpserver Go tests passing; real conversion, topology switching, rollback, and assembled-handler fallback evidence remain open.
