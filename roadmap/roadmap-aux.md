# Auxiliary implementation plan

This is the implementable plan for the additive work identified by reviewing neighbouring open-source Pi interfaces: `pi-ui` (MIT, `github.com/hyperpuncher/pi-ui`), `thinkrail`, `leyline`, `pi-web-ui`, `pi-web`, `pi-forge`, `openpi`, and `pi-gui`. It is subordinate to [roadmap.md](roadmap.md): it does not change the three-product target, the trust model, or the disabled-by-default module policy, and every task here is additive or optional.

The plan is a gap plan. Reconnaissance found that several patterns are already implemented in Pixie, so each task states the current state, the exact delta, the files it touches, and the test that proves it. Citations prefixed with a repository name refer to that external checkout; unprefixed paths are ours.

## Gates

Every task carries one gate:

- **G1** — source-only, implementable and testable now with no live Pi, credentials, or network.
- **G2** — needs live runtime evidence: a credentialed Pi, a real PTY, arm64, Docker, or systemd.
- **G3** — needs a public upstream Pi API or a design decision that may end in "keep unavailable".
- **G4** — needs separate operator authorization (publication, deployment, dependency adoption).

Tasks are ordered by dependency and risk. A G2 or G3 task must not block a G1 task.

## Current-state gap analysis

| Task | Current state | Delta | Gate |
| --- | --- | --- | --- |
| AUX-01 transcript egress | Absent; `web/webui/index.html` sets no `img-src` policy and chat renders micromark output through `{@html}` | App-document CSP or image rewriting | G1 |
| AUX-02 local-request trust | Implemented: HMAC token, constant-time compare, `ControllerHost`, `PublicOrigin` in `web/internal/controller/auth.go` | Regression tests only | G1 |
| AUX-03 steering | Host rejects `session.steer` with `CapabilityError`; `session.followUp` works and queue lanes exist | Spike, then either contract or explicit unavailable | G3 |
| AUX-04 compaction queue | `web/internal/controller/session_queues.go` models `Steering`, `FollowUp`, `Blocked`, `Revision` | Flush/drain on compaction end | G1 |
| AUX-05 ownership | Session release and `reln` ownership exist; no generation guard | Generation guards and replacement epochs | G1 |
| AUX-06 provider usage | `provider_inventory.go`, `pi_events.go`, `web/webui/src/chat/session/session-stats.ts` | Rate-limit projection from resolved auth | G1 |
| AUX-07 extension-UI matrix | `docs/sdk-coverage.md` has a native-TUI-only row | Enumerate per-method supported/unsupported | G1 |
| AUX-08 unknown-event policy | — | Tests only; fold into AUX-12 | G1 |
| AUX-09 anti-slop lint | Biome recommended preset only (`biome.json:25-30`) | Portable rule subset | G1 |
| AUX-10 config schema gate | Absent; no schema generator under `web/scripts` | Generator plus `--check` gate | G1 |
| AUX-11 session index | Controller lists through the host | Only if measured; decision | G2 |
| AUX-12 schema version guard | Absent; parser tolerates shapes only | Header version guard and repair | G1 |
| AUX-13 CLI coexistence | Absent | Lease plus mtime tail | G1 |
| AUX-14 branching | `SessionManager.Fork` exists (`web/internal/controller/session_lifecycle.go:18`); one mode only | Two-tier semantics and ancestry | G1 |
| AUX-15 cost/context | Partial in `session-stats.ts` | Monotonic across compaction | G1 |
| AUX-16 snapshot plus delta | Idempotent replay exists (`web/internal/controller/replay.go`); login snapshot exists (`websocket.go:467`) | Revision-chained delta and resync | G1 |
| AUX-17 streaming render | Partial | Delta channel, prefix cache, virtualization | G1 |
| AUX-18 host supervision | Separate owner-locked host exists | Protocol validation, epochs, handshake, restart fixes | G1 |
| AUX-19 drain and quiesce | `runtime_drain_test.go` covers Git pipe drain only | Admission gate for update/rollback | G1 |
| AUX-20 auth hardening | Core auth exists; no Fetch Metadata, lockout, or rate limit | Optional-mode additions | G1 |
| AUX-21 file and Git containment | Read-only mounts and read-only Git per `docs/security.md` | Realpath walk-up, per-cwd mutex, per-turn diff | G1 |
| AUX-22 boundary and pin gates | `check-catalog.ts` exists | Module-boundary and banned-dependency check | G1 |
| AUX-23 PTY contract | Absent; PTY evidence unproven | Contract plus allowlist | G2 |
| AUX-24 orchestration | Absent | Extension tools plus contained workers | G2, G4 |
| AUX-25 host route parity | Catalog marks archive routes absent and `mcp.attach` unavailable, but legacy controller fixtures and browser route rows can still advertise them | Generated ownership/status matrix, real-host fixtures, and metadata-shape parity checks | G1 |
| AUX-26 archive lifecycle | Archive/unarchive are controller-owned but have no durable marker; the current controller fails closed instead of inventing a native route | Persisted archive state or an explicit unavailable contract, with restart and list reconciliation | G3 |
| AUX-27 deletion admission | Native MCP authority is revoked before host capability, attachment, and deletion-journal admission complete | Admit and persist the deletion intent before revocation, then reconcile uncertain dispatch | G1 |
| AUX-28 child stderr boundary | The full supervisor mirrors raw child stderr beside the bounded redacted ring | Redact the live mirror or remove the bypass and test secret/path-bearing child output | G1 |
| AUX-29 assistant failure redaction | Startup failures redact without the resolved bearer secret and can preserve spaced absolute paths | Thread configured secrets through every fatal path and harden path redaction | G1 |
| AUX-30 structured log safety | TypeScript redaction misses common assignment/API-key forms; record keys, capacities, and split stderr chunks are not fully defensive | Harden patterns, null-prototype records, finite capacities, and line-fragment buffering | G1 |
| AUX-31 Go snapshot redaction | Bare configured or opaque 32–64 character secrets can survive support-snapshot sanitization | Redact configured secrets and bounded opaque credentials without hiding required identifiers | G1 |
| AUX-32 native error boundary | Raw Pi/SDK error text can reach browser responses or session projections | Return stable safe errors and retain only redacted causes in diagnostics | G1 |
| AUX-33 v2 hello strictness | Negotiation accepts an empty v2 advertisement and can under-validate a selected peer version | Require and validate the v2 supported-version advertisement while retaining explicit v1 compatibility | G1 |
| AUX-34 shared runtime schemas | `@pixie/shared` validates only transport envelopes; method parameters/results remain handler-local and can drift across Go and TypeScript | Generate or centralize method-level runtime validators and cross-boundary drift checks | G1 |
| AUX-35 pinned verification environment | The workspace requires Bun `1.4.0`, but local checks can run under `1.3.14`; temporary-copy tests can also fail from `ENOSPC` | Refuse unsupported runtimes, isolate temporary workspaces, and classify incomplete evidence without false green results | G1 |

## M0 — Plan guardrails

Cheap checks that make the later invariants executable. No runtime behavior changes.

### AUX-22 — Module-boundary and banned-dependency gate

Outcome: a check that fails when `assistant/` imports `web/` internals, when `web/` imports `assistant/src` directly, or when a banned dependency such as Tailwind reappears. Delta: extend `web/scripts/check-catalog.ts` or add a `check-boundaries.ts` checker in `web/scripts`, and wire it into the root `lint` or `check:deps` script. Touch points: `web/scripts/`, `package.json`, `web/package.json`. Steps: enumerate allowed module edges, parse imports, fail on violation with the file and line. Verify: `bun run check:deps` passes on the tree and fails when a temporary violating import is added. Exit: gate runs in CI and in the documented pre-commit command. Also adopt `thinkrail`'s exact-pin and generated-surface drift idea where it is not already covered. Depends: none. Gate: G1.

### AUX-09 — Portable anti-slop rule subset

Outcome: a small rule set enforced mechanically. Encode a portable subset of `pi-ui`'s 17 rules as custom checks: no object-typed parameters, no `unknown` returns or parameters outside explicit boundaries, no chained type assertions, non-const assertions require a `SAFETY:` marker, no `filter().map()`, no accumulating spread. Do not vendor the oxlint plugin or pin oxlint. Touch points: `biome.json` or a new `check-conventions.ts` checker in `web/scripts`, plus `package.json` lint. Verify: the gate fails on a seeded violation and passes on the tree. Exit: rule list documented in the script header. Depends: AUX-22. Gate: G1.

### AUX-10 — Config schema generation gate

Outcome: a generated JSON Schema for controller and assistant configuration with a stale-check. `pi-ui` generates from TypeBox and runs `schema:check` in CI; `pi-web` shows the atomic, unknown-field-preserving write we already want. Delta: add a `generate-config-schema.ts` generator in `web/scripts` and a `check:config-schema` gate, wired into CI. Touch points: `web/scripts/`, `docs/`, `.pixie.example`. Verify: the gate fails on a stale schema and passes when regenerated. Exit: keep strict fail-closed validation; the schema is documentation and drift detection, not a relaxation. Depends: none. Gate: G1.

### AUX-25 — Host route, ownership and fixture parity

Outcome: every browser method and native host route has one generated owner, status and metadata shape, and tests cannot claim support that the Bun host does not advertise. Delta: derive a parity matrix from `shared/schema/protocol-catalog.json`, align `shared/tests/contracts/ws-catalog.test.ts` with the host status vocabulary, replace broad legacy hello fixtures with the generated supported set, and make `session.list` metadata requirements explicit. Touch points: `shared/schema/protocol-catalog.json`, generated catalogs, `shared/src/ws-runtime.ts`, `shared/tests/contracts/`, `web/internal/controller/pi_client.go`, `web/internal/controller/session_manager.go`, `web/internal/controller/session_lifecycle.go`, `web/tests/go/controller/support_test.go`, and `assistant/tests/host-capabilities.test.ts`. Verify: contract checks fail on an unknown native route or an advertised absent/unavailable route; controller tests use the real host profile and assert unsupported calls fail before side effects; list fixtures cover resident and cwd-scoped metadata. Exit: archive, MCP and session-list behavior are either implemented by their declared owner or explicitly unavailable, with no fixture-only success. Depends: none. Gate: G1.

### AUX-34 — Shared method-level runtime schemas

Outcome: direct TypeScript imports improve reuse without treating erased types as runtime validation. Delta: extend the schema generator or add explicit generated validators for method parameters, results and error envelopes, use them at the browser/controller and controller/host boundaries, and retain additive-version handling for unknown fields. Touch points: `shared/schema/`, `web/scripts/generate-contracts.ts`, `shared/src/ws-runtime.ts`, generated TypeScript and Go artifacts, `web/internal/controller/`, `assistant/src/host.ts`, and contract tests. Verify: malformed method payloads, invalid results and stale generated artifacts fail at the owning boundary; valid additive fields remain compatible. Exit: Go and TypeScript adapters share executable contract checks rather than only compile-time declarations. Depends: AUX-25. Gate: G1.

### AUX-35 — Pinned verification environment

Outcome: source checks distinguish a verified result from a run under an unsupported Bun version, missing native toolchain or exhausted temporary storage. Delta: add an early runtime-version gate for Bun `1.4.0`, direct Go temporary-directory configuration to a bounded workspace, and make probe/test wrappers report skipped or environment-blocked evidence without converting it to success. Touch points: root `package.json`, `web/scripts/`, `assistant/tests/`, `shared/tests/`, `docs/development.md`, and release/preflight checks. Verify: Bun `1.3.14`, missing writable temporary storage and unavailable CGO prerequisites fail with actionable classifications; pinned Bun `1.4.0` runs contract, host, probe and cross-module checks. Exit: no release or architecture decision relies on checks run under the wrong runtime or an incomplete filesystem/toolchain. Depends: none. Gate: G1.

## M1 — Session fidelity and CLI coexistence

The highest-leverage milestone: it turns "never attach to an active TUI" into a testable contract and makes browsing robust across Pi releases.

### AUX-13 — Session-file lease and mtime tail

Outcome: Pixie coexists with a native Pi CLI writer without clobbering it. Delta: add an advisory `<file>.jsonl.lease` with pid and mtime staleness that blocks only a live foreign writer, and re-read a session from disk when its mtime advances while it is not streaming, as `pi-gui` does (`pi-gui:packages/pi-sdk-driver/src/session-lease.ts:21,107-124`, `session-supervisor.ts:360-376`). Touch points: `assistant/src/host.ts`, `web/internal/controller/session_history.go`, `web/internal/controller/session_liveness.go`. Steps: define lease read/write helpers beside the session-dir adapter, take the lease only for mutating operations, release on release/reload, and tail on mtime for non-streaming reads. Verify: `assistant/tests` for lease staleness and concurrent-writer refusal; `web/tests/go/controller` for mtime-tail refresh. Exit: two owners on one session fail closed with a typed error, never interleave. Depends: none. Gate: G1.

### AUX-12 — Session schema version guard and repair

Outcome: newer-format sessions are detected and surfaced, not mis-parsed, and damaged sessions are repaired. Delta: parse the session header, compute `writtenByNewerRuntime`, and refuse to guess; drop unknown records and parse failures without rewriting; repair dangling tool calls by appending synthetic results on reopen (`pi-gui:packages/pi-sdk-driver/src/session-schema.ts:36-48`; `pi-web:lib/session-reader.ts:87-94`; `thinkrail:packages/server/src/agent/sessionRepair.ts:13-57`). Touch points: `assistant/src/host.ts`, `web/internal/controller/history.go`, `web/internal/controller/session_history.go`. Verify: `assistant/tests` fixtures for an unknown record, a bumped header version, and a dangling tool call. Exit: unknown input degrades safely and is recorded in the support snapshot. Depends: AUX-13. Gate: G1.

### AUX-08 — Unknown-event and projection policy tests

Outcome: the host ignores unknown session events and projects unknown transcript roles and tree kinds without throwing. Delta: add fixtures that feed unknown events, roles, and record kinds through the reducer and projector. Touch points: `assistant/tests`, `web/tests/go/controller`. Verify: `bun test` in `assistant` and `go test ./...` in `web`. Exit: a newer Pi within the pinned version cannot break projection. Depends: AUX-12. Gate: G1.

### AUX-14 — Two-tier branching

Outcome: "New session" and "Edit from here" work in Pi's native format. Delta: keep the existing `Fork` for a new independent file with a header parent link, and add edit-from-here as an in-file sibling that shares the original `parentId`, as `pi-web` does (`pi-web:lib/rpc-manager.ts:750-834`). Touch points: `web/internal/controller/session_lifecycle.go`, `session_actions.go`, `web/webui/src/chat/render/turns.svelte`, `web/webui/src/session/`. Verify: `web/tests/go/controller` for both branch identities, and a webui test for the edit affordance. Exit: ancestry is an iterative parent chain; forks are roots and only subagents nest; no custom branch schema. Depends: AUX-12. Gate: G1.

### AUX-15 — Monotonic cost and context projection

Outcome: cost and token counters never regress at compaction. Delta: aggregate every entry, including `compaction` and `branch_summary` usage, then merge live message deltas, per `pi-web` (`pi-web:lib/session-stats.ts:69-123`). Touch points: `web/webui/src/chat/session/session-stats.ts`, `web/internal/controller/session_events.go`, `types.go`. Verify: a webui test where a compaction removes older messages but counters hold. Exit: live context usage comes from the SDK; totals stay monotonic. Depends: none. Gate: G1.

## M2 — Lifecycle and admission

### AUX-05 — Generation guards and replacement epochs

Outcome: stale async results cannot mutate a session that was replaced in place. Delta: add an allocation generation, require identity and generation match for foreground callbacks, and fence late results with a replacement epoch, per `pi-ui` and `openpi`. Touch points: `assistant/src/host.ts`, `web/internal/controller/session_lifecycle.go`, `session_manager.go`, `session_actions.go`. Verify: `assistant/tests` and `web/tests/go/controller` tests that a delayed callback from the old session is discarded. Exit: a duplicate registration for one session path fails with a typed invariant error. Depends: none. Gate: G1.

### AUX-18 — Host supervision hardening

Outcome: the owner-locked host has a validated protocol and predictable restart behavior. Delta: validate every message against a typed request-correlated protocol with expected response types and per-request timeouts, classify commands as replacement/interrupt/unblock so a reload can preempt a prompt but not vice versa, stop gracefully before killing, and buffer partial stdout lines, per `openpi` (`openpi:electron/pi/sidecarHost.ts:78-171,338-359`, `sidecarCommandQueue.ts:3-51`). Also fix the gaps `openpi` confirms: no single-instance lock, a restart budget that never resets, no ready or version handshake, and silent prompt loss after restart (`sidecarHost.ts:148,285-294`). Touch points: `assistant/src/host.ts`, `assistant/src/serve.ts`, `assistant/src/probe.ts`, `web/cmd/pixie-cli/main.go`, `web/cmd/pixie-full/main.go`, `web/internal/ownerlock/`. Verify: `assistant/tests` for malformed, incompatible, and orphaned messages, queue ordering, and a restart that re-establishes a session or fails loudly. Exit: `runtime.hello` carries a version and readiness; a crash never silently drops a prompt. Depends: AUX-05. Gate: G1.

### AUX-04 — Compaction-aware prompt queue

Outcome: follow-up and steering input survive compaction. Delta: keep a separate compaction queue drained on compaction end and rebuild the SDK queue to remove one item, because no single-item API exists, per `pi-ui` (`pi-ui:src/agent/prompt-lifecycle.ts:70-177`). Touch points: `web/internal/controller/session_queues.go`, `session_events.go`, `assistant/src/host.ts`. Verify: `web/tests/go/controller` for a prompt submitted during compaction and delivered after. Exit: `Blocked` never becomes silent loss. Depends: AUX-05. Gate: G1.

### AUX-19 — Drain and quiesce admission gate

Outcome: update and rollback have a testable quiesce step. Delta: extend the owner lock or a mode-0600 local channel into an admission gate that refuses new prompts, forks, and resumes while in-flight runs finish, per `pi-web-ui` (`pi-web-ui:control-socket.ts`, `bin/pi-web-ui.mjs:1273-1284`). Touch points: `web/internal/ownerlock/`, `web/internal/controller/runtime.go`, `session_admission_test.go`, `runtime_status.go`. Verify: `go test ./...` for admission refusal during drain and clean release after. Exit: the Web UI reports quiescing, not errors. Depends: AUX-18. Gate: G1.

### AUX-03 — Steering contract spike

Outcome: a decision, then either a safe contract or an explicit unavailable with a recorded reason. Delta: Pi exposes `streamingBehavior`, `isStreaming`, and `preflightResult`; `pi-ui` binds steering with a host-owned generation plus preflight acceptance rather than a run identifier (`pi-ui:src/agent/prompt-lifecycle.ts:52-65`). Prototype behind the negotiated operation set using our generation guard from AUX-05. Touch points: `assistant/src/host.ts`, `web/internal/controller/session_queues.go`, `shared/schema/protocol-catalog.json`. Verify: `assistant/tests` for binding under replacement and rejection under compaction. Exit: if binding cannot be proven, keep `session.steer` unavailable and update `docs/sdk-coverage.md` with the exact limitation. Depends: AUX-05. Gate: G3.

### AUX-11 — Session index decision

Outcome: a measured decision, default no. Delta: if session listing is a measured bottleneck, adopt the append-only summary cache and dual watcher from `pi-ui` (`pi-ui:src/agent/session-summary-cache.ts:78-135`, `session-catalog.ts:274-304`). Touch points: `web/internal/controller/history.go`, `session_manager.go`. Verify: a benchmark under `web/tests/performance/` before and after. Exit: no second index is added without a measured need. Depends: none. Gate: G2.

### AUX-26 — Archive lifecycle state and metadata

Outcome: archive and unarchive have truthful, restart-safe semantics instead of dispatching absent Pi routes or reporting a legacy-fixture success. Delta: decide whether to add a controller-owned durable archive marker with migration, metadata-only restore and list reconciliation, or keep the surface explicitly unavailable; preserve native session identity, deletion authority and fail-closed recovery either way. Touch points: `web/internal/persist/`, `web/internal/controller/session_lifecycle.go`, `session_manager.go`, `history.go`, `shared/schema/protocol-catalog.json`, `shared/src/ws-protocol.ts`, Web UI archive controls, and lifecycle tests. Verify: archive/unarchive behavior survives restart, filters active versus archived records consistently, does not revoke or mutate native state without a durable decision, and emits lifecycle events only after publication. Exit: documentation, catalog, persistence and UI agree on one archive owner and no route is implied by a missing Pi API. Depends: AUX-25. Gate: G3.

### AUX-27 — Deletion admission and authority ordering

Outcome: native MCP authority is never lost merely because capability validation, attachment, binding or deletion-journal admission failed. Delta: validate the host profile and generation, establish the deletion binding, and persist the deletion intent before revoking native session authority; after durable admission, keep revocation and uncertain-dispatch recovery semantics explicit and retryable. Touch points: `web/internal/controller/session_lifecycle.go`, deletion-journal persistence, `web/internal/controller/session_manager.go`, `web/internal/mcpserver/`, and controller lifecycle tests. Verify: injected profile, unsupported-capability, attachment, binding and journal failures leave the session's native authority intact; post-admission dispatch and cleanup failures retain a reconciliable tombstone. Exit: every authority revocation is justified by a durable deletion record or a confirmed native deletion. Depends: AUX-25. Gate: G1.

## M3 — Transport and streaming

### AUX-16 — Revision-chained snapshot delta

Outcome: reconnects reconcile cheaply and large histories stay bounded. Delta: keep the existing replay cache and login snapshot; add append detection that walks entry identity and emits a delta carrying `rev`, `baseRev`, and `appended`, with client resync on a broken chain, per `pi-web-ui` (`pi-web-ui:server/agent-service.ts:1735,3195-3241`). Add `pi-forge`'s snapshot-first ordering, event allowlist, padded heartbeats, and explicit backpressure cap (`pi-forge:packages/server/src/sse-bridge.ts:141-189,495-512,622-628`). Touch points: `web/internal/controller/websocket.go`, `socket_output.go`, `replay.go`, `session_events.go`, `web/webui/src/connection/`. Verify: `web/tests/go/controller/websocket_test.go` and `socket_output_test.go` for delta correctness, seq ordering, and resync. Exit: a client that misses a frame resyncs instead of diverging. Depends: AUX-18. Gate: G1.

### AUX-17 — Streaming render discipline

Outcome: token streaming stays smooth on long sessions. Delta: separate an unthrottled delta channel from the throttled snapshot path keyed by a monotonic sequence; prefix-cache markdown so completed blocks freeze and only the tail re-parses; virtualize the timeline past a measured threshold (`pi-web-ui:server/agent-service.ts:2965-2988`, `src/stream-markdown.ts:1-36`; `pi-gui` `conversation-timeline.tsx:12`). Touch points: `web/webui/src/chat/render/markdown.svelte`, `web/webui/src/chat/runtime/session-runtime.ts`, `web/webui/src/chat/render/turns.svelte`. Verify: a webui test that completed blocks are not re-parsed and that fencing mid-stream is not highlighted. Exit: no O(n) work per token. Depends: AUX-16. Gate: G1.

### AUX-33 — Strict v2 hello negotiation

Outcome: a peer cannot claim a selected v2 protocol without proving the supported-version advertisement needed to validate it. Delta: require a non-empty `supportedProtocolVersions` list whenever v2 is selected, validate the selected version against the offered and advertised sets, and retain the empty-advertisement exception only for an explicitly negotiated legacy v1 path. Touch points: `shared/piprotocol/hostv2_negotiation.go`, `web/internal/controller/pi_client_v2.go`, host hello handlers, and Go negotiation tests. Verify: malformed v2 peers, mismatched highest mutual versions and silent downgrade attempts fail before requests; valid v1 and v2 handshakes remain compatible. Exit: protocol selection is fail-closed and independently testable. Depends: AUX-25. Gate: G1.

### AUX-06 — Provider usage projection

Outcome: usage and rate limits display without a second policy. Delta: reuse the auth Pi already resolved and add a short TTL with identity guards, per `pi-ui` (`pi-ui:src/agent/usage-controller.ts:52`, `provider-usage.ts:8-46`). Touch points: `web/internal/controller/provider_inventory.go`, `provider_configuration.go`, `pi_events.go`, `web/webui/src/chat/session/session-stats.ts`. Verify: a controller test with a stubbed provider response and a TTL-expiry case. Exit: Pixie never writes provider configuration. Depends: none. Gate: G1.

## M4 — Security and hardening

### AUX-02 — Local-request trust regression tests

Outcome: the existing auth and Host/Origin handling is proven against rebinding and cross-site writes. Delta: tests only; the implementation exists in `web/internal/controller/auth.go`. Touch points: `web/tests/go/controller`. Verify: `go test ./...` with non-loopback `Host`, cross-site `Origin`, and a rebinding-style request rejected whenever unauthenticated trust is off, and authenticated remote access still requiring the exact public origin. Exit: the documented unauthenticated-LAN exception is covered and narrow. Depends: none. Gate: G1.

### AUX-01 — Transcript egress hardening

Outcome: model output cannot beacon to arbitrary hosts. Delta: chat renders micromark through `{@html}` and `index.html` sets no `img-src` policy, so a markdown image auto-loads. Enforce an app-document CSP permitting only `self`, `data:`, and `blob:` images, or rewrite and drop remote images. Touch points: `web/webui/index.html`, `web/webui/src/chat/render/markdown.svelte`, `web/internal/controller/static_files.go`. Verify: a webui test that a remote image is not emitted as a live `src`, and a header assertion on the served document. Exit: no remote resource loads from transcript content. Depends: AUX-02. Gate: G1.

### AUX-20 — Optional auth hardening

Outcome: the optional authenticated mode matches mature single-tenant deployments. Delta: a signing secret generated and persisted like an SSH host key, a password hash stored with its scrypt parameters and compared in constant time, login lockout with per-IP rate limiting, an inactivity timeout, and Fetch Metadata plus strict `Host` checks, per `pi-forge` and `pi-web` (`pi-forge:packages/server/src/config.ts:360-385`, `auth.ts:21-25,250-264,307-314`; `pi-web:lib/request-security.ts:90-155`). Touch points: `web/internal/controller/auth.go`, `web/internal/persist/`, `docs/security.md`. Verify: `web/tests/go/controller` for lockout, expiry, and metadata rejection. Exit: keep our single-user model and distinct tokens. Depends: AUX-02. Gate: G1.

### AUX-21 — File and Git containment primitives

Outcome: stronger read-only inspection without new write authority. Delta: add a realpath walk-up that defeats symlink escapes with traversal mapped to 403, a per-cwd Git mutex with a soft timeout, and per-turn diffs from `write`/`edit` tool calls with a `git diff HEAD` fallback, per `pi-forge` and `openpi` (`pi-forge:packages/server/src/file-manager.ts:171-232`, `git-runner.ts:16-20`; `openpi:electron/git/gitLock.ts:16-88`). Touch points: `web/internal/workspace/`, `web/internal/controller/session_history.go`. Verify: `web/tests/go/workspace` for symlink escape, and a Git failure rendered as a result, not a 500. Exit: no write path is added. Depends: none. Gate: G1.

### AUX-28 — Supervisor child-stderr redaction boundary

Outcome: live supervisor output and retained child diagnostics share one bounded, redacted boundary. Delta: remove the raw `io.MultiWriter` mirror or pass it through the same secret/path sanitizer and bounded line sink as `diagnostics.StderrRing`; preserve operator-visible lifecycle summaries without exporting raw child text. Touch points: `web/cmd/pixie-full/main.go`, `web/internal/diagnostics/stderr.go`, supervisor tests, and deployment logging tests. Verify: a child that emits bearer tokens, configured secrets, URLs and absolute paths cannot place raw values in console output, retained rings or support snapshots, including partial lines and write failures. Exit: no child stderr path bypasses redaction. Depends: AUX-18. Gate: G1.

### AUX-29 — Assistant fatal-path redaction

Outcome: assistant startup and shutdown failures cannot leak the resolved bearer secret or path-shaped configuration values. Delta: thread the resolved secret through `fail`, `fatalServeMessage` and every startup error path, and make spaced absolute-path redaction deterministic without weakening ordinary prose. Touch points: `assistant/src/serve.ts`, `assistant/src/log.ts`, `assistant/tests/serve.test.ts`, and redaction fixtures. Verify: real 32-character secrets, `secret=...`, endpoint URLs, quoted paths and paths containing spaces are absent from fatal stderr while stable failure context remains. Exit: configured secrets and absolute paths are redacted before any serve error reaches stderr. Depends: AUX-28. Gate: G1.

### AUX-30 — Structured logger and stderr-buffer safety

Outcome: hostile or malformed diagnostic values cannot escape the TypeScript logger's bounds or mutate its record structure. Delta: cover `=` and `:` credential assignments, common API-key forms such as `AKIA...`, null-prototype or safe-key structured records, finite positive capacity normalization, and buffering of incomplete stderr lines across appends before redaction. Touch points: `assistant/src/log.ts` and `assistant/tests/log.test.ts` or dedicated logging tests. Verify: `token=`, `api_key=`, `password=`, AWS-style keys, `__proto__`, `NaN`, `Infinity`, split secrets and split paths are redacted or bounded deterministically. Exit: logger output is secret-safe, prototype-safe, finite and chunk-safe. Depends: AUX-29. Gate: G1.

### AUX-31 — Go support-snapshot opaque-token redaction

Outcome: support exports do not retain a configured secret or an unlabeled opaque credential merely because it lacks a known prefix. Delta: pass configured secrets into the sanitizer and add a bounded opaque-token rule with explicit exclusions for required, labeled identifiers; preserve the existing URL, path and credential-label behavior. Touch points: `web/internal/diagnostics/support_snapshot.go`, `stderr.go`, configured-runtime wiring, and diagnostics tests. Verify: bare configured secrets, digest-shaped tokens and ordinary project/session identifiers are classified as intended in rings, snapshots and support exports. Exit: a support snapshot cannot expose configured or recognized opaque credentials, and redaction does not erase the identifiers needed for recovery. Depends: AUX-28. Gate: G1.

### AUX-32 — Native-error browser boundary

Outcome: Pi and SDK failures retain actionable stable codes without exposing raw paths, URLs, credentials or arbitrary native text to the browser. Delta: normalize host replies and controller projections to safe error messages at the boundary, log only the redacted cause, and keep unknown native details out of persisted session events and replay. Touch points: `assistant/src/host.ts`, `web/internal/controller/websocket.go`, `session_events.go`, shared error contracts, and host/controller tests. Verify: fake native errors containing secrets, paths and URLs produce bounded safe browser replies while sanitized diagnostics retain the failure class. Exit: browser-visible errors are stable and secret-free, and raw native errors never become session state. Depends: AUX-28. Gate: G1.

## M5 — Gated and conditional

### AUX-23 — PTY transport contract

Outcome: a defined contract for a future terminal surface and for the PTY evidence our release still lacks. Delta: one PTY per session-scoped socket with an origin-gated upgrade, a ready handshake with a client input queue, resize messages, and kill on socket close; add a rolling replay buffer, idle reap, SIGTERM then SIGKILL, and a fail-safe environment allowlist with explicit passthrough, per `leyline` and `pi-forge` (`leyline:server/pi-api/terminal.js:65-116`; `pi-forge:packages/server/src/pty-manager.ts:61-70,182-268,295-325,399-441`). Do not bundle native `node-pty` and a gyp toolchain into a Bun image. Touch points: `web/internal/controller/`, `web/webui/src/`. Verify: live PTY evidence on both architectures. Exit: no terminal work starts before the terminal-surface decision. Depends: a product decision. Gate: G2.

### AUX-24 — Orchestration tools, conditional on containment

Outcome: optional child-agent tools that respect the worker boundary. Delta: expose `create_child_thread`, `list_threads`, `read_thread`, and `send_message_to_thread` as Pi tools with parent-owned supervision and cancel cascading, per `pi-gui` (`pi-gui:packages/pi-sdk-driver/src/orchestration-runtime.ts:232-249`). This is conditional: `pi-gui`'s children share the parent working directory with no isolation, which violates our rule, so it may proceed only with the enforced worker boundary from the stream plans. Touch points: `assistant/src/`, `web/internal/controller/`, `roadmap/roadmap-canvas.md`. Verify: containment tests require a canary read to fail and the network to be unavailable. Exit: absent enforcement makes the feature unavailable rather than unrestricted. Depends: worker launcher, roadmap-canvas. Gate: G2, G4.

## Critical path and sequencing

The critical path is AUX-13 → AUX-12 → AUX-14 → AUX-16 → AUX-17. AUX-25 gates AUX-34, AUX-26 and AUX-27. AUX-18 gates AUX-16, AUX-19 and AUX-28. AUX-05 gates AUX-18, AUX-04 and AUX-03. AUX-29 depends on AUX-28, AUX-30 depends on AUX-29, and AUX-31/AUX-32 consume the same redaction boundary. AUX-35 gates interpretation of all source and release checks. M0 has no dependencies and should land first so the later invariants are enforced. No G2 or G3 task blocks a G1 task; if AUX-03 or AUX-26 ends unavailable, the remaining contract and hardening work still proceeds.

Suggested order of execution: AUX-22, AUX-25, AUX-34, AUX-35, AUX-09, AUX-10, then AUX-13, AUX-12, AUX-08, AUX-14, AUX-15, then AUX-05, AUX-18, AUX-27, AUX-28, AUX-29, AUX-30, AUX-31, AUX-32, AUX-33, AUX-04, AUX-19, then AUX-16, AUX-17, AUX-06, AUX-26, AUX-02, AUX-01, AUX-20, AUX-21, and finally the gated AUX-23, AUX-24 and the AUX-11 decision.

## Constraints and invariants

These hold for every task. Do not add a row to the frozen release-evidence matrix under `web/scripts`. Do not change the three-product target. Do not add a second model, agent, or MCP policy. Do not introduce a Tailwind dependency or a second generated visual system. Do not add a worker without enforced containment. Do not attach to an active TUI; use the lease and an explicit idle handoff. Keep one owner per file and one lifecycle owner per DOM region. Remote publication and live deployment require separate authorization.

## Not adopting

- No browser authentication or loopback-only trust. `pi-ui` has no auth, CSRF, `Origin`, or `Host` check, and `--host` exposes file read and write to the network.
- Out-of-workspace file read, edit, and download. `pi-ui`'s read/write path deliberately bypasses containment (`pi-ui:src/server/workspace-files.ts:253-275`).
- A Datastar hypermedia rewrite; we have a Svelte 5 and Mewa investment.
- An inline provider extension such as `pi-ui`'s llama.cpp provider; it conflicts with our no-second-model-policy rule.
- Bundling Pi into the assistant or setting `PI_BUNDLED_NODE`; our bundle externalizes Pi and the shipped `runtime/node_modules` serves the TUI.
- Deep `node_modules/dist` imports, a patched shiki distribution, beta pins, and a machine-local formatter path (`pi-ui`).
- No single-instance lock and non-atomic state writes (`thinkrail:architecture.md:218-220`; `openpi`).
- Never-resetting restart budgets, a missing ready or version handshake, and silent prompt loss after restart (`openpi:electron/pi/sidecarHost.ts:148,285-294`).
- Monkey-patching the SDK as a mandatory first import (`pi-web-ui:server/agent-service.ts:13-16`).
- Passing the full server environment, including secrets, to a spawned shell (`leyline:server/pi-api/terminal.js:147-157`).
- Treating a loopback origin allowlist as authentication (`leyline:server/cors.js:31-39`).
- An opt-in subprocess sandbox presented as containment (`pi-forge:docs/agent-tool-sandbox.md`).
- Multi-thousand-line god modules and shape-only forward compatibility with no schema version (`pi-web-ui:server/serialize.ts:199-206`).

## Source evidence

The reviews were read-only clone inspections. `pi-ui` is MIT with an anti-slop oxlint plugin, a Datastar hypermedia UI, and confirmed security weaknesses. `thinkrail` contributes a versioned wire protocol, replay and ack, and executable boundary gates. `leyline` contributes the PTY-over-WebSocket shape. `pi-web-ui` contributes revision-chained snapshots, a separate delta channel, prefix-cached markdown, and a control socket. `pi-web` contributes native branching, direct session-file compatibility, monotonic cost projection, and atomic config edits. `pi-forge` contributes single-tenant auth, layered path containment, Git review, and a PTY environment allowlist. `openpi` contributes the supervised sidecar and its negative checklist. `pi-gui` contributes the session lease, schema-version guard, worktree and orchestration models.

## Evidence limits

No external repository was built or run, and their citations are spot-checked, not independently reproduced. Our assistant bundle size and Pi externalization were verified locally with `bun build`. There is no runtime, arm64, Docker, systemd, or credentialed Pi evidence here. Several tasks depend on measurements or decisions that do not exist yet, and the interaction of AUX-03 through AUX-35 with a live Pi remains unproven.
