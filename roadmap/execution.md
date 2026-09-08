# Execution guide and task ledger

Read [README.md](README.md), [contracts.md](contracts.md), [compatibility.md](compatibility.md), the assigned plan and [acceptance.md](acceptance.md). The unnumbered files are canonical. Do not restore numbered duplicates or deleted planning stubs under docs/.

## Working rules

Implement the listed code, tests and current-state documentation in small coherent commits. Use the stated defaults; record bounded technical choices and evidence. Do not stop after producing another plan. Recheck source and unrelated changes before editing shared files.

No feature reductions are preapproved. A native API gap needs a supported replacement and proof, or a separately recorded user decision before cutover. TUI guidance is useful degraded-mode UX, not parity for a retained Web UI operation. Complete all unblocked work while an exact external/API/approval blocker is resolved.

Use disposable agent/data directories and owned/licensed fixtures. Do not relocate live Pi state, install optional native packages, trust projects or delete real documents for tests. Preserve native unknown records and user changes. No history rewrites, hidden SDK fallback, second provider system, second Pi MCP client or arbitrary remote frontend code.

Only this document records delivery status. A documentation consolidation does not complete the runtime tasks below. Add evidence when checking a task. Runtime plans, review observations and passing tests are separate kinds of information.

## Parallel ownership

| Stream | Owns | Coordination/start |
| --- | --- | --- |
| A — Contracts/integration | Actual method inventory, schemas, compatibility, context/selection and composition contracts | Immediately; approves shared schema edits with affected owners |
| B — Go assistant | Shared engine/facade, CLI, discovery, child I/O, catalog and lifecycle | Contract fixtures; API-03 feasibility begins early |
| C — Shell/navigation | Six slots, reducer, URL driver, saved workspace migration | Selection contract; sole owner of shared workspace state |
| D — Mewa/views | Adapters, foundation styles, lists/details/content views | Mewa inventory immediately; props coordinated with C |
| E — Integrations | Native UI semantics, generic bridge, MCP scope, registry and Browser contribution | A/B native contracts; exactly one response translation path |
| F — Reliability/security | Reproductions, failure/concurrency tests, boundaries and performance | Immediately, production fixes coordinated with code owner |
| G — Builds/docs/migration | Full-host composition, two build variants, image/release/units and state/installation docs | Facade with B; release identity already decided |
| H — Canvas | Versioned documents, isolated render jobs, six tools, registered UI | After Gate 5 and scoped worker contract |
| I — Openfig | Isolated parser/index, persistent slot, five tools, UI/shared focus and frame gate | Feature integration after Canvas Gate 6 |

One agent can fill all streams sequentially. Multiple agents use agreed fixtures instead of conflicting schemas/stores. Coordinate dependency locks, Dockerfile, registry, global CSS and shared state through one named editor per change. Research/licensed fixture preparation may happen early; Canvas and Openfig remain final feature integrations.

## Foundation and immediate regressions

- [ ] FIX-01 — F/B: production restart uses bounded shutdown and real executable/PID/boot-identity evidence, not only an injected callback.
- [ ] FIX-02 — F/E: failed module persistence leaves committed catalog, authorization and runtime unchanged.
- [ ] FIX-03 — F/E: repeated enable is a no-op preserving the active Browser handle; Restart is separate.
- [ ] FIX-04 — C/F: one unresolved create per UI action; selected-chat/project-empty reproduced or explicitly isolated; late results cannot steal navigation.
- [ ] FIX-05 — A/F: strict typed envelopes and deliberate legacy/new-protocol duplicate-ID behavior.
- [ ] FIX-06 — B/F: agent-file no-clobber create, revision-checked update/delete and path revalidation; concurrent external edits preserved.
- [ ] FIX-07 — B/D: native-supported thinking values, including max where supported; global defaults and session selection tested independently.
- [ ] FIX-08 — E/F: malformed Browser configuration degrades the module, never silently drops restrictive settings and starts defaults.
- [ ] API-01 — A: complete browser/controller/assistant/native method inventory and CP-01–17 mapping, including schedules absent from WS_METHODS.
- [ ] API-02 — A/B: one schema owner; host/browser/native versions, boot/checkpoint, errors, acceptance/settlement and Stop contracts.
- [ ] API-03 — B/E/A: real generic native-bridge feasibility for provider/settings/resources/MCP/liveness gaps on independent Pi; supported public interfaces only.
- [ ] STATE-01 — A/C: two independent selections, optional grouping, explicit cwd/admission, module context, routes and old-tab migration.
- [ ] MODULE-01 — A/E: backend/frontend descriptors and sidebar-only/viewer-only/unavailable fixtures with authenticated context.

API-03 is not a late cleanup task. Run it alongside initial native chat and shell slices; do not delete the SDK host before it establishes the managed-profile path. An upstream gap gets an exact version/API record and exit test, not a fabricated empty success.

## Shared Go assistant

- [ ] GO-01 — B: separate module/public facade, CLI/config precedence, selected executable and read-only doctor; standalone/npm/custom installation fixtures.
- [ ] GO-02 — B: bounded native JSONL, reader/writer/correlation/deadlines, child exit, partial/oversized lines and slow-consumer handling.
- [ ] GO-03 — B: Vanilla chat/create/images/model/thinking/abort/reopen; model picker independent of provider administration; native resources/trust preserved.
- [ ] GO-04 — B/F: clone/fork identity and cancellation, unpersisted drafts, bounded read-only catalog/history, unknown records and external replacements.
- [ ] GO-05 — B/A/F: correct settlement/retry/compaction/native continuation, one outbox owner, uncertainty and default Stop behavior.
- [ ] GO-06 — B/E: native UI response shapes, pending deadlines/epochs, passive projections and newer-draft protection.
- [ ] GO-07 — B/F: on-demand residency, conservative unknown liveness, managed process groups, cleanup and explicit idle TUI handoff.
- [ ] GO-08 — B/E/A: all retained CP families and optional enabled/absent integrations pass through supported native APIs/bridge; no silent reductions.
- [ ] GO-09 — B/G: staged legacy/Go state conversion, old/new protocol support and rollback evidence before retiring legacy packaging.

## Build composition and state

- [ ] BUILD-01 — B/G/A: same assistant/host facade in standalone and full-host, exact checkout module dependency, no forbidden internal imports or duplicate supervisor.
- [ ] BUILD-02 — B/G: assistant-only builds/runs without UI/controller/Chromium/parser dependencies.
- [ ] BUILD-03 — G/B: full-host one executable/service embeds engine/controller/real web assets; no external assistant or asset installation.
- [ ] BUILD-04 — G/A: Docker explicitly external, never discovers/spawns Pi; embedded loopback credential private; scoped owner conflict detection.
- [ ] BUILD-05 — G/F: complete service restart/quiesce/descendant cleanup; both user-unit alternatives and effective Docker init/subreaper behavior tested.
- [ ] MIG-01 — G/B: exact store/key/schema inventory and read-only migration dry-run; distinguish Pixie sidecars from native configuration.
- [ ] MIG-02 — G/F: transactional conversion/checkpoint, private backups/receipt, crash/retry/unknown-schema tests and preserved metadata.
- [ ] MIG-03 — G/B: both topology directions, explicit data-root mapping and native owner/deletion-binding validation, dispatch paused during switch.
- [ ] MIG-04 — G/F: schema-aware downgrade/rollback without queue, schedule or deletion-ledger rewind; uninstall leaves native state intact.

## Workspace and Mewa

- [ ] UI-01 — C: six-slot reducer/shell fixture and independent primary/secondary ownership, no generic mixed content tabs.
- [ ] UI-02 — C/D: real Chat + File split, focus/restore/close/hide and independent sidebars preserve draft, stream, scroll and focus.
- [ ] UI-03 — D/C: grouped/flat/ungrouped sessions, selected-row visibility, recent expansion, native titles, guarded create and persisted Archive.
- [ ] UI-04 — D/C: schedule list/detail/CRUD/run navigation and primary Settings reuse backend behavior; no second scheduler/large settings modal.
- [ ] UI-05 — D/C: session details, admitted Files, multiple-repository/no-repository Git and contextual secondary selections.
- [ ] UI-06 — C/F: back/forward/reload, stale/missing resources, old-state migration, local availability, mobile drawers and accessible splitters.
- [ ] MEWA-01 — D: pinned component/token/adapter inventory, preserving correct wrappers and integrity checks.
- [ ] MEWA-02 — D: single color/type/spacing/geometry owner, tested cascade and Svelte lifecycle; retire temporary mapping/generators with consumers.
- [ ] MEWA-03 — D/F: five content-filled light/dark modes, zoom/keyboard/reduced-motion/forced-colors where supported, long content and independent scroll.

## Integrations, security and release preparation

- [ ] EXT-01 — E/B: supported native UI and passive hints/liveness, explicit terminal-only limitations, generation-safe replay and final single response mapping.
- [ ] EXT-02 — E: one transactional registry, independent module status/toggle/restart/failure, retained unknown configuration and no stale reactivation.
- [ ] EXT-03 — E/C: Browser contribution in slots 4/5/6, existing leases/artifacts/cleanup, human availability separate from Pi-tool availability.
- [ ] EXT-04 — E/A/F: native MCP sole client, compatible generic bridge, session-scoped credentials and forged-ID/cross-generation failures.
- [ ] SEC-01 — F/G: actual Browser/auth/origin/filesystem/resource boundaries in Docker and combined host; all core recovery cases in acceptance.md.
- [ ] SEC-02 — F/E/G: choose/provision/test the isolated untrusted-worker contract; filesystem/network/total-memory/CPU/PID/wall/output enforcement and setup failure, no unrestricted fallback.
- [ ] PERF-01 — F: reproducible amd64/arm64 measurements of complete topologies, UI/history and optional workers; no language-only performance claim.
- [ ] PKG-01 — G/B: final binary/units/private config/version/doctor/readiness and independent install/start/stop/restart/remove fixtures.
- [ ] PKG-02 — G: sha-12 naming across Git tag/Release/archives/binaries/GHCR, four archives and both image platforms, full-source manifest/checksums/provenance and complete-set checks.
- [ ] PKG-03 — G/F: actual archives+matching images on both architectures, no separate assistant in full-host, no Pi in Docker, upgrade/rollback/mode switch.
- [ ] PKG-04 — G/F: frozen release inputs, collision/wrong-revision/partial-upload/retry tests, draft-first publication and read-only PR/main/schedule CI.
- [ ] DOC-01 — G/A: root instructions and source paths align with canonical roadmap and distinguish current implementation from target.
- [ ] DOC-02 — G: delete superseded docs, repair live links, keep operational docs brief and accurate; verify both released setup/migration examples once implemented.
- [ ] CUTOVER-01 — A: Gate 5 and every required CP row pass; remove only replaced legacy implementation; no unapproved reduction or hidden SDK in the Go artifacts.

## Penultimate feature: Canvas

- [ ] CAN-01 — H/E/F: session-authorized connection and real isolated rendering job before untrusted HTML execution.
- [ ] CAN-02 — H: full-document revisions, CAS/mutation identity, quota reservations, bounded/coalesced jobs, tombstones and restart.
- [ ] CAN-03 — H: six tools/guide, bounded rendered text/DOM and actual exact-version image bytes.
- [ ] CAN-04 — H/C/D: registered sidebar/view/tool cards, safe raster-backed live iteration and shared shell focus/restore.
- [ ] CAN-05 — H/F/G: cross-session/egress/disable/remove/restart races and optional dependency packaging under both deployment/architecture profiles.

## Last feature: Openfig

- [ ] FIG-01 — I/F: reproduce released public parser with owned/licensed real files and select/test isolated worker artifact.
- [ ] FIG-02 — I: archive/schema/expansion/index bounds, transactional instance-wide slot, source retention and dependency-free removal.
- [ ] FIG-03 — I/E: five read-only tools/shared query service, actual bounded images, stale-document/cursor/focus rejection.
- [ ] FIG-04 — I/C/D: pages/layers/frame candidates/direct text, labelled cover, private navigation versus shared-focus CAS and explicit reference-in-chat.
- [ ] FIG-05 — I/F/G: structural/offline/authority/failure acceptance in both topologies on supported architectures; sibling-module regressions.
- [ ] FIG-06 — I: supported upstream Design-frame renderer, licensed offline assets/fonts, real visual comparison, bounded pixels/cache and cancellation; leave exact API blocker open when unproven.

## Integration gates

| Gate | Exit evidence |
| --- | --- |
| 1 — Contracts | Regression fixtures; exact catalog and CP coverage; schemas, selection/module/authority/facade; API-03 proof or named blocker without deleting affected features |
| 2 — Vertical slices | Independent Vanilla Pi through both commands; real Chat + File shell; no optional prerequisite |
| 3 — Continuity | Images/clone/fork/retry/compaction/queues, dialogs/reconnect and five layout modes; no duplicate dispatch or stale selection |
| 4 — Integrations | Managed CP rows and optional enabled/absent profiles, registered Browser, scoped authority and local failure behavior |
| 5 — Core release-ready | All CP required rows, migration/rollback, four real archives + two image platforms, both units/topologies and truthful docs/security; no unapproved reduction |
| 6 — Canvas | CAN-01–05 including actual same-session isolation, live version/image evidence and deletion/egress tests |
| 7a — Design structure | FIG-01–05 with source retention, bounded offline worker and real headless/UI inspection |
| 7b — Design frames | FIG-06 with supported artifact and actual frame fidelity/offline/bounds evidence, independent of cover support |

Gate 5 permits local Canvas implementation, not publication. Canvas integrates before Openfig; renderer research may occur early. A verified frame API blocker leaves 7b open while 7a and all unrelated work continue. Neither binary variant nor Docker may be omitted from a release because it has no code change.

## Known decisions requiring implementation evidence

| Item | Owner | Exit test | While unresolved |
| --- | --- | --- | --- |
| Managed native bridge on supported Pi distributions | B/E/A, API-03 | CP-07/08/09/11/12/16 via public installed APIs, no replacement SDK or state reset | Build Vanilla/UI; retain explicit legacy option; managed cutover blocked |
| Selected-chat/project-empty screenshot cause | C/F, FIX-04 | Reproducible route/state fixture and corrected invariant | Guard duplicate create separately; preserve diagnostics and continue unrelated work |
| Worker enforcement implementation and deployment support | F/G, SEC-02 | Host/sibling/egress/resource attack fixtures in both topologies | Core remains usable; arbitrary Canvas/Design processing unavailable |
| Openfig frame renderer | I, FIG-06 | Released public Design-frame API, offline licensed assets and visual evidence | Finish structure; clearly label frame preview unavailable |
| Final release publication | G/operator | Approved full source/destinations/workflow and completed matrix | Build/test locally; no tag/image/Release publication |

## Carry-forward items

Standalone subagent verification is GO-08/PKG-03, not a forced extension dependency. Reassess the Bun child-launch and SDK llama-export patches against installed Pi; retain required legacy cases until proven obsolete. Upstream submissions need separate approval. Per-session nondisruptive reload stays deferred unless a verified retained requirement needs it. Completed native state relocation/history rewrites are not tasks to repeat. Existing releases stay immutable.

## Evidence and approvals

For each completed task record: task/CP IDs; commit; actual behavior/contract change; commands and environments; test result; UI/process/artifact evidence; state/rollback impact; remaining limitation. Do not count static inspection as runtime execution or remove a regression just because the replacement fails it.

Ordinary local implementation/test/commits proceed under the implementation request. Remote pushes/merges, upstream submissions, tag/Release/package/image publication, workflow dispatch and live service/native-state operations need their own authorization. A push approval covers publication it triggers. This documentation update is the requested roadmap change, not approval to publish or deploy its implementation.
