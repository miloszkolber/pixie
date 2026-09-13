# Pixie roadmap and investigation center

This is the canonical implementation status, investigation backlog, and forward plan for Pixie. Operating behavior belongs in `docs/`. A source helper, mock, compiler pass, or legacy fixture is not final Go-assistant, cross-platform, deployment, or release evidence.

## Current verdict

**No-go for Go-assistant cutover, release, or untrusted Browser work.** A third repair pass landed the P0 execution-integrity fixes at source and unit-test level: one native child per logical session with a durable path/cwd registry, immutable event/run ownership, separate acceptance and settlement states, exhaustive fail-closed operation negotiation, non-blocking deletion recovery bound to a stable persisted host identity, and a full-host lifecycle that fails closed. The operator approved removal of the legacy Bun service; its retained behaviors are now either implemented in Go, delegated to the opt-in bridge, or explicitly reduced. The remaining blockers are the unported native parity and administration surface, the unwired host-v2/pairing/migration path, and missing live release evidence. No P0 repair has been exercised against a real pinned Pi distribution in this checkout.

The authorized local amd64 deployment uses controller image `pixie-local:8c3d4e75` from checkout `8c3d4e75a268b61bf02854cb6e4d2f59b37e0b40`. The controller is healthy, loopback-bound, authenticated, non-root, read-only, capability-dropped, and resource-limited. The legacy Bun assistant is removed from the repository and the production assistant is now the Go binary, installed from the fresh `sha-d0576bae586d` assistant archive as a user systemd service (`~core/.config/systemd/user/pixie-assistant.service`, binary and bridge beside it, config and env under `~core/.config/pixie`). The old Bun system unit is stopped and disabled, with its unit file backed up at `/root/pixie-assistant.service.bak`. The production controller reports `applicationReady`/`configured`/`reachable` with `agentProfile.name=Pi`, `compatible=true`, `version=embedded-go-native`. A non-production Go full-host test instance (`pixie-e2e`, port 7412) also runs. This is local amd64 evidence against a source build, not a final artifact or release.

Current fail-closed gates agree with that verdict:

- `check-coverage` consumes a produced input: FC01 actual/live evidence from the staged-binary `--version`/`doctor` probes and the optional readiness probe, plus explicit operator-approved reductions for the remaining 27 mandatory FC rows, the 14 X rows and Gates 1–5 in `package/contracts/reductions.json`. Any row that is neither executed nor reduced still fails closed; the repaired behaviors have focused Go regression tests, not per-row live coverage.
- `check-package-artifacts` consumes the staged bundle and verifies the four commit-named archives, their unit/config contents, the static command sources and the executed native `--version`/`doctor` probes. The `packageArtifacts` reduction group exempts only non-native arm64 execution, readiness, lifecycle, uninstall, host embedded-UI and the full-host native-engine facade; a reduced row is reported as reduced, never as passing, and every structural row still fails closed.
- `check-performance` measures all 4 required process targets: the assistant and full-host binaries on amd64 and, from a native `ubuntu-24.04-arm` job whose measurements the workflow merges before collection, on arm64, each with fresh-process startup p50/p95 and peak RSS. Only the legacy worker/decoded-buffer fields remain reduced.
- `release-gate` consumes the same bundle plus the staged local manifest the collector embeds. Static workflow publication-policy checks, source reachability, archive/binary identity, archive checksum consistency and the controller-image evidence mapping stay mandatory. The `releaseGate` reduction group exempts only Git tag/GitHub Release state, the published ghcr.io tag, registry provenance/SBOM, registry image/platform digests and latest-promotion ancestry; those rows are reported as reduced, not passed, and still require a real publication to produce.
- Repository lint is green after the legacy removal: `bun run lint` reports 0 errors, 58 warnings and 5 informational diagnostics, all warnings in the retained bridge and test/source tree.
- `bun test tests` is green at 528 pass / 0 fail under the pinned Bun 1.4.0; `bun test assistant/bridge` is green at 34 pass / 0 fail. The removed parity suites account for the earlier larger count.
- Both Go modules pass `go test -race` on the host with Go 1.27.0, including a fixed test-side supervisor map race. The opt-in `assistant/host/native_real_test.go` harness passes against the host's official Pi 0.85.1 for session ownership and, with a live provider, a real prompt that settles with a terminal stop reason and records the assistant message. Real-Pi testing found and fixed a host bug that fatally misclassified official `extension_ui_request` events as the removed compatibility protocol.

## Authorized execution plan

The operator authorized live host testing and settled the open decisions. Execution is in dependency order; each item records its state.

- **D1 shared contracts: done.** `package/contracts/schema/protocol-catalog.json` is the single source and generates the TypeScript and Go catalogs; `check:contracts` regenerates and fails on drift in static CI. The Go host still reads its own `nativeOperationSet` literal rather than the generated catalog, so binding it remains open.
- **D2 authority:** select host v2 and wire durable pairing (`RequirePairedRecovery`, `DeletionBindingV2`) as the production deletion authority. Outline: additive `runtime.hello` negotiation (controller offers `[2,1]` and prefers 2; the host selects per connection and mixed peers close rather than downgrade) with the v1 envelope adapter retained; an explicit pairing ceremony stores only a verifier hash in `pi-pairing-authority.json`; recovery calls `RequirePairedRecovery` before a flag-gated dual-proof legacy matcher; authority ledgers are never rewound and pairing is added to `AuthorityLedgerFiles`. Rollout: `v1` (default, byte-identical) → `auto` negotiation → operator pairing → `paired` strict authority → `v2`-only, behind `PIXIE_PI_PROTOCOL` and `PIXIE_DELETION_AUTHORITY`. Progress: `PIXIE_DELETION_AUTHORITY` (`auto` default, `paired`, `legacy`) is wired into requested-deletion recovery with a derived storage key; host-v2 envelopes, per-connection negotiation and mixed-peer rejection are wired behind `PIXIE_PI_PROTOCOL` with v1 byte-identical by default; the operator ceremony (`pixie pair`/`revoke-pairing`/`rotate-pairing`) exists; and `pi-pairing-authority.json` is an authority-ledger file. The shared protocol package now lives at `package/contracts/piprotocol` so both modules can import it without reaching into `internal`. Requested records now persist a distinguishable `deletion-binding-v2:` binding when paired, and paired/auto recovery verifies the specific session binding before dispatch; legacy-bound records quarantine in `paired` mode. Rehearsal: the staged-apply, rollback, topology-switch planning and no-ledger-rewind engine passes on the host against real files (persist and controller migration/rollback/topology suites). Open: production startup/transport still negotiates v1 and the migration helpers have no production caller, so a real systemd topology switch/rollback is not yet wired (defect 7).
- **D3 Browser: pointer model implemented and host-verified.** Pixie hosts no browser and never proxies MCP traffic. The controller stores one setting, registers Pixie's entry in Pi's effective MCP configuration through `pi.mcp.servers.*` and reports a bounded probe (`package/internal/controller/mcp_browser.go`, `package/tests/go/controller/browser_mcp_test.go`); the wire surface is `browserMcp.status`/`browserMcp.configure`/`browserMcp.remove` with the `BrowserMCPStatus` contract. The WebUI exposes Settings → Browser with name, URL, enabled toggle, register/update, remove and full probe status. `docker-compose.yaml` ships an optional `pixie-browser` service (Obscura MCP HTTP, its own network, host-loopback publish only, read-only rootfs, non-root, resource limits); `pixie` does not depend on it. Verified on the host: the Compose `pixie-browser` service starts (`h4ckf0r0day/obscura:latest`, `obscura-mcp 0.2.2`) and its MCP `initialize`/`tools-list` return the 37 `browser_*` tools, and Pi's `pi-mcp-adapter` `loadMcpConfig` resolves the registration entry. Host-verified end to end through the live assistant: `pi.mcp.servers.read` resolves the agent-dir entry, `probe` reports `obscura-mcp 0.2.2` reachable with the tool list, `remove` clears it and `upsert` re-registers it. Host-verified through a freshly built controller image as well: `browserMcp.status` reported the entry registered in the `agent-dir` layer with Obscura reachable and 37 tools, and `browserMcp.configure` persisted the setting and returned `enabled`/`registered`/`reachable` true. The endpoint's hardening, isolation and egress are the deployment's responsibility.
- **D4 reductions confirmed:** FC24 (subagents), FC27 (local llama.cpp) and FC28 (Signet) are optional and not release-blocking; FC01–FC23, FC25, FC26 and FC29–FC31 remain required. Named reductions are also approved for FC15 (no observable working-message and no request-specific cancellation frame upstream), the FC16 editor read path (`getEditorText()` returns empty; the write projection remains), `pi.tools.call` (upstream blocks execution), and `session.prompt.resource` (no RPC representation). FC09 native delete stays reduced per D5.
- **Bridge authorized:** the opt-in administration bridge may be implemented as a sidecar using the selected installation's public SDK (`ModelRuntime`, `SettingsManager`, `DefaultPackageManager`, `AgentSession`/`ExtensionAPI`, `pi-mcp-adapter` events), explicitly opt-in and fail-closed when the selected installation cannot be verified.
- **D5 native delete (priority):** verified against the installed Pi 0.85.1 RPC command union (`dist/modes/rpc/rpc-types.*`): it has no `delete`/`remove` command, so Go `session.delete` stays unsupported and reconciliation is confirm/retain. The same union does expose `fork`, `clone`, `set_model`, `get_available_models`, `set_thinking_level`, `get_available_thinking_levels`, `get_messages`, `get_session_stats`, `get_entries`, `export_html`, `switch_session`, `compact`, `steer`, `follow_up` and `clear_queue`, so several currently-false Go operations (GO-04/GO-06, FC06/FC10/FC11/FC07) are implementable without new upstream APIs.
- **D6 evidence: producers implemented.** A versioned bundle, a local archive/OCI collector and packaged-binary `--version`/`doctor`/readiness probes exist and feed the gates. `package/scripts/produce-evidence-inputs.ts` emits `coverage.json` and `performance.json` from the staged archives plus the optional running host, merges `package/contracts/reductions.json`, and only marks checks that ran as actual/live. `check-package-artifacts` and `release-gate` load the same file's `packageArtifacts` and `releaseGate` groups, report reduced rows as reduced rather than passing, and keep structural, static-policy, reachability and checksum checks mandatory. Registry provenance/SBOM, a real publication and the remaining live matrix stay unproduced or explicitly reduced; arm64 performance is now produced natively by an `ubuntu-24.04-arm` job and merged before collection.
- **D7 evidence bundle schema:** accepted; the versioned schema records source commit, release id, platform, profile, exact command, artifact digest, timestamp and result per assertion.
- **D8 Settings: done.** The modal is removed; the primary-area Settings view remains reachable in every connection state, and the deletion-recovery section is added.
- **arm64: partially evidenced.** A Mac arm64 pass (`roadmap/arm64-test-pr23.md`) ran the workspace and assistant Go tests, a serialized `-race` suite, both release archives with matching checksums, and the packaged full-host `/readyz`/`/health`/`/livez` on a Linux arm64 VM, all green. The pass predates the legacy removal, so its Bun-host parity and percent-encoded-patch findings no longer apply. The release workflow now measures native arm64 performance on `ubuntu-24.04-arm`; non-native arm64 `--version`/`doctor` probes remain reduced. Open: the OCI image build (verify with Linux BuildKit or a GitHub arm64 runner), fresh-artifact systemd lifecycle, and macOS cannot compile the Go tests directly because the source uses Linux-only `unix.O_PATH`.
- **Live target: cut over to Go.** The production assistant is the Go binary installed from the fresh archive as a user systemd service; the legacy Bun unit is stopped, disabled and backed up. Fresh-archive lifecycle exercised on the host: install from archive, stop (inactive), start (active, controller reachable), restart (active, controller reachable and compatible), `RestartForceExitStatus=75`, and the `pixie-assistant uninstall` plan. Rollback is restoring `/root/pixie-assistant.service.bak` and the legacy source at `e940743`. A non-production Go full-host test instance uses separate ports, data dir and a copied agent directory. `PIXIE_PI_SECRET_KEY` was exposed in local tool output and should be rotated before any public sharing.

## Confirmed implemented baseline

- The Go repository builds separate `pixie-assistant`, full-host `pixie`, and controller-only `pixie` compositions. Docker correctly runs controller-only mode and never starts Pi.
- The Go host owns one immutable child per logical session, launched in that session's admitted cwd, with a durable `native-sessions.json` mapping logical ID to absolute native path and cwd, exact identity checks before every load/prompt/cancel/release, a per-session installation lock, and per-event session ownership instead of a mutable current-session field.
- The Go host blocks `session.prompt` until the native run settles and returns the terminal stop reason; prompt acceptance, uncertain dispatch, and proven rejection use distinct typed errors (`-32003`/`-32004`). The controller clears a reattached run on the authoritative `agent_settled` signal and only rolls back a proven structured rejection.
- Hello advertises an exhaustive `operationSet`. A missing or false operation is rejected before dispatch; unsupported delete cannot journal a durable intent. Unsupported create-time model/thinking/MCP overrides and text-resource prompts are rejected before any native mutation.
- The Go host persists a stable host identity under the agent directory and exposes it as `runtimeId`. Deletion binding v2 excludes ephemeral endpoint, port and transport secret, with legacy endpoint-bound records still matchable; unmatched or unsupported records are retained and surfaced instead of aborting startup.
- Full-host startup fails without an absolute Pi agent directory and a resolvable Pi executable; the composition joins `assistant.Errors()` with the controller; assistant readiness degrades while a lost session awaits reload; `runtime.restart` is explicit opt-in and exits status 75; generated install instructions include the private environment file and Pi selection.
- A source-built Go full-host ran under real user-systemd on the host as `core` with `RestartForceExitStatus=75`, a copied Pi agent directory and an isolated data dir: `/health`, `/livez` and `/readyz` returned 200, `/readyz` reported `applicationReady=true`, `configured=true`, `reachable=true`, `agentProfile.name=Pi`, `compatible=true`, the embedded UI served `<title>pixie</title>`, and stop/restart were clean. The status-75 reload path is now exercised end to end: `pi.reload` returns `{"ok":true}` and systemd replaces the process with a new boot identity. Live testing also found and fixed the host rejecting `params: null` on no-argument admin calls, which had broken `runtime.restart` and every other parameterless admin operation.
- Bounded native JSONL parsing, aggregate admission, control reserve, pending limits, timeouts, and process-group teardown have focused tests.
- Controller Host/Origin policy, loopback Pi restrictions, reserved module routes, non-executing Git inspection, bounded controller child termination, typed persistence outcomes, and storage bounds have focused implementation tests. Settings now reconciles a durability-uncertain config publish against the visible primary instead of leaving the cache stale. `session_state.go` and `project-root-migration.go` still call `persist.Write`, but they re-read the primary and do not cache a divergent value.
- The six-slot workspace, responsive Settings behavior, rail keyboard navigation, read-only Files/Git views and Mewa foundations have source or fixture acceptance.
- The local controller is live and healthy; the legacy assistant source is removed from the repository, and any deployed instance remains until cutover. Both Go modules pass `CGO_ENABLED=0 go test -count=1 ./...` and `CGO_ENABLED=0 go vet ./...`; this verifies isolated Go code, not full integration.
- npm assistant publication has been removed. The assistant workspace now contains only the opt-in Bun administration bridge and its typecheck; it is not a fallback assistant or a release artifact.

## Confirmed defects and integration risks

### P0 — execution integrity (repaired at source; real-Pi integration unproven)

1. **Wrong session and cwd — GO-03/GO-04/GO-05/GO-07, FC02–FC07, X06/X10/X11. Repaired at source.** The host now keeps one immutable child per logical session in the admitted cwd, records a durable logical-ID-to-path/cwd mapping, verifies exact native identity before load/prompt/cancel/release, and attributes each event to the bound session. Focused tests cover A/B concurrency, same-cwd isolation, registry restart, release readmission, and event attribution. Still missing: execution against a real pinned Pi distribution, A/B/A with live providers, and schedule-versus-chat interleaving.

2. **Acceptance is mistaken for settlement — GO-05, FC03/FC05/FC11, X10. Repaired at source.** The host separates accepted from settled, waits for the pre-dispatch barrier plus `agent_settled` (or an authoritative post-acceptance state probe) before returning, and classifies rejected versus uncertain prompts. The controller clears a reattached run on `agent_settled` and admits follow-ups only afterwards. Focused tests cover late settlement, timeout poisoning, reconnect settlement, and proven-rejection rollback. Still missing: real-provider retry/compaction event-order testing.

3. **False capabilities and startup-blocking deletion recovery — API-01/FIX-01/BUILD-05, FC07–FC09/FC22. Repaired at source; reconciliation UX incomplete.** Hello advertises an exhaustive `operationSet`; missing/false operations fail closed before mutation, and unsupported delete cannot write a durable intent. The host persists a stable identity exposed as `runtimeId`; deletion binding v2 excludes endpoint/port/secret and still matches legacy records. Recovery quarantines unmatched or unsupported records, keeps their tombstones, surfaces them in readiness/welcome, and exposes `session.deletionRecovery`/`session.confirmExternalDeletion`. Still missing: a WebUI reconciliation view, retry-same-authority execution (Go delete remains unsupported), migration/quarantine of pre-v2 unreadable records, and a non-test binding between the host's hand-maintained `nativeOperationSet` and the controller RPC call sites (the exhaustive binding covers browser/controller only). Durable pairing authority (API-03) belongs to defect 7 and remains helper-only.

### P1 — retained functionality and lifecycle

4. **Model, thinking, and resource contracts disagree — FC04/FC10/FC17–FC19. Partially repaired.** Go now rejects create-time model/thinking/MCP overrides and text-resource prompts before any native mutation, and its snapshot returns real provider/model/thinking config options. The controller already gates resources on `session.prompt.resource`. The Go host now implements `session.configure` (model/thinking) with focused tests, and the controller sends the canonical `session.steer`/`session.rename` names it negotiates. Still missing: live provider/model evidence for FC10, one generated typed schema shared by Go and TypeScript, authoritative snapshot projection for provider/model catalogs, and transactional or reconciled create when a settings step fails.

5. **Full-host packaging and supervision — BUILD-03/BUILD-05/PKG-01–PKG-03, FC22. Substantially repaired at source.** Full-host startup now requires an absolute agent directory and a resolvable Pi executable, `serveFullHost` joins `assistant.Errors()` and the controller error channel, assistant readiness degrades while a lost session awaits reload, `runtime.restart` is explicit opt-in and exits status 75, packaged `pixie.json` selects Pi, and generated install instructions include the private environment file and selection. Fresh-archive amd64 lifecycle is now exercised: installed the `sha-2705eebfbda3` full-host archive as a user unit, stop/start/restart with `/readyz` 200, `RestartForceExitStatus=75`, and the `pixie uninstall` plan. Still missing: arm64 and upgrade/rollback archive lifecycle.

6. **Native administration and parity remain incomplete — BRIDGE-01–BRIDGE-03/GO-06/GO-08/GO-09/EXT-01/EXT-04, FC13–FC28.** The legacy Bun tree and its parity tests are removed by explicit operator approval. The opt-in administration bridge carries the supported selected-Pi public-API surface, and unsupported operations stay absent and fail closed; it does not prove the Go binary or independent npm/standalone Pi profiles. Required fix: integrate only supported selected-Pi public APIs, preserve explicit unsupported states, and run each operation through the Go binary against both distributions. FC15 stays a cutover gate: the retained public API cannot provide request-specific dialog cancellation or non-empty editor text, so exact working hints and UI cancellation are known-reduced and must not be presented as parity.

### P1 — architecture and delivery

7. **Host v2, durable pairing, and staged migration are helper-only — FIX-05/API-02/API-03/MIG-01, X04/X06/X14.** Production host/controller negotiate v1. Host-v2 envelopes, durable authority, staged migration, topology switch, and rollback exist as test/helper code without production callers. Required fix: select and wire one protocol/identity model through startup, transport, recovery, and real topology transitions before claiming migration.

8. **Browser MCP pointer — SEC-02/EXT-03, FC31, X12/X13. Implemented; assistant path host-verified.** Pixie no longer runs or hardens a browser, so containment moved to the operator. The controller persists one setting, upserts or removes only Pixie's entry through Pi's `pi.mcp.servers.*` operations, then probes the endpoint and reports `registered`, `layer`, `path`, `disabled`, `reachable`, `serverInfo`, `protocolVersion`, `tools` and `error`; a missing assistant, failed host call or failed probe returns an error rather than a success-shaped status (`package/internal/controller/mcp_browser.go`). The endpoint is operator-chosen and may be unauthenticated; hardening, network isolation and egress are the deployment's responsibility, and Pixie never proxies traffic. Compose ships an optional `pixie-browser` service on its own network with a non-root user, read-only rootfs and resource limits; `pixie` does not depend on it. Host-verified: the optional Compose service runs and speaks MCP (37 browser tools), and Pi's adapter resolves Pixie's registration entry. Host-verified: the live assistant advertises the four `pi.mcp.servers.*` operations, resolves and mutates the entry, and probes Obscura; a freshly built controller image returned the composed status and performed a persisted configure through its authenticated WebSocket.

9. **CI evidence order — REL-01–REL-04/COVERAGE-01/PKG-01–PKG-03/PERF-01. Sequenced; producer wired.** Static validation no longer runs the live gates. `stage` and `image` produce exact-commit archives and the OCI tar first; the `evidence` job downloads both, runs `produce-evidence-inputs.ts`, collects the versioned bundle (D7), and runs coverage/package/performance/release gates; `publish-image` and `publish` depend on that evidence job. Artifact names use the identity job's source commit instead of `github.sha`. The producer emits FC01 live evidence from executed staged-binary probes, fresh-process amd64 startup measurements, and the committed operator-approved reductions; `collect-evidence.ts` inspects the four archives, the staged local `release-manifest.json` and the docker-save/OCI image tar, hashes the contained binaries, and feeds `check-package-artifacts --evidence` and `release-gate --evidence`. Both gates load the committed `packageArtifacts`/`releaseGate` reductions, report reduced rows as reduced instead of passing, and keep structural archive/systemd/config checks, static workflow policy, source reachability, archive/binary identity and checksum consistency mandatory. Still missing and explicitly reduced or fail-closed: full per-row live coverage, non-native arm64 `--version`/`doctor` execution, packaged-binary lifecycle/uninstall/embedded-UI and full-host facade probes, Git tag/GitHub Release/registry provenance/SBOM/latest evidence, and arm64 platform digests; arm64 performance is now produced natively and is no longer reduced.

10. **Operating documentation previously outran implementation — DOC-01/DOC-02.** Full-host topology, prompt settlement, stable identity, and migration descriptions were corrected to the observed behavior. Documentation checks validate links and commands, not truth of runtime claims; the remaining host-v2, browser-MCP and parity sections stay explicitly incomplete.

### Evidence anchors for continued investigation

| Area | Primary source anchors |
| --- | --- |
| Session targeting, cwd, supported methods, prompt conversion | `assistant/host/native_child.go`; `package/internal/controller/session_manager.go` |
| Concurrent host dispatch and event attribution | `assistant/host/host.go`; `package/internal/controller/session_operations.go`; `package/internal/controller/session_events.go` |
| Acceptance versus settlement and follow-up admission | `package/internal/controller/session_actions.go`; `package/internal/controller/session_events.go` |
| Capability negotiation and deletion recovery | `package/internal/controller/pi_client.go`; `package/internal/controller/session_lifecycle.go`; `package/internal/controller/runtime.go` |
| Model/thinking configuration and resources | `package/internal/controller/session_manager.go`; `package/internal/controller/session_actions.go`; `assistant/host/native_child.go` |
| Full-host startup, child supervision, and packaging | `package/cmd/main.go`; `package/cmd/runtime.go`; `package/systemd/pixie.json`; `package/scripts/build-release.ts` |
| Host v2, authority, and migration helpers | `package/contracts/piprotocol/hostv2_envelope.go`; `package/internal/controller/pairing_authority.go`; `package/internal/persist/migration_apply.go` |
| Browser MCP registration and probe | `package/internal/controller/mcp_browser.go`; `package/contracts/src/ws-protocol.ts`; `package/webui/src/settings/sections/browser-mcp-settings.svelte`; `docker-compose.yaml`; `docs/security.md` |
| CI evidence dependency cycle | `.github/workflows/ci.yml`; `.github/workflows/release.yml`; `package/scripts/check-coverage.ts`; `package/scripts/check-package-artifacts.ts`; `package/scripts/release-gate.ts` |

## Original task status audit

`Done` means connected source behavior with focused evidence, not final-artifact or cross-platform acceptance. `Partial` means useful code/tests exist but the original outcome is not established. `Not done` means a required production path or evidence class is absent.

| Original work | State | Audit conclusion |
| --- | --- | --- |
| FIX-01 | Partial | Go `runtime.restart` is opt-in and exits status 75 with lifecycle tests; real systemd restart evidence is absent. |
| FIX-02–FIX-04, FIX-06–FIX-13 | Partial | Focused regressions exist, but several prove controller behavior rather than target Go/final artifacts. |
| FIX-05/API-02/API-03/MIG-01 | Not done end-to-end | Versioned transport, durable authority, and staged topology recovery are disconnected helpers. |
| API-01 | Partial | Browser/controller methods are bound both ways and Go advertises an exhaustive, fail-closed operation set before dispatch. The Go host catalog is hand-maintained with no non-test binding to controller call sites. |
| STATE-01/MODULE-01/ROUTE-01/LIMIT-01 | Done at source level | Workspace state, module routing, and bound helpers are integrated with focused tests. |
| LIFE-01 | Partial | Controller Stop/idle-release exists and release now verifies native quiescence before dropping residence; full real-Pi lifecycle evidence is absent. |
| GO-01/GO-02 | Partial | Go module, facade, bounded transport, and teardown exist; production contract is incomplete. |
| GO-03–GO-07 | Partial | Session targeting, cwd, settlement, and event ownership are repaired at source with regressions; history/fork and real-Pi evidence remain. |
| BRIDGE-01–BRIDGE-03/GO-08/GO-09 | Not done for Go | The Go/bridge surface exists; independent selected-Pi profiles are absent. |
| BUILD-01/BUILD-02 | Partial | Binaries build, but the assistant does not provide the required retained behavior. |
| BUILD-03 | Partial | Full-host composition now fails closed on missing Pi and joins assistant lifecycle; systemd evidence is absent. |
| BUILD-04 | Done at source/local-controller level | Docker is controller-only; evidence is local amd64 source, not a release. |
| BUILD-05 | Partial | Restart, lifecycle join, readiness degradation, and failure propagation exist at source; real systemd transitions are unverified. |
| REL-01–REL-04 | Partial | Commit identity and workflow sequencing exist; complete artifacts, collectors and authorized release evidence do not. |
| UI-01–UI-06/MEWA-01–MEWA-03 | Partial | Workspace and foundation acceptance is credible within its tested boundary, but residual UI-04 scope is open: ungrouped sessions are hardcoded empty with no host metadata over `session.list`, there is no shared visibility-aware poller (four independent 5 s loops), Settings is both a primary area and a modal, and zoom/layout checks are source/CSS substring assertions rather than rendered acceptance. |
| UI-07 | Partial | Recovery code exists; real old-client/new-server artifact evidence is absent. |
| EXT-01/EXT-04 | Not done for Go | Native UI/MCP integration is incomplete on the Go/bridge path. |
| EXT-02 | Done at source level | Registry desired/readiness separation and local failure handling have focused tests. |
| EXT-03/SEC-01 | Partial | Browser is no longer a Pixie module or runtime; the endpoint's security posture belongs to the deployment, and Pixie's registration/probe surface fails closed with focused tests. |
| SEC-02/PERF-01 | Partial | Browser MCP registration and probe are implemented at source with fail-closed controller behavior; the assistant registration/probe and the optional Compose service are host-verified, while the deployed controller UI path and four live performance profiles remain. |
| PKG-01–PKG-03 | Not done | Final archive installation and lifecycle evidence are absent. |
| DOC-01/DOC-02 | Partial | Concise docs and link checks exist; runtime accuracy must follow fixes. |
| COVERAGE-01/CUTOVER-01 | Not done | The checker explicitly refuses cutover. |
| CAN-01–CAN-05 | Not done | Bounded storage, revisions, cursors, MCP tools and UI exist, but no contained worker launcher or production `CanvasConfig` is wired, so production Canvas can never reach Ready. Gate 6 evidence is absent. |
| FIG-01–FIG-06 | Not done | Bounded preflight, transactional source lifecycle, cursors, MCP tools and UI exist, but no real parser, no design worker package, and no `openfig-core` pin. The deterministic fixture adapter now fails closed on an archive without design JSON instead of fabricating a document; actual frame rendering remains open. |

## Previous plan verification

The prior 17-file plan was re-audited against the current tree using `git show 8c3d4e7:roadmap/<file>`. The consolidated table already downgraded the largest overclaims; this pass adds the corrections below. The old per-task `execution.md`/`CHANGELOG.md` evidence ledger was removed in the consolidation, so the task-to-commit-to-evidence mapping now exists only in Git history.

- **Corrected:** API-01 is partial, not done. Only browser/controller methods are bound both ways (`package/tests/contracts/ws-catalog.test.ts`); the Go host `nativeOperationSet` is hand-maintained with no non-test binding to controller call sites. Durable pairing (API-03) is helper-only and is no longer grouped with the repaired deletion path — `PairAuthority`/`RequirePairedRecovery`/`DeletionBindingV2` have no production caller.
- **Corrected:** FC10 model/thinking mutation is unavailable on the Go host because `session.configure` is false, although the retained index marks it vanilla.
- **Restored:** FC15 public-API fidelity limits and its explicit cutover-gate status, including that the assistant bridge package named by the original plan does not exist.
- **Restored:** gate applicability — X09 belongs to Design/Gate 7 and X12–X13 to worker and browser-MCP containment, while `check-coverage` currently counts all 14 X rows into Gate 5. The Gate 6/7 definitions and numeric bounds table now live only in code.
- **Recorded:** the packed-artifact assistant build boundary (`check-assistant-build.ts`, `distribution.test.ts`, `dist/main.js`) was removed with npm retirement without a named reduction; the Go binary is the only assistant artifact.
- **Recorded:** documentation checks now cover `roadmap/README.md` and `assistant/README.md`, and the fixture design parser now rejects an archive without design JSON rather than fabricating a document.
- **Recorded:** `settings.go` now reconciles a durability-uncertain publish with the visible primary; `session_state.go` and `project-root-migration.go` still call `persist.Write` without typed reconciliation, though they re-read the primary. Shared UI poller ownership, rail/dialog unification, ungrouped session listing, and rendered zoom acceptance remain open.
- **Still uncovered:** the environment-discovery matrix from `assistant-go.md` (symlinks, spaces, custom prefixes, systemd non-login PATH) belongs in the real-Pi integration work.

## Dependency-ordered next steps

1. **Keep unsafe transitions frozen:** block Go cutover and publication; do not treat an external browser MCP endpoint as contained until the deployment proves it. The Go operation set is exhaustive and fail-closed.
2. **Prove core execution authority against real Pi:** run the new per-session ownership, cwd, settlement, abort, reconnect, and event-order tests against the pinned standalone and npm Pi distributions, including A/B/A, concurrent chat, and schedule-versus-chat. Cover executable discovery with symlinks, spaces, custom prefixes and systemd's non-login PATH.
3. **Align the shared contracts (remaining):** generate one typed model/thinking/resource schema for Go and TypeScript, project authoritative provider/model catalog snapshots, and reconcile partial create failures.
4. **D5 destructive recovery (priority):** the verified Pi 0.85.1 RPC surface has no delete command, so Go `session.delete` stays unsupported and never dispatches. Deliver the WebUI confirm/retain reconciliation view, migrate/quarantine pre-v2 records, and allow retry-same-authority only for operator-confirmed records. Never clear tombstones blindly.
5. **Prove full-host lifecycle (remaining):** amd64 fresh-archive install/start/stop/restart/uninstall is host-verified. Remaining: the same on arm64 and an upgrade/rollback rehearsal under real systemd.
6. **Port retained native integrations:** landed — the vanilla RPC slice; FC13/FC14 native UI dialogs via raw `extension_ui_request`/`extension_ui_response` frames; FC23 filesystem `pi.sources.*`/`pi.agent-mentions.list`; and the opt-in administration bridge serving FC17 providers, FC18 provider login with streaming auth events, FC19 defaults/preferences, FC20 extension inventory and FC21 extension configuration. The bridge ships as source in both archives and runs on the operator's `bun`; the full-host advertises its operations only when `PIXIE_ADMIN_BRIDGE` is enabled and the selected installation verifies, confirmed on the host against Pi 0.85.1. Remaining: FC26 `mcp.attach`/`adapter.status`/`pi.tools.list` need a live Pi extension event bus, so they stay false and reduced; and a stronger capability binding than the current name-presence check. The deleted legacy scenarios are classified as ported, bridge-backed, or named reductions rather than a standing oracle.
7. **Browser-MCP pointer:** done and host-verified end to end (live assistant host operations, the Compose Obscura MCP service, and a fresh controller image through its authenticated WebSocket). Remaining deployment hardening, isolation and egress belong to the operator.
8. **Wire transport and migration:** integrate host v2, durable pairing, stable authority, snapshots, staged conversion, topology switching, rollback, and no-ledger-rewind behavior with real services.
9. **Finish the CI evidence pipeline:** the archive/OCI collectors and the coverage/performance producer are wired, and the release workflow now adds a native arm64 performance leg that it merges before collection. Remaining: extend the evidence model to carry per-architecture binary probe facts (or run the package/release gates per architecture) so the arm64 `--version`/`doctor` rows can be un-reduced, then run an authorized publication rehearsal.
10. **Verify the publication-only registry rows:** the `releaseGate` rows (tag/release identity, registry tag and digests, provenance/SBOM, complete-set, latest) cannot exist before publication. `publish-image` already emits `pixie-image-evidence.json` and `publish` sets `completeSet`; add a post-publish job that feeds that evidence back into `release-gate` and gates `latest` promotion on it, or record the two-phase gate as an explicit operator decision, before removing those reductions.
11. **Close final gates:** run both architectures and both host compositions, performance, upgrade compatibility, OCI/SBOM/provenance, then authorize the Go cutover. The legacy source and its Bun patches are already removed by explicit operator decision.

## Investigation backlog

- I decided on one child per logical session (bounded at 16, 4 launching) rather than one globally serialized process; document capacity, memory, handoff, and event-correlation consequences under real load.
- Verify Pi's stable session identifier versus session file path across npm and standalone distributions; never pass a logical ID where `switch_session` requires a path. `assistant/host/native_real_test.go` is an opt-in harness for this.
- Identify the authoritative native completion/error signal for text, tools, commands, retries, compaction, extensions, and abort; test all event/acknowledgement orderings with a real provider.
- Generate controller UI availability from the negotiated operation set so unsupported controls cannot drift from host capability.
- Stable host identity is persisted at `<agentDir>/pixie/host-identity.json` and exposed as `runtimeId`; deletion binding v2 excludes endpoint, port and secret. Remaining: key rotation policy and migration of pre-v2 records.
- Deletion quarantine and reconciliation API now exists (`session.deletionRecovery`, `session.confirmExternalDeletion`, readiness/welcome surfacing) and retains tombstones. Remaining: WebUI view and retry-same-authority execution.
- Verify whether public Pi RPC can carry bounded text resources with the intended semantics; if not, prepare a precise upstream request or named feature-reduction decision.
- Establish how full-host config selects Pi safely on first install and how child failure reaches systemd without losing controller shutdown evidence.
- Classify each retained FC/X behavior as native RPC, supported public bridge, unavailable, or intentionally reduced; the deleted legacy Bun scenarios are no longer the oracle.
- Define an evidence-bundle schema tying command, artifact digest, platform, profile, source commit, timestamps, and result to every FC/X/gate assertion.
- Threat-model the external browser MCP deployment against same-host attacks: loopback access, DNS rebinding, unauthenticated MCP control, tool-result prompt injection, sockets, egress, and crash cleanup.
- Audit controller persistence for crash consistency across all multi-file mutations, not only the already-tested publication helpers; include disk-full, permission, rename, fsync, and stale-backup cases.
- Audit observability under failure: secret-safe logs, boot/runtime/run IDs, queue/deletion reconciliation, child stderr retention, health transitions, and bounded diagnostic exports.
- Audit long-lived operation behavior: WebSocket reconnect storms, slow clients, schedule overlap, compaction during disconnect, state growth, artifact cleanup, and clock/timezone changes.

## Product and engineering improvements after correctness

- Add a diagnostics and recovery center showing negotiated operations, native session ID/path/cwd, host boot identity, current run, Pi child health, browser MCP registration/probe status, pending uncertainty records, and actionable remediation.
- Add a disposable `doctor --scenario` mode that exercises A/B/A switching, cwd, settlement, cancellation, child restart, and resource delivery against temporary Pi state without touching user sessions.
- Make the UI capability-driven: hide or explain unsupported controls from negotiated operations, distribution, bridge, and worker status rather than allowing late failures.
- Add an explicit session-context banner for project, admitted cwd, native session identity, model/thinking state, and whether execution is active, queued, uncertain, or detached.
- Add operator-guided deletion quarantine with inspect, retry-same-authority, confirm-external-deletion, and retain-record actions; never provide a blind clear button.
- Add health-transition history and a redacted support bundle containing version/digest, config shape, negotiated capabilities, recent lifecycle events, and gate results.
- Add schedule preview/dry-run, next-run explanations, missed-run policy visibility, and per-run provenance without changing Pi's execution authority.
- Provide a maintained external browser MCP deployment example (the Compose `pixie-browser` service) with documented isolation, a no-auth warning and a probe/self-test.
- Generate protocol/config TypeScript and Go types from one schema to prevent model/thinking/resource and operation-name drift.
- Add deterministic event-order fuzzing and multi-session model checking around acceptance, settlement, cancellation, reconnect, deletion, and queue transitions.

## Deferred feature phases

- **Canvas — CAN-01–CAN-05:** the bounded storage, revisions, cursors, tools and UI are substantive. Remaining blockers: a contained `WorkerLauncher` implementation and production `CanvasConfig` wiring, exact-version raster previews, registered native authority, and both-mode/architecture hostile/recovery evidence. Applicable X04/X05/X12/X13 must pass.
- **Openfig — FIG-01–FIG-06:** the bounded preflight, transactional lifecycle, cursors, tools and UI are substantive. Remaining blockers: an independently released licensed parser/runtime behind the `Parser` interface, the pinned design worker package, focus/draft identity, hostile/offline/recovery tests, and actual upstream frame rendering. Cover extraction never substitutes for FIG-06.

## UI review

A 19-state visual and interaction review (desktop/mobile, light/dark, every primary area and settings section, overlay and recovery states) was run against the real UI in the acceptance container. Fixed and verified: Settings sections now use container queries so provider copy, System badges and model rows keep their actions at narrow pane widths; every navigation group shows a persistent selected state (settings rows use the session selected pattern, mobile panes/areas use `role=tablist`+`aria-selected`, rails style `aria-current`); mobile panes fill the viewport with user-facing labels; the session-plan popover is clamped inside the chat pane; internal settings rationale was replaced, schedule empty states de-duplicated, and the secondary Restore scoped. Residual: mobile pane `role=tab` groups lack roving tabindex/`aria-controls`, and the dark-mode attachment chip should be re-confirmed on a real image.

## Legacy removal

The operator approved a breaking removal of the legacy Bun assistant service and every artifact that existed only to serve it. The repository now contains the Go assistant (`assistant/cmd/pixie-assistant`, `assistant/host`), the controller/UI (`package/`), and the opt-in Bun administration bridge (`assistant/bridge/`). The legacy assistant TypeScript tree, its compiled output, both patch directories, the Bun parity suites, the native-SDK controller test, the root `patchedDependencies` declaration and the `check:parity` script are deleted.

This removal is an explicit operator reduction, not evidence that the corresponding FC/X rows pass. The production entrypoints already used only Go; the audit-found controller/host gap (canonical `session.steer`/`session.rename`) remains fixed. Remaining `pi.session.*`/admin names still fail closed on Go.

- **Ported or bridge-backed:** FC13/FC14 raw UI frames, FC17 providers, FC19 defaults/preferences, FC20 extension inventory, FC23 `pi.sources.*`, and the FC22 lifecycle pieces listed above.
- **Named reductions accepted with the removal:** FC15 working hints and cancellation fidelity, FC16 editor read path, `pi.tools.call`, and `session.prompt.resource`.
- **Reduced, not ported:** FC26 `mcp.attach`, `adapter.status` and `pi.tools.list` require a live Pi extension event bus the standalone sidecar cannot provide without starting a second adapter, so they stay unavailable. Everything else in FC17–FC23 is Go-native or bridge-backed.
- **Machine-readable reductions:** `package/contracts/reductions.json` records every reduced FC/X/gate with its reason and the operator approval, the performance legacy-field reductions, and the `packageArtifacts`/`releaseGate` staged-artifact reductions. Each of the two gate groups carries its own approved approver/reference/statement block. `package/scripts/produce-evidence-inputs.ts` merges the coverage/performance groups; `check-package-artifacts` and `release-gate` load the same file (default, or `--reductions`/`PIXIE_REDUCTIONS_MANIFEST`) and exempt exactly the listed rows. A reduction removes a live-matrix or publication requirement, not the retained feature or structural check, and it never records a reduced row as passing.
- **Retired test assets:** the legacy protocol, transport, serialize, and native-parity suites were removed with the tree. Their retained assertions are carried by the Go host/controller tests and the bridge suite; anything not carried is an accepted reduction above.

## Deviations from the original plan

- A local source controller and legacy assistant were deployed before final artifacts because the user explicitly authorized testing unused Pixie/Pi. This proves only the selected amd64 host path.
- The active assistant stayed on Bun rather than Go because rollout reused the existing service; subsequent audits found that switching would risk wrong-session execution and startup-blocking recovery.
- Independent npm and standalone Pi profiles were not used for the live test, so compatibility remains unproved.
- The deletion binding changed from endpoint/secret-derived v1 to stable v2 while keeping v1 records matchable. A requested record whose binding matches neither is retained and quarantined rather than aborting startup; this is an intentional safety change from the previous fail-stop behavior.
- npm retirement removed the packed assistant build boundary check and `distribution.test.ts`. The Go binary is now the only assistant artifact; no packaged-artifact boundary check replaced them.
- The deterministic design fixture parser now rejects an archive without a design JSON document instead of producing a synthetic one-page projection. A real `.fig` requires the licensed upstream parser, which is still absent.
- Documentation link/path checks now include `roadmap/README.md` and `assistant/README.md`, not only the root README and `docs/`.
- Initial host Compose exposed an unauthenticated wildcard listener and mounted `/home/core`; the rollout corrected it to authenticated loopback, removed that mount, and added restrictions.
- The in-process browser and its worker boundary were removed in favor of the pointer model; the optional Compose service is operator-deployed and not yet verified on a live host.
- Temporary rollback archives and the preceding image were removed by the later explicit cleanup request. Create a fresh snapshot before any future upgrade.
- Historical npm assistant publication was removed by explicit direction. The assistant workspace now holds only the opt-in Bun administration bridge and its typecheck.
- The legacy Bun service, its patches and its parity suites were removed by explicit operator direction without the FC/X/gate evidence the prior rule required. The affected rows are tracked as ported, bridge-backed, or named reductions in the Legacy removal section; the removal is not evidence that they pass.
- The former 17-file roadmap and later two-file summary were consolidated into this single investigation center. Detailed historical records remain in Git history.

## Retained feature index

Every row remains required unless the user approves a named reduction. V means vanilla Pi RPC, A the optional public native-administration bridge, M native MCP integration, and W a workspace module.

| ID | Retained behavior | Profile |
| --- | --- | --- |
| FC01 | Runtime identity and capabilities | V |
| FC02 | Create, reopen, resources, and trust | V |
| FC03 | Prompt, streaming, and settlement | V |
| FC04 | Images, text, and file attachments | V |
| FC05 | Queues, steering, follow-up, and Stop | V |
| FC06 | History, search, summaries, and custom content | V |
| FC07 | Rename, clone, and fork | V |
| FC08 | Archive, grouping, and ungrouped sessions | V |
| FC09 | Confirmed deletion and recovery | V |
| FC10 | Model and thinking selection | V |
| FC11 | Compaction, retry, stats, and context | V |
| FC12 | Slash commands, templates, and skills | V |
| FC13 | Blocking native UI primitives | V/A |
| FC14 | Status, widgets, titles, and notifications | V |
| FC15 | Exact working hints and UI cancellation | A |
| FC16 | Editor text and paste | V |
| FC17 | Provider catalog, readiness, and models | V/A |
| FC18 | Provider login, cancellation, logout, and status | A |
| FC19 | Global provider/model preferences | A |
| FC20 | Extension, package, and skill inventory | A |
| FC21 | Native extension enablement | A |
| FC22 | Whole-host reload | V/A |
| FC23 | Defined-agent Markdown authoring | V |
| FC24 | Native delegation and subagents | Optional |
| FC25 | Plans, goals, tasks, and questions | V/M |
| FC26 | Native MCP inventory and registration | A/M |
| FC27 | Local llama.cpp provider | Optional |
| FC28 | Signet and unrelated extensions | Optional |
| FC29 | Schedules and durable run ledger | Controller |
| FC30 | Read-only files, Git, source, diff, images, and Markdown | Controller |
| FC31 | Browser MCP registration (external endpoint; Pi is the client) | M |
| FC32 | Canvas contribution | W/M |
| FC33 | Openfig Design contribution | W/M |

## Cross-boundary acceptance index

| ID | Required assertion |
| --- | --- |
| X01 | Host, Origin, proxy, and authenticated authority are independent |
| X02 | Git inspection cannot execute configured helpers |
| X03 | Managed subprocess termination and pipe draining are bounded |
| X04 | Storage uncertainty preserves safe authority |
| X05 | Module routes cannot take over reserved routes or fall back to SPA |
| X06 | Browser, host, and native IDs never coerce or collide |
| X07 | Input, image, resource, and serialized-byte limits agree |
| X08 | Aggregate admission preserves control operations |
| X09 | Design indexes use a distinct bounded artifact path |
| X10 | Stop prevents further automatic dispatch and reports uncertainty |
| X11 | Idle release preserves state without unsafe eviction |
| X12 | Worker launchers have verified pre-exec containment and bounds |
| X13 | The external browser endpoint's isolation is explicit and Pixie fails closed when unconfigured |
| X14 | Upgrade recovery preserves drafts and avoids duplicate mutations |

Gate applicability is conditional: X09 belongs to Design and Gate 7, and X12–X13 to worker and browser-MCP containment, but `check-coverage` currently counts all 14 X rows as applicable and requires only Gates 1–5. The original Gate 6 (Canvas) and Gate 7 (Openfig/X09) definitions and the numeric bounds table existed only in the removed planning files; they must be restored here or explicitly reduced. The numeric limits themselves remain implemented in code (`internal/canvas`, `internal/design`) and tested.

## Evidence and authority rules

Record source commit, command, fixture, platform, profile, result, artifact digest, authority impact, and remaining limitation for every completed claim. Static inspection, unit tests, mocks, legacy fixtures, live Pi, final binaries, systemd, OCI, and publication are distinct evidence classes.

Publication, remote mutation, release-policy enablement, and live/native-state changes require explicit authorization. Do not treat an approved legacy removal as evidence that the applicable FC/X/gates pass against final artifacts.
