# Pixie roadmap and investigation center

This is the canonical implementation status, investigation backlog, and forward plan for Pixie. Operating behavior belongs in `docs/`. A source helper, mock, compiler pass, or legacy fixture is not final Go-assistant, cross-platform, deployment, or release evidence.

## Current verdict

**No-go for Go-assistant cutover, release, or untrusted Browser work.** A third repair pass landed the P0 execution-integrity fixes at source and unit-test level: one native child per logical session with a durable path/cwd registry, immutable event/run ownership, separate acceptance and settlement states, exhaustive fail-closed operation negotiation, non-blocking deletion recovery bound to a stable persisted host identity, and a full-host lifecycle that fails closed. The remaining blockers are legacy-only native parity, the uncontained Browser boundary, the unwired host-v2/pairing/migration path, and missing live release evidence. No P0 repair has been exercised against a real pinned Pi distribution in this checkout.

The authorized local amd64 deployment uses controller image `pixie-local:8c3d4e75` from checkout `8c3d4e75a268b61bf02854cb6e4d2f59b37e0b40`. The controller is healthy, loopback-bound, authenticated, non-root, read-only, capability-dropped, and resource-limited. The host assistant still runs the legacy Bun source service; the Go repairs are newer than that image and have not been deployed. This is a tested local deployment, not a final artifact or Go cutover.

Current fail-closed gates agree with that verdict:

- `check-coverage` has no mapped live evidence for all 28 mandatory FC rows, all 14 X rows, or Gates 1–5. The repaired behaviors have focused Go regression tests, not live coverage rows.
- `check-package-artifacts` lacks four archives, four executables, lifecycle checks, and a working full-host evidence record.
- `check-performance` has 0 of 4 required process targets.
- `release-gate` lacks release identity, archives, binaries, OCI platform digests, checksums, SBOM/provenance, source reachability, and publication authorization. CI now produces archives and the OCI tar before the evidence gates, but the gate collectors still cannot ingest them and no evidence bundle producer exists.
- Repository lint is not green: the latest run reported 119 errors, 182 warnings, and 16 informational diagnostics, mainly in retained legacy TypeScript. The new Go and contract edits are formatted and type-clean.
- A `bun test tests` run under Bun 1.3.14 reported 805 passes and 2 failures, both in retained legacy Pi parity (`tests/pi-native-parity/native-child-resources.test.ts`, `tests/pi-native-parity/subagent-child.test.ts`) and caused by a `webidl.util.markAsUncloneable` incompatibility in the pinned `@earendil-works/pi-coding-agent` bundle. The repository pins Bun 1.4.0; re-run under the pinned runtime before assigning them to code, and meanwhile do not count the suite as green.

## Confirmed implemented baseline

- The Go repository builds separate `pixie-assistant`, full-host `pixie`, and controller-only `pixie` compositions. Docker correctly runs controller-only mode and never starts Pi.
- The Go host owns one immutable child per logical session, launched in that session's admitted cwd, with a durable `native-sessions.json` mapping logical ID to absolute native path and cwd, exact identity checks before every load/prompt/cancel/release, a per-session installation lock, and per-event session ownership instead of a mutable current-session field.
- The Go host blocks `session.prompt` until the native run settles and returns the terminal stop reason; prompt acceptance, uncertain dispatch, and proven rejection use distinct typed errors (`-32003`/`-32004`). The controller clears a reattached run on the authoritative `agent_settled` signal and only rolls back a proven structured rejection.
- Hello advertises an exhaustive `operationSet`. A missing or false operation is rejected before dispatch; unsupported delete cannot journal a durable intent. Unsupported create-time model/thinking/MCP overrides and text-resource prompts are rejected before any native mutation.
- The Go host persists a stable host identity under the agent directory and exposes it as `runtimeId`. Deletion binding v2 excludes ephemeral endpoint, port and transport secret, with legacy endpoint-bound records still matchable; unmatched or unsupported records are retained and surfaced instead of aborting startup.
- Full-host startup fails without an absolute Pi agent directory and a resolvable Pi executable; the composition joins `assistant.Errors()` with the controller; assistant readiness degrades while a lost session awaits reload; `runtime.restart` is explicit opt-in and exits status 75; generated install instructions include the private environment file and Pi selection.
- Bounded native JSONL parsing, aggregate admission, control reserve, pending limits, timeouts, and process-group teardown have focused tests.
- Controller Host/Origin policy, loopback Pi restrictions, reserved module routes, non-executing Git inspection, bounded controller child termination, typed persistence outcomes, and storage bounds have focused implementation tests. Settings now reconciles a durability-uncertain config publish against the visible primary instead of leaving the cache stale. `session_state.go`, `browser_panel_ownership.go` and `project-root-migration.go` still call `persist.Write`, but they re-read the primary and do not cache a divergent value.
- The six-slot workspace, responsive Settings behavior, rail keyboard navigation, read-only Files/Git views, Mewa foundations, Browser leases/artifacts, and packaged Chromium shell have source or fixture acceptance.
- The local controller and legacy assistant are live and healthy. Both Go modules pass `CGO_ENABLED=0 go test -count=1 ./...` and `CGO_ENABLED=0 go vet ./...`; this verifies isolated Go code, not full integration.
- npm assistant publication has been removed. The private Bun workspace remains only as a fallback and parity oracle until Go cutover.

## Confirmed defects and integration risks

### P0 — execution integrity (repaired at source; real-Pi integration unproven)

1. **Wrong session and cwd — GO-03/GO-04/GO-05/GO-07, FC02–FC07, X06/X10/X11. Repaired at source.** The host now keeps one immutable child per logical session in the admitted cwd, records a durable logical-ID-to-path/cwd mapping, verifies exact native identity before load/prompt/cancel/release, and attributes each event to the bound session. Focused tests cover A/B concurrency, same-cwd isolation, registry restart, release readmission, and event attribution. Still missing: execution against a real pinned Pi distribution, A/B/A with live providers, and schedule-versus-chat interleaving.

2. **Acceptance is mistaken for settlement — GO-05, FC03/FC05/FC11, X10. Repaired at source.** The host separates accepted from settled, waits for the pre-dispatch barrier plus `agent_settled` (or an authoritative post-acceptance state probe) before returning, and classifies rejected versus uncertain prompts. The controller clears a reattached run on `agent_settled` and admits follow-ups only afterwards. Focused tests cover late settlement, timeout poisoning, reconnect settlement, and proven-rejection rollback. Still missing: real-provider retry/compaction event-order testing.

3. **False capabilities and startup-blocking deletion recovery — API-01/FIX-01/BUILD-05, FC07–FC09/FC22. Repaired at source; reconciliation UX incomplete.** Hello advertises an exhaustive `operationSet`; missing/false operations fail closed before mutation, and unsupported delete cannot write a durable intent. The host persists a stable identity exposed as `runtimeId`; deletion binding v2 excludes endpoint/port/secret and still matches legacy records. Recovery quarantines unmatched or unsupported records, keeps their tombstones, surfaces them in readiness/welcome, and exposes `session.deletionRecovery`/`session.confirmExternalDeletion`. Still missing: a WebUI reconciliation view, retry-same-authority execution (Go delete remains unsupported), migration/quarantine of pre-v2 unreadable records, and a non-test binding between the host's hand-maintained `nativeOperationSet` and the controller RPC call sites (the exhaustive binding covers browser/controller only). Durable pairing authority (API-03) belongs to defect 7 and remains helper-only.

### P1 — retained functionality and lifecycle

4. **Model, thinking, and resource contracts disagree — FC04/FC10/FC17–FC19. Partially repaired.** Go now rejects create-time model/thinking/MCP overrides and text-resource prompts before any native mutation, and its snapshot returns real provider/model/thinking config options. The controller already gates resources on `session.prompt.resource`. Still missing: the Go host does not enable `session.configure`, so FC10 model/thinking selection is unavailable there despite the retained-feature index marking it vanilla; one generated typed schema shared by Go and TypeScript; authoritative snapshot projection for provider/model catalogs; and transactional or reconciled create when a settings step fails.

5. **Full-host packaging and supervision — BUILD-03/BUILD-05/PKG-01–PKG-03, FC22. Substantially repaired at source.** Full-host startup now requires an absolute agent directory and a resolvable Pi executable, `serveFullHost` joins `assistant.Errors()` and the controller error channel, assistant readiness degrades while a lost session awaits reload, `runtime.restart` is explicit opt-in and exits status 75, packaged `pixie.json` selects Pi, and generated install instructions include the private environment file and selection. Still missing: fresh-archive systemd install/start/stop/restart/upgrade/rollback/uninstall evidence on both architectures.

6. **Native administration and parity remain legacy-only — BRIDGE-01–BRIDGE-03/GO-06/GO-08/GO-09/EXT-01/EXT-04, FC13–FC28.** Provider/auth/settings/extensions/MCP/native-UI tests primarily execute `assistant/src` under Bun. They do not prove the Go binary or independent npm/standalone Pi profiles. Required fix: integrate only supported selected-Pi public APIs, preserve explicit unsupported states, and run each operation through the Go binary against both distributions. FC15 stays a cutover gate: the retained public API cannot provide request-specific dialog cancellation or non-empty editor text, so exact working hints and UI cancellation are known-reduced and must not be presented as parity. The assistant bridge package named by the original plan does not exist.

### P1 — architecture and delivery

7. **Host v2, durable pairing, and staged migration are helper-only — FIX-05/API-02/API-03/MIG-01, X04/X06/X14.** Production host/controller negotiate v1. Host-v2 envelopes, durable authority, staged migration, topology switch, and rollback exist as test/helper code without production callers. Required fix: select and wire one protocol/identity model through startup, transport, recovery, and real topology transitions before claiming migration.

8. **Browser is enabled without containment — SEC-02/EXT-03, FC31, X12/X13.** Chromium uses `--no-sandbox` and shares controller UID, writable state, and host network. Compose capability/resource restrictions are defense in depth, not same-UID or network isolation. Required fix: make untrusted Browser unavailable unless a verified external worker boundary provides separate process authority, filesystem mediation, network policy, pre-exec limits, and cleanup.

9. **CI evidence order — REL-01–REL-04/COVERAGE-01/PKG-01–PKG-03/PERF-01. Sequenced; producers missing.** Static validation no longer runs the live gates. `stage` and `image` produce exact-commit archives and the OCI tar first; a new `evidence` job downloads both and runs coverage/package/performance/release gates; `publish-image` and `publish` depend on that evidence job. Artifact names now use the identity job's source commit instead of `github.sha`. Still missing: gate collectors that ingest the archives/OCI layout, a coverage/performance evidence-bundle producer, and a `latest`/reachability collector, so the evidence job still fails closed on genuinely absent live inputs.

10. **Operating documentation previously outran implementation — DOC-01/DOC-02.** Full-host topology, prompt settlement, stable identity, and migration descriptions were corrected to the observed behavior. Documentation checks validate links and commands, not truth of runtime claims; the remaining host-v2, Browser-worker and parity sections stay explicitly incomplete.

### Evidence anchors for continued investigation

| Area | Primary source anchors |
| --- | --- |
| Session targeting, cwd, supported methods, prompt conversion | `assistant/host/native_child.go`; `package/internal/controller/session_manager.go` |
| Concurrent host dispatch and event attribution | `assistant/host/host.go`; `package/internal/controller/session_operations.go`; `package/internal/controller/session_events.go` |
| Acceptance versus settlement and follow-up admission | `package/internal/controller/session_actions.go`; `package/internal/controller/session_events.go` |
| Capability negotiation and deletion recovery | `package/internal/controller/pi_client.go`; `package/internal/controller/session_lifecycle.go`; `package/internal/controller/runtime.go` |
| Model/thinking configuration and resources | `package/internal/controller/session_manager.go`; `package/internal/controller/session_actions.go`; `assistant/host/native_child.go` |
| Full-host startup, child supervision, and packaging | `package/cmd/main.go`; `package/cmd/runtime.go`; `package/systemd/pixie.json`; `package/scripts/build-release.ts` |
| Host v2, authority, and migration helpers | `package/internal/piprotocol/hostv2_envelope.go`; `package/internal/controller/pairing_authority.go`; `package/internal/persist/migration_apply.go` |
| Browser defaults and containment | `package/internal/mcpserver/modules.go`; `package/internal/browser/config.json`; `package/internal/browser/service.go`; `docker-compose.yaml`; `docs/security.md` |
| CI evidence dependency cycle | `.github/workflows/ci.yml`; `.github/workflows/release.yml`; `package/scripts/check-coverage.ts`; `package/scripts/check-package-artifacts.ts`; `package/scripts/release-gate.ts` |

## Original task status audit

`Done` means connected source behavior with focused evidence, not final-artifact or cross-platform acceptance. `Partial` means useful code/tests exist but the original outcome is not established. `Not done` means a required production path or evidence class is absent.

| Original work | State | Audit conclusion |
| --- | --- | --- |
| FIX-01 | Partial | Go `runtime.restart` is opt-in and exits status 75 with lifecycle tests; real systemd restart evidence is absent. |
| FIX-02–FIX-04, FIX-06–FIX-13 | Partial | Focused regressions exist, but several prove controller or legacy behavior rather than target Go/final artifacts. |
| FIX-05/API-02/API-03/MIG-01 | Not done end-to-end | Versioned transport, durable authority, and staged topology recovery are disconnected helpers. |
| API-01 | Partial | Browser/controller methods are bound both ways and Go advertises an exhaustive, fail-closed operation set before dispatch. The Go host catalog is hand-maintained with no non-test binding to controller call sites. |
| STATE-01/MODULE-01/ROUTE-01/LIMIT-01 | Done at source level | Workspace state, module routing, and bound helpers are integrated with focused tests. |
| LIFE-01 | Partial | Controller Stop/idle-release exists and release now verifies native quiescence before dropping residence; full real-Pi lifecycle evidence is absent. |
| GO-01/GO-02 | Partial | Go module, facade, bounded transport, and teardown exist; production contract is incomplete. |
| GO-03–GO-07 | Partial | Session targeting, cwd, settlement, and event ownership are repaired at source with regressions; history/fork and real-Pi evidence remain. |
| BRIDGE-01–BRIDGE-03/GO-08/GO-09 | Not done for Go | Evidence remains legacy-only; independent selected-Pi profiles are absent. |
| BUILD-01/BUILD-02 | Partial | Binaries build, but the assistant does not provide the required retained behavior. |
| BUILD-03 | Partial | Full-host composition now fails closed on missing Pi and joins assistant lifecycle; systemd evidence is absent. |
| BUILD-04 | Done at source/local-controller level | Docker is controller-only; evidence is local amd64 source, not a release. |
| BUILD-05 | Partial | Restart, lifecycle join, readiness degradation, and failure propagation exist at source; real systemd transitions are unverified. |
| REL-01–REL-04 | Partial | Commit identity and workflow sequencing exist; complete artifacts, collectors and authorized release evidence do not. |
| UI-01–UI-06/MEWA-01–MEWA-03 | Partial | Workspace and foundation acceptance is credible within its tested boundary, but residual UI-04 scope is open: ungrouped sessions are hardcoded empty with no host metadata over `session.list`, there is no shared visibility-aware poller (four independent 5 s loops), Settings is both a primary area and a modal, and zoom/layout checks are source/CSS substring assertions rather than rendered acceptance. |
| UI-07 | Partial | Recovery code exists; real old-client/new-server artifact evidence is absent. |
| EXT-01/EXT-04 | Not done for Go | Native UI/MCP integration remains legacy-only. |
| EXT-02 | Done at source level | Registry desired/readiness separation and local failure handling have focused tests. |
| EXT-03/SEC-01 | Partial | Browser and security controls exist without required containment/default posture. |
| SEC-02/PERF-01 | Not done | Worker isolation and four live performance profiles are absent. |
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
- **Restored:** gate applicability — X09 belongs to Design/Gate 7 and X12–X13 to Browser, while `check-coverage` currently counts all 14 X rows into Gate 5. The Gate 6/7 definitions and numeric bounds table now live only in code.
- **Recorded:** the packed-artifact assistant build boundary (`check-assistant-build.ts`, `distribution.test.ts`, `dist/main.js`) was removed with npm retirement without a named reduction; the Go binary is the only assistant artifact.
- **Recorded:** documentation checks now cover `roadmap/README.md` and `assistant/README.md`, and the fixture design parser now rejects an archive without design JSON rather than fabricating a document.
- **Recorded:** `settings.go` now reconciles a durability-uncertain publish with the visible primary; `session_state.go`, `browser_panel_ownership.go` and `project-root-migration.go` still call `persist.Write` without typed reconciliation, though they re-read the primary. Shared UI poller ownership, rail/dialog unification, ungrouped session listing, and rendered zoom acceptance remain open.
- **Still uncovered:** the environment-discovery matrix from `assistant-go.md` (symlinks, spaces, custom prefixes, systemd non-login PATH) belongs in the real-Pi integration work.

## Dependency-ordered next steps

1. **Keep unsafe transitions frozen:** retain the legacy assistant; block Go cutover and publication; do not enable untrusted Browser work. The Go operation set is exhaustive and fail-closed.
2. **Prove core execution authority against real Pi:** run the new per-session ownership, cwd, settlement, abort, reconnect, and event-order tests against the pinned standalone and npm Pi distributions, including A/B/A, concurrent chat, and schedule-versus-chat. Cover executable discovery with symlinks, spaces, custom prefixes and systemd's non-login PATH.
3. **Align the shared contracts (remaining):** generate one typed model/thinking/resource schema for Go and TypeScript, project authoritative provider/model catalog snapshots, and reconcile partial create failures.
4. **Finish destructive recovery (remaining):** add a WebUI reconciliation view for retained tombstones, implement retry-same-authority once Go delete exists, and migrate/quarantine pre-v2 records.
5. **Prove full-host lifecycle (remaining):** verify fresh archive install/start/stop/restart/upgrade/rollback/uninstall under real systemd on both architectures.
6. **Port retained native integrations:** implement the public bridge and each FC13–FC28 operation through Go against independent npm and standalone Pi distributions. Keep the Bun oracle until exact parity or explicit named reductions are approved.
7. **Enforce Browser separation:** provide a separate worker UID/filesystem/network design and deployment profile; default Browser unavailable when enforcement cannot be verified.
8. **Wire transport and migration:** integrate host v2, durable pairing, stable authority, snapshots, staged conversion, topology switching, rollback, and no-ledger-rewind behavior with real services.
9. **Finish the CI evidence pipeline:** add artifact/OCI collectors and a coverage/performance evidence-bundle producer so the new post-artifact gates can pass on real candidate inputs, then run authorized publication rehearsals.
10. **Close final gates:** run both architectures and both host compositions, performance, upgrade compatibility, OCI/SBOM/provenance, and only then retire legacy code and its optional Bun patches.

## Investigation backlog

- I decided on one child per logical session (bounded at 16, 4 launching) rather than one globally serialized process; document capacity, memory, handoff, and event-correlation consequences under real load.
- Verify Pi's stable session identifier versus session file path across npm and standalone distributions; never pass a logical ID where `switch_session` requires a path. `assistant/host/native_real_test.go` is an opt-in harness for this.
- Identify the authoritative native completion/error signal for text, tools, commands, retries, compaction, extensions, and abort; test all event/acknowledgement orderings with a real provider.
- Generate controller UI availability from the negotiated operation set so unsupported controls cannot drift from host capability.
- Stable host identity is persisted at `<agentDir>/pixie/host-identity.json` and exposed as `runtimeId`; deletion binding v2 excludes endpoint, port and secret. Remaining: key rotation policy and migration of pre-v2 records.
- Deletion quarantine and reconciliation API now exists (`session.deletionRecovery`, `session.confirmExternalDeletion`, readiness/welcome surfacing) and retains tombstones. Remaining: WebUI view and retry-same-authority execution.
- Verify whether public Pi RPC can carry bounded text resources with the intended semantics; if not, prepare a precise upstream request or named feature-reduction decision.
- Establish how full-host config selects Pi safely on first install and how child failure reaches systemd without losing controller shutdown evidence.
- Re-run all legacy parity scenarios through Go and classify each as native RPC, supported public bridge, unavailable, or intentionally reduced.
- Define an evidence-bundle schema tying command, artifact digest, platform, profile, source commit, timestamps, and result to every FC/X/gate assertion.
- Threat-model the external Browser worker against same-host attacks: loopback access, DNS rebinding, sibling sessions, artifact paths, process signaling, sockets, egress, and crash cleanup.
- Audit controller persistence for crash consistency across all multi-file mutations, not only the already-tested publication helpers; include disk-full, permission, rename, fsync, and stale-backup cases.
- Audit observability under failure: secret-safe logs, boot/runtime/run IDs, queue/deletion reconciliation, child stderr retention, health transitions, and bounded diagnostic exports.
- Audit long-lived operation behavior: WebSocket reconnect storms, slow clients, schedule overlap, compaction during disconnect, state growth, artifact cleanup, and clock/timezone changes.

## Product and engineering improvements after correctness

- Add a diagnostics and recovery center showing negotiated operations, native session ID/path/cwd, host boot identity, current run, Pi child health, Browser containment status, pending uncertainty records, and actionable remediation.
- Add a disposable `doctor --scenario` mode that exercises A/B/A switching, cwd, settlement, cancellation, child restart, and resource delivery against temporary Pi state without touching user sessions.
- Make the UI capability-driven: hide or explain unsupported controls from negotiated operations, distribution, bridge, and worker status rather than allowing late failures.
- Add an explicit session-context banner for project, admitted cwd, native session identity, model/thinking state, and whether execution is active, queued, uncertain, or detached.
- Add operator-guided deletion quarantine with inspect, retry-same-authority, confirm-external-deletion, and retain-record actions; never provide a blind clear button.
- Add health-transition history and a redacted support bundle containing version/digest, config shape, negotiated capabilities, recent lifecycle events, and gate results.
- Add schedule preview/dry-run, next-run explanations, missed-run policy visibility, and per-run provenance without changing Pi's execution authority.
- Provide a maintained external Browser worker deployment template with verifiable isolation status and a built-in hostile-page self-test.
- Generate protocol/config TypeScript and Go types from one schema to prevent model/thinking/resource and operation-name drift.
- Add deterministic event-order fuzzing and multi-session model checking around acceptance, settlement, cancellation, reconnect, deletion, and queue transitions.

## Deferred feature phases

- **Canvas — CAN-01–CAN-05:** the bounded storage, revisions, cursors, tools and UI are substantive. Remaining blockers: a contained `WorkerLauncher` implementation and production `CanvasConfig` wiring, exact-version raster previews, registered native authority, and both-mode/architecture hostile/recovery evidence. Applicable X04/X05/X12/X13 must pass.
- **Openfig — FIG-01–FIG-06:** the bounded preflight, transactional lifecycle, cursors, tools and UI are substantive. Remaining blockers: an independently released licensed parser/runtime behind the `Parser` interface, the pinned design worker package, focus/draft identity, hostile/offline/recovery tests, and actual upstream frame rendering. Cover extraction never substitutes for FIG-06.

## Deviations from the original plan

- A local source controller and legacy assistant were deployed before final artifacts because the user explicitly authorized testing unused Pixie/Pi. This proves only the selected amd64 host path.
- The active assistant stayed on Bun rather than Go because rollout reused the existing service; subsequent audits found that switching would risk wrong-session execution and startup-blocking recovery.
- Independent npm and standalone Pi profiles were not used for the live test, so compatibility remains unproved.
- The deletion binding changed from endpoint/secret-derived v1 to stable v2 while keeping v1 records matchable. A requested record whose binding matches neither is retained and quarantined rather than aborting startup; this is an intentional safety change from the previous fail-stop behavior.
- npm retirement removed the packed assistant build boundary check and `distribution.test.ts`. The Go binary is now the only assistant artifact; no packaged-artifact boundary check replaced them.
- The deterministic design fixture parser now rejects an archive without a design JSON document instead of producing a synthetic one-page projection. A real `.fig` requires the licensed upstream parser, which is still absent.
- Documentation link/path checks now include `roadmap/README.md` and `assistant/README.md`, not only the root README and `docs/`.
- Initial host Compose exposed an unauthenticated wildcard listener and mounted `/home/core`; the rollout corrected it to authenticated loopback, removed that mount, and added restrictions.
- Browser was smoke-tested for UI liveness without claiming containment; its architecture still deviates from the enforced-worker requirement.
- Temporary rollback archives and the preceding image were removed by the later explicit cleanup request. Create a fresh snapshot before any future upgrade.
- Historical npm assistant publication was removed by explicit direction. The private Bun workspace remains only until Go correctness/parity permits legacy retirement.
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
| FC31 | Browser contribution | W |
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
| X12 | Workers have verified pre-exec containment and bounds |
| X13 | Browser non-isolation and worker fail-closed behavior are explicit |
| X14 | Upgrade recovery preserves drafts and avoids duplicate mutations |

Gate applicability is conditional: X09 belongs to Design and Gate 7, and X12–X13 to Browser/worker containment, but `check-coverage` currently counts all 14 X rows as applicable and requires only Gates 1–5. The original Gate 6 (Canvas) and Gate 7 (Openfig/X09) definitions and the numeric bounds table existed only in the removed planning files; they must be restored here or explicitly reduced. The numeric limits themselves remain implemented in code (`internal/canvas`, `internal/design`) and tested.

## Evidence and authority rules

Record source commit, command, fixture, platform, profile, result, artifact digest, authority impact, and remaining limitation for every completed claim. Static inspection, unit tests, mocks, legacy fixtures, live Pi, final binaries, systemd, OCI, and publication are distinct evidence classes.

Publication, remote mutation, release-policy enablement, and live/native-state changes require explicit authorization. Do not retire legacy behavior until applicable FC/X/gates pass against final artifacts or the user approves the precise reduction.
