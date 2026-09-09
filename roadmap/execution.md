# Execution guide

Read [README.md](README.md), [contracts.md](contracts.md), [feature-coverage.md](feature-coverage.md), [acceptance.md](acceptance.md), the relevant findings in [repository-review.md](repository-review.md) and the assigned feature plan. Implement the tasks rather than writing another replacement plan. Use the reconciled roadmap on main; recheck the actual checkout and concurrent changes.

## Working rules

Use the specified defaults and one owner per state/delivery/runtime concern. Preserve user changes, unknown native records, durable authority and retained behavior. Test with disposable state and owned/licensed fixtures, not live credentials, documents or repositories. The single review is evidence; current requirements live in the contracts/plans and only this ledger records progress.

Do not rewrite history, silently install native extensions, relocate Pi state, remove failing regressions or expand scope into a terminal/IDE/agent framework. Missing optional capabilities stay local; required authority/containment failures stay closed. A TUI workaround is not a retained Web UI feature's replacement.

Investigate native bridge feasibility early, alongside the first vanilla and shell slices. An exact public API blocker has a coverage ID, reproduction, owner and release consequence. Continue independent work while it remains open; do not claim a mock proves an absent native API exists.

## Approval boundaries

Remote writes/merges, upstream submissions, publication-policy enablement and live-service/native-state operations require their own authorization. Inspect indirect workflow effects before a push. Local implementation/tests/coherent commits proceed under the implementation request; documentation edits do not enable releases or deployment.

After explicit approval, the continuous-release policy in builds-and-releases.md is standing authorization for eligible main commits. Before that, prepare/test without remote publication. Do not invent per-artifact version approvals after the policy is enabled, or treat it as approval for live deployment.

## Parallel ownership

| Stream | Owner surface | Dependency |
| --- | --- | --- |
| A — Contracts/integration | Method/FC inventory, v2 schemas, identity/authority, shared composition | Initial source review |
| B — Go assistant | Native I/O, supervision, catalog, public facade and bridge host | Agreed A fixtures |
| C — Shell/navigation | Shared state, routes, layout, draft migration and upgrade recovery | Selection contract |
| D — Mewa/views | Foundation/adapters and list/detail/content views | Inventory immediately; C props |
| E — Extensions | Native UI semantics, bridge adapter, registry/routing and Browser | A capabilities; B transport |
| F — Reliability/security | Regressions, subprocess/resource controls, recovery, worker profiles and measurements | Immediately |
| G — Builds/docs | Combined composition, both artifacts/Docker, units, release/install/rollback/docs | Facade/identity; factual docs immediately |
| H — Canvas | Canvas store/jobs/tools/view | Core Gate 5 and worker/session authority |
| I — Openfig | Design worker/store/query/view and frame gate | Accepted Canvas foundation after Gate 5 |

A integrates shared schemas, C shared workspace state, B native I/O, E primitive/adapter/module semantics and G release composition. Responses have one translation owner. Coordinate shared lockfiles, Dockerfile, CSS and registry edits through a designated editor; use fixtures rather than parallel conflicting implementations.

## Initial checks

Record revision and concurrent changes; recheck F01–F41 against source. Capture current browser/host/native behavior separately and preserve correct tests. The isolated probes recorded in repository-review.md confirm existing behavior, not fixed production behavior. Promote them into real endpoint/subprocess regressions and invert unsafe expectations when implementing fixes.

The consolidation changes documentation, not runtime, dependencies or workflows. Current operating docs remain true until corresponding behavior ships. Native bridge, services, workers, complete application and release artifacts still need their own live evidence. Use [migration.md](migration.md) for the detailed state inventory, staged conversion, topology switching and no-ledger-rewind procedure under MIG-01.

## Task ledger

Every task below remains open until implementation evidence is recorded. FC01–FC33 and X01–X14 are required traceability, not optional tests. Discovering another retained method extends its appropriate row rather than deleting the method as cleanup. Consolidation retains the 78 existing tasks; findings F39–F41 are assigned to their existing owners below rather than creating duplicate work.

### Regressions and contracts

- [x] FIX-01 — F/B: real production restart exits through bounded teardown and obtains a new process/boot identity (F02). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-01--production-assistant-restart).
- [x] FIX-02 — F/E: known pre-publication module-save failure preserves prior committed state/runtime; post-publication uncertainty follows FIX-13 (F03/F32). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-02-and-fix-03--mcp-module-persistence-and-lifecycle).
- [x] FIX-03 — F/E: unchanged enable preserves Browser handles; explicit Restart is separate (F04). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-02-and-fix-03--mcp-module-persistence-and-lifecycle).
- [x] FIX-04 — C/F: guarded create/idempotency and selected-chat/empty-panel reproduction with stale-generation rejection (F07). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-04--guarded-chat-creation-and-stale-selection-rejection).
- [x] FIX-05 — A/F: strict host envelopes and deliberate v1/v2 duplicate behavior without changing browser/native ID domains (F05/F34). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-05--strict-v1-host-envelopes-and-handshake).
- [x] FIX-06 — B/E/F: native thinking levels across session/default/schedule paths, including supported max (F22). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-06--native-thinking-levels-including-max).
- [x] FIX-07 — B/F: agent content revisions, exclusive create, rooted identity and stale/external-edit handling with explicit writer assumptions (F23). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-07--agent-revisions-exclusive-create-and-rooted-edits).
- [x] FIX-08 — B/F: literal-loopback assistant bind and validated startup inputs (F26). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-08--assistant-loopback-binding-and-startup-validation).
- [x] FIX-09 — B/G/F: service deadline encloses creation/admin/extensions/in-flight teardown, not just provider abort (F27). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-09--bounded-assistant-lifecycle-deadlines).
- [x] FIX-10 — F/A: configured Host allowlist plus separately checked normalized Origin/proxy policy across real routes; X01 (F29). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-10--controller-host-authority-and-mcp-routing).
- [x] FIX-11 — F/A: non-executing Git inspection, harmless clean/process-filter endpoint regressions and explicit raw-conversion semantics; X02 (F30). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-11--read-only-git-inspection-and-termination).
- [x] FIX-12 — F/G: controller child-group termination and finite pipe draining, including Git and final container reaping; X03 (F31/F40). Evidence: [CHANGELOG.md](CHANGELOG.md#fix-12--bounded-child-termination-and-container-reaping).
- [ ] FIX-13 — F/A/E: typed publication/durability outcomes, reconciliation and post-rename fault injection; X04 (F32).
- [ ] API-01 — A: exhaustive browser/controller/host/native catalog and FC mapping, including schedule methods absent from constant lists and removal of broad Administration/provider assumptions (F20/F41).
- [ ] API-02 — A/B: host v2 schemas/epochs/snapshot/settlement/error fixtures; separate browser/native request IDs and compatibility; X06 (F34).
- [ ] API-03 — A/B/F: durable paired authority independent of ephemeral dialing, legacy recovery-blocked migration (F21).
- [ ] STATE-01 — A/C: independent selections, nullable project grouping, explicit cwd/admission, routes/drafts/layout migration.
- [ ] MIG-01 — A/B/F with G: full metadata inventory, staged conversion, topology switching and schema-aware rollback from migration.md; ungrouped queues/deletions, old archive metadata and no ledger rewind (F25/F32).
- [ ] MODULE-01 — A/E: trusted contribution descriptors and sidebar-only/viewer-only fixtures with real scope authority.
- [x] ROUTE-01 — E/A/F: registered MCP/management/artifact routing through the assembled HTTP handler, reserved routes and no API-to-SPA fallback; X05 (F33). Evidence: [CHANGELOG.md](CHANGELOG.md#route-01--assembled-reserved-routing-without-spa-fallback).
- [ ] LIMIT-01 — A/B/F: composed input/store/transport caps, aggregate-byte admission, control reserve and bounded Design index artifact; X07–X09 (F35).
- [ ] LIFE-01 — B/E/C/F: verified Stop outcomes and explicit safe idle-runtime release under capacity pressure; X10–X11 (F36).

### Shared assistant and retained features

- [ ] GO-01 — B: separate module/facade, CLI/config/discovery/read-only doctor and independent Pi installations.
- [ ] GO-02 — B: bounded native JSONL, writer/correlation/admission/backpressure, child exit and stalled-pipe handling.
- [ ] GO-03 — B: vanilla create/prompt/images/model/thinking/abort/reopen with native resource/trust semantics.
- [ ] GO-04 — B/F: clone/fork identity/cancellation, drafts, bounded read-only catalog/history, unknown entries and external replacement.
- [ ] GO-05 — B/A/F: prepared/dispatching/accepted/settled/uncertain outbox, retry/compaction/continuation and verified Stop.
- [ ] GO-06 — B/E: supported native UI/latest passive state, draft conflicts and generation-safe replay.
- [ ] GO-07 — B/F: residency/capacity, detached-work disposition, explicit runtime release, descendants and idle TUI handoff.
- [ ] BRIDGE-01 — B/E/A: public installed import/context/storage feasibility on npm/standalone Pi, private bounded channel and explicit asset opt-in.
- [ ] BRIDGE-02 — E/B: every retained MCP admin operation uses a supported adapter API, not private AgentSession tool access (F28).
- [ ] BRIDGE-03 — E/B: exact native UI cancellation/working hints or precise FC15 upstream blocker, without fabricated acknowledgments (F24).
- [ ] GO-08 — B/E/A: all declared FC17–FC28 administration/authoring/optional profiles retain tested behavior; no TUI-only substitution.
- [ ] GO-09 — B/G: staged legacy compatibility and schema-aware rollback before host/npm retirement.

### Builds and release pipeline

- [ ] BUILD-01 — A/B/G: one public assistant/host facade/lifecycle; no copied implementation or forbidden internal imports.
- [ ] BUILD-02 — B/G: assistant-only independently builds/runs without controller/UI/worker closure.
- [ ] BUILD-03 — G/B: one full-host binary/service embeds engine/controller/real UI; no separate assistant/assets/Docker for core.
- [ ] BUILD-04 — G/A: Docker explicitly controller-only, never starts Pi; combined private transport/pairing/ownership tested.
- [ ] BUILD-05 — G/F: whole-composition drain/restart, both unit choices, effective final-container init/descendant handling and mode switch (F40).
- [ ] REL-01 — G/A: one sha-<12> identity from exact full commit across tag/Release/binaries/archives; no hash ordering or run-counter fallback.
- [ ] REL-02 — G: matching Docker tag with both runnable platforms, source/version labels and recorded index/platform digests.
- [ ] REL-03 — G/F: complete four-archive/image staging, provenance, immutable retries/collisions/partial failure and non-regressing latest.
- [ ] REL-04 — G: validate-only PR/schedule/docs paths and explicitly approved automatic main policy; no tag loops or untrusted privileged publication.

### Workspace and Mewa

- [ ] UI-01 — C: six-slot reducer/shell against fixture transport, no mixed-content tab ownership.
- [ ] UI-02 — C/D: real Chat + File split and independent collapse/focus/restore with draft/stream/scroll/focus continuity.
- [ ] UI-03 — D/C: grouped/flat/ungrouped catalog, recent expansion, native titles, selected/running visibility and Archive.
- [ ] UI-04 — D/C: Schedules and primary Settings list/detail/run navigation reuse backend state and semantics.
- [ ] UI-05 — D/C: Details, multi-repository Git, read-only files/diffs and context-safe previews; show eligible idle-runtime release.
- [ ] UI-06 — C/F: v2 routes/back-forward/restore, invalid/missing/stale states, responsive layouts and keyboard resizers.
- [ ] UI-07 — C/G/F: old-tab/new-server and lazy-asset upgrade recovery without draft loss or mutation re-execution; X14 (F38).
- [ ] MEWA-01 — D: pinned tokens/components/adapters and preserved correct wrappers/integrity.
- [ ] MEWA-02 — D: one foundation owner/cascade/lifecycle; retire mappings/generators only after consumers migrate.
- [ ] MEWA-03 — D/F: five content-filled light/dark layouts, zoom/keyboard/focus/overflow and no view-driven runtime loss.

### Integrations, validation and docs

- [ ] EXT-01 — E/B: every supported native UI row, exact limitations and single final response mapping.
- [ ] EXT-02 — E: generalized registry, complete-map persistence outcomes, independent desired/readiness states and no lock-held startup; mixed invalid/restrictive config fails locally without permissive fallback (F39).
- [ ] EXT-03 — E/C: Browser in slots 4/5/6, leases/artifacts/cleanup, human/tool availability and explicitly tested deployment profile.
- [ ] EXT-04 — E/A/F: generic scoped native MCP registration/credentials/revocation; forged IDs grant nothing.
- [ ] SEC-01 — F/G: actual HTTP/auth/CSRF/filesystem/Browser posture in both modes; X01/X02/X13 without unsupported isolation claims.
- [ ] SEC-02 — F/G: actual launcher/delegation, pre-exec resource placement, scratch/inode/egress/descriptor/descendant tests on both architectures; X12/X13 (F37).
- [ ] PERF-01 — F: repeated full-process amd64/arm64 and content-filled UI measurements, including decoded/buffer memory; bounds change only with evidence.
- [ ] PKG-01 — G/B: final archive/unit/config/version/doctor/readiness/start/stop/restart/uninstall evidence.
- [ ] PKG-02 — G: complete commit-named release set and fix/retire stale legacy npm guards (F19).
- [ ] PKG-03 — G/F: real matching Docker+assistant and full-host artifacts, both architectures, upgrade/rollback/mode-switch, effective entrypoint and old-browser recovery.
- [ ] DOC-01 — G/A: root guidance/commands/contracts track implementation and canonical owners.
- [ ] DOC-02 — G: brief current-state docs, no superseded plans or separate review copies, checked links/anchors/examples and supported installation profiles.
- [ ] COVERAGE-01 — A/F: all mandatory FC rows and applicable X tests have actual native/bridge/UI/artifact evidence; reductions need specific approval.
- [ ] CUTOVER-01 — A: pass core gates before removing replaced runtime/UI paths.

### Penultimate feature: Canvas

- [ ] CAN-01 — H/E/F: actual session authority and contained worker/resource profile before untrusted HTML.
- [ ] CAN-02 — H: immutable revisions, retry/CAS/quota/tombstone state with explicit persistence uncertainty and recovery.
- [ ] CAN-03 — H: six tools/guide, bounded DOM/text and actual exact-version image content.
- [ ] CAN-04 — H/C/D: registered HTTP/UI contribution/cards, safe raster-first live preview and stale-version display.
- [ ] CAN-05 — H/F/G: cross-session/egress/disable/delete/overflow/restart tests through both deployment/architecture profiles.

### Last feature: Openfig

- [ ] FIG-01 — I/F: independent released public parser/runtime, licensed real files and tested enclosed worker.
- [ ] FIG-02 — I: bounded preflight/index artifact path, transactional instance-wide slot and durable source/removal; X09.
- [ ] FIG-03 — I/E: shared read-only query service/five tools, real cover bytes, bounded cursors and source/focus identity.
- [ ] FIG-04 — I/C/D: pages/layers/frame candidates, private versus shared focus, explicit draft reference and disabled removal.
- [ ] FIG-05 — I/F/G: structural/offline/hostile-file/authority/recovery and multi-module tests in both modes/architectures.
- [ ] FIG-06 — I: actual supported upstream frame renders with offline/license/fidelity/bounds evidence, or explicit external API blocker; cover extraction does not complete it.

## Gates

**Gate 1 — Contracts/regressions.** Relevant consolidated findings have fixtures; exhaustive FC catalog, separated request domains, v2 capability/authority, route ownership, selection and shared-build seam are agreed. BRIDGE feasibility begins here.

**Gate 2 — Independent slices.** Real selected vanilla Pi conversation with no optional package and a real Chat + File shell. Both executable compositions use the same facade.

**Gate 3 — Continuity/metadata.** Images, commands/retry/compaction, clone/fork, outbox/Stop, UI epochs/layouts, idle-release capacity, ungrouped migration and durable/persistence recovery pass. No duplicate uncertain delivery.

**Gate 4 — Retained integrations.** Required declared FC profiles, provider/auth/settings/resources, UI/MCP fidelity and registered Browser pass. A core-only build with old controls disabled cannot pass without individually approved reductions. Browser's actual deployment boundary is documented/tested, not inferred from module labels.

**Gate 5 — Core cutover.** Four final archives and both OCI platforms pass actual installation/systemd/mode-switch/rollback and complete-set publication-failure tests. Applicable X01–X08/X10–X14 pass with old-browser upgrade recovery and truthful profiles/docs. X09 belongs to later Design. Only now retire replaced implementation; publication can still await approval without blocking local Canvas work.

**Gate 6 — Canvas.** All CAN tasks and applicable cross-boundary storage/routing/worker cases pass with actual scoped rendering, not just an image.

**Gate 7 — Openfig.** Structure and real frame previews have separate evidence. Structure includes X09 and all applicable authority/worker/persistence cases. A supported renderer gap leaves FIG-06 open while other work completes; no cover substitution or false final completeness claim.

## Carry-forward work and evidence

Standalone subagent/child-launch patches remain FC24/GO-08/PKG-03 work, not forced dependencies or an npm release before Go. Reassess SDK/extension patches against selected Pi and retire only with tested replacements. arm64 measurement is PERF-01. Per-session nondisruptive reload remains deferred. Completed host relocation/history rewrites are not tasks to repeat.

For a completed task record task/FC/X IDs, implementation commit, behavior/profiles, actual commands/environment/results, UI/native/process/artifact evidence, migration/authority/rollback impact and remaining blocker. Static inspection, copied-function probes, endpoint tests, live Pi and final systemd/worker artifacts are distinct. Do not copy earlier test counts or mark runtime work complete because its roadmap is written.
