# Execution guide

Read [README.md](README.md), the assigned plan and relevant entries in [repository-review.md](repository-review.md). These directions incorporate the user's six-column UI, native Go assistant and two-build release requirements. Do not revive superseded bundled-SDK, mixed-content-tab or two-process full-host instructions from older plans.

## Working rules

When instructed to implement this roadmap, proceed with the listed code, tests, documentation and coherent local commits. Use stated defaults rather than asking for a new product decision for each task. Record bounded technical choices and evidence.

Remote pushes/tags, package/image/release publication, upstream submissions and live services retain explicit approval boundaries. Inspect workflow side effects before a push. This roadmap commit changes planning only; it is not permission to deploy all future implementation.

Do not rewrite Git history, relocate native Pi state, install optional native extensions or delete real documents for tests. Use disposable agent directories and owned/licensed fixtures. Preserve user changes and unknown native records.

Maintain one owner per runtime/delivery/state concern. Hide does not stop a session. Mewa owns foundations; files/Git remain read-only. No new provider database, agent loop, automatic worktrees, generic plugin marketplace or remotely supplied frontend code.

Optional dependency failure stays local. Missing mandatory containment fails closed. Record a precise external/API/approval blocker and continue independent tasks; never silently reduce scope or call blocked work complete. Both build variants are mandatory for release; missing one is not an optional limitation.

## Initial pass

Inspect the current checkout and concurrent changes; record the base revision. Recheck F02–F05/F07 and read actual package scripts. Run narrow baseline checks; historical green CI is not this task's result. Capture legacy transport behavior and a content-filled UI fixture before replacement.

Update root AGENTS.md early for this roadmap, actual assistant/package paths, shared Go facade, full-host composition and colocated Go unit tests. Keep user docs accurate while implementation is in progress; the roadmap is not evidence that features already ship.

## Parallel ownership

| Stream | Owns | Starts after |
| --- | --- | --- |
| A — Contracts/integration | Method inventory, schemas, capabilities, selection/module contracts and composition seam | Initial review |
| B — Go assistant | Shared assistant runtime, public facade, standalone entrypoint and process tests | A's host/composition contract |
| C — Shell/navigation | Shell, route driver, layout persistence and shared workspace store | Selection contract and primitive APIs |
| D — Mewa/views | Adapters/foundations and list/detail/content views | Visual inventory; selection contract for integration |
| E — Extensions | Registry, Browser contribution, native UI semantics and scoped MCP authority | A's module/capability contract |
| F — Reliability/security | Reproductions, failures, containment, recovery and performance | Immediately |
| G — Builds/docs | Full-host composition with B, both build targets, units, archives, installation/release tests and docs | Shared CLI/facade; editorial corrections may begin immediately |
| H — Canvas | Canvas service, isolated render, tools and contribution | Core Gate 5 and E's scoped authority |
| I — Openfig | Design storage/worker/query/UI and renderer gate | Core Gate 5 and Canvas foundation accepted |

A owns shared schemas; C owns shared workspace state. B owns child I/O/lifecycle; E owns native UI semantics, with exactly one final response translation. G owns application composition with B through the facade, not a second assistant implementation.

Other streams integrate through fixtures/props rather than concurrent broad edits of shared files. H/I reuse E's registry and F's tested worker boundary. Early research is allowed, but Canvas then Openfig remain the last feature integrations.

## Task ledger

All tasks begin unimplemented. Check only after recording evidence below. Split tasks as needed without changing ownership or silently reducing scope.

### Foundation and regressions

- [ ] FIX-01 — F/B: production restart exits through bounded shutdown; real executable/service test, not only callback injection.
- [ ] FIX-02 — F/E: failed module persistence changes neither effective state nor active Browser identity.
- [ ] FIX-03 — F/E: unchanged enable is a no-op; explicit Restart is separate.
- [ ] FIX-04 — C/F: one pending create per action; reproduce/isolate selected-chat/project-empty; stale responses cannot win.
- [ ] FIX-05 — A/F: strict IDs/envelopes and deliberate duplicate-in-flight error behavior in conformance fixtures.
- [ ] API-01 — A: exact browser/controller/assistant/native method map, capability versions and retained-feature dispositions.
- [ ] API-02 — A/B: shared schema/bindings, epoch/checkpoint and acceptance/settlement contracts.
- [ ] STATE-01 — A/C: primary/secondary selections, optional projects, context resolution, URL/persistence migration and reducer invariants.
- [ ] MODULE-01 — A/E: contribution descriptors, sidebar-only/viewer-only fixtures and instance/project/session authority.
- [ ] DOC-01 — G/A: root instructions recognize accepted architecture/paths/tests without claiming implementation is complete.

### Shared Go assistant

- [ ] GO-01 — B: separate module, CLI/config, executable discovery, read-only doctor and independent Pi installation fixtures.
- [ ] GO-02 — B: bounded JSONL framing, writer/correlation, cancellation, child exit and slow-consumer behavior.
- [ ] GO-03 — B: vanilla create/prompt/model/thinking/abort/reopen slice, images and native resource/trust resolution.
- [ ] GO-04 — B/F: clone/fork ownership, empty drafts, read-only catalog, unknown records, large histories and external replacement.
- [ ] GO-05 — B/A/F: acceptance/settlement, retries/compaction/continuation, durable outbox ownership, uncertainty and Stop.
- [ ] GO-06 — B/E: scoped dialogs/widgets/status/title/editor requests, draft conflicts and generation-safe replay.
- [ ] GO-07 — B/F: on-demand residence, detached-work disposition, managed descendant cleanup and idle TUI handoff.
- [ ] GO-08 — B/A: retained administration/optional features have tested dispositions, without silent loss or mandatory bundle.
- [ ] GO-09 — B/G: staged metadata migration, old/new protocol coverage and rollback before removing legacy packaging.

### Required build variants

- [ ] BUILD-01 — A/B/G: public assistant/host facade and explicit composition lifecycle; standalone/full binary share it; no forbidden internal imports or duplicate supervisor.
- [ ] BUILD-02 — B/G: assistant-only build independently compiles/runs without UI, controller, Chromium or Design dependencies.
- [ ] BUILD-03 — G/B: full-host single binary embeds assistant/controller/UI; no separate assistant executable, Docker or external asset directory for core functionality.
- [ ] BUILD-04 — G/A: explicit Docker controller-only mode never launches Pi; private full-host transport, matching identity and ownership conflict detection.
- [ ] BUILD-05 — G/F: whole-composition restart/shutdown, two user-unit alternatives, preserved queues/state and Docker/full-host mode switching.

### Workspace and Mewa

- [ ] UI-01 — C: six slots/selection reducers against fake transport; no generic mixed-content-tab ownership.
- [ ] UI-02 — C/D: real Chat + File split, independent collapse, focus/restore and draft/stream continuity.
- [ ] UI-03 — D/C: grouped/flat/ungrouped sessions, recent expansion, explicit Archive, guarded create and native titles.
- [ ] UI-04 — D/C: Schedules list/detail/run navigation and Settings primary areas reuse existing backend behavior.
- [ ] UI-05 — D/C: session details, multi-repository Git, file/diff views and context-safe secondary selection.
- [ ] UI-06 — C/F: back/forward, persistence migration, stale responses/missing routes, scoped availability, mobile and keyboard resizers.
- [ ] MEWA-01 — D: pinned component/token/adapter inventory; retain correct wrappers and integrity checks.
- [ ] MEWA-02 — D: one foundation owner, temporary mapping retired after migration, verified cascade and lifecycle.
- [ ] MEWA-03 — D/F: five content-filled modes, light/dark/zoom/accessibility, long content, independent scrolling and no page overflow.

### Extensions, security and release preparation

- [ ] EXT-01 — E/B: complete native UI mapping/replay; explicit terminal-only limitations.
- [ ] EXT-02 — E: one generalized registry; preserve all module states and unrelated runtime on toggle/failure/restart.
- [ ] EXT-03 — E/C: Browser contribution in slots 4/5/6 with leases/artifacts/cleanup and separate Pi-tool availability.
- [ ] EXT-04 — E/A/F: session-scoped MCP credentials/context; forged IDs and transport session IDs confer no authority.
- [ ] SEC-01 — F/G: actual Browser threat model/flags and auth/origin/path/size tests in Docker and full-host modes; tested optional worker enclosure.
- [ ] PERF-01 — F: repeatable amd64/arm64 measurements of both variants including Pi children, Browser and large-history UI.
- [ ] PKG-01 — G/B: both binaries, matching units/private configs, doctor/version/readiness and install/stop/restart/remove smoke tests.
- [ ] PKG-02 — G: exact-source two-variant × two-architecture release matrix, checksums/provenance/manifest, complete-set promotion and validation/publication separation.
- [ ] PKG-03 — G/F: real Docker controller + assistant archive and full-host archive on both architectures; upgrade/rollback/mode switch and dependency diagnostics.
- [ ] DOC-02 — G: current user docs/examples for both builds; old roadmap/drafts become links; Markdown/source links checked.
- [ ] CUTOVER-01 — A: all core gates pass; remove only superseded paths whose replacements preserve retained scenarios.

### Penultimate feature: Canvas

- [ ] CAN-01 — H/E/F: session authority, quotas and offline worker containment before untrusted HTML execution.
- [ ] CAN-02 — H: immutable revision store, idempotent create/write retry, single-flight jobs, tombstones and restart.
- [ ] CAN-03 — H: six tools/guide, bounded text/DOM and actual images with exact version/render identities.
- [ ] CAN-04 — H/C/D: registered sidebar/view/cards and version-aware live preview without untrusted controller-origin script.
- [ ] CAN-05 — H/F/G: cross-session/egress/remove/disable races, cache/artifact limits and dependencies in both deployments/architectures.

### Last feature: Openfig

- [ ] FIG-01 — I/F: reproduce pinned public parser with licensed real .fig files; choose/test isolated worker artifact.
- [ ] FIG-02 — I: bounded preflight/index, transactional single-document slot, explicit instance scope and durable restart/removal.
- [ ] FIG-03 — I/E: one read-only query service/five tools, actual bounded images and stale-document/selection rejection.
- [ ] FIG-04 — I/C/D: pages/layers/frames, labelled cover, private versus explicit shared focus, reference-in-chat and removal while disabled.
- [ ] FIG-05 — I/F/G: structure MVP/failure/offline/authority matrix and both deployment variants on supported architectures.
- [ ] FIG-06 — I: verify released public renderer and actual frame previews, or record exact upstream blocker without marking previews complete.

## Integration gates

**Gate 1 — Contracts/regressions.** Actual dispatchers, schemas, selections, module scope and two-build facade are agreed. Tests cover confirmed defects; native feature gaps have dispositions. Owners work independently against fixtures.

**Gate 2 — Vertical slices.** Shared Go host uses independent vanilla Pi. New shell displays chat and file together. Full-host composition uses that same assistant facade; no optional module/native package is required.

**Gate 3 — Continuity.** Images, clone/fork, retry/compaction, queues, reconnect, dialogs, switching/cancellation and all layout modes pass. No duplicate accepted prompt or cross-generation/context response.

**Gate 4 — Optional integrations.** Browser uses registry; native MCP absence is non-fatal; supported optional integrations remain optional; failures do not replace shell or stop chat.

**Gate 5 — Core cutover/two builds.** Four final archives install on amd64/arm64. Docker controller uses standalone host; full-host needs one binary/service. Stop/restart, ownership conflict, metadata migration, rollback and mode switch pass; docs agree. Only now remove superseded implementations. Exact-source publication approval is still separate.

**Gate 6 — Canvas.** CAN tasks pass including session authority and actual egress isolation. No fallback to weaker execution just to get a screenshot.

**Gate 7 — Openfig.** Structure is verified independently from frame rendering. Real previews need separate evidence or a visible blocker. Cover support does not satisfy FIG-06.

## Previous roadmap items

| Existing item | Disposition |
| --- | --- |
| Standalone optional subagents | GO-08 plus both artifact modes; preserve opt-in legacy support until retired, not a mandatory npm release before Go work |
| arm64 measurements | PERF-01/PKG-03; old unverified measurements are not results |
| Bun child-launch patch proposal | Reassess against selected native runtime; retain only while tested support needs it; upstream submission requires approval |
| SDK built-in export patch | Legacy compatibility only; selected Pi CLI preferred for Go; remove after tested legacy retirement |
| Right-rail module tabs | UI-05/EXT-02/EXT-03; same contribution contract for Canvas/Design |
| Non-disruptive per-session reload | Deferred; no automatic installation/new reload machinery without demonstrated need |
| Host relocation/history rewrite already completed | Not tasks; do not repeat |

## Evidence

Append concise completion records as tasks land:

```text
Task IDs:
Commit(s):
Behavior and contract changes:
Tests executed and result:
UI/process/artifact evidence, including deployment variant where relevant:
State migration and rollback impact:
Remaining limitation or exact blocker:
```

Static review, mocks, native Pi tests and artifact/systemd checks are different evidence. Do not delete failing regressions because the rewrite fails them. Explain intentional approved changes and test their replacement. Final report names both build variants and distinguishes Openfig inspection from real frame rendering.
