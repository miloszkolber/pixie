# Execution guide

Read [README.md](README.md), [contracts.md](contracts.md), [feature-coverage.md](feature-coverage.md), both repository reviews and the assigned feature plan. Implement the tasks; do not produce another high-level plan instead of code. The latest main roadmap is the baseline, not the earlier alternative branch.

## Working rules

Use the specified defaults and one owner per state/delivery/runtime concern. Record implementation choices with evidence. Preserve user changes, unknown native data, recovery claims and supported behavior. Use disposable native directories and owned/licensed fixtures, never live user credentials or documents for testing.

A roadmap request authorizes local coding/testing/coherent commits. Do not rewrite history, auto-install native extensions, relocate Pi state, disable tests to hide regressions or expand scope into a terminal/IDE/agent framework. Optional dependency failure stays local; mandatory security/authority failure stays closed.

Known external API limitations have a coverage ID, reproduction, owner and release consequence. Continue independent work while blocked. A TUI fallback does not satisfy a retained web-feature row. Existing source/CI success is not evidence that the new protocol, bridge, binary or UI works.

## Approval boundaries

Remote branch writes/merges, upstream submissions, initial publication-policy enablement and changes to live services/native state require their own authorization. Inspect workflow side effects before pushing. Documentation commits do not authorize releases or deployments.

The target continuous-release policy in builds-and-releases.md runs automatically for eligible main commits only after its initial explicit approval. That approved policy is standing publication authorization, not permission for unrelated live deployment. Before approval, build/test/stage locally without remote publication. Do not keep a contradictory per-version approval requirement after that policy has explicitly been enabled.

## Parallel ownership

| Stream | Owner surface | Dependency |
| --- | --- | --- |
| A — Contracts/integration | Method inventory, v2 schemas, FC map, authority/migration and shared composition seam | Initial review |
| B — Go assistant | Native I/O/supervision/catalog, public facade, standalone binary and native bridge host | A contracts |
| C — Shell/navigation | Shared workspace state, router, layout, drafts and context resolution | A selection contract |
| D — Mewa/views | Foundations/adapters and content/list/detail views | Component inventory; C props |
| E — Extensions | Native UI semantics, bridge adapter operations, module registry, Browser and scoped MCP | A capabilities; B transport |
| F — Reliability/security | Regression reproductions, resource enforcement, recovery, worker profiles and measurements | Immediately |
| G — Builds/docs | Combined composition with B, both artifacts, Docker, units, release policy, install/rollback/docs | A facade/identity; factual docs can start now |
| H — Canvas | Canvas service/store/tools/render/view | Core Gate 5 plus worker/session authority |
| I — Openfig | Design worker/store/query/view and renderer gate | Core Gate 5 and accepted Canvas foundation |

A alone integrates shared schemas, C the shared workspace store, B child I/O, E UI/adapter semantics and G release identity/composition. Native UI responses have one translation owner. Other streams use fixtures/props instead of broad conflicting edits. H/I reuse registry and worker contracts; they do not re-generalize Browser.

## Initial checks

Record checkout revision and concurrent changes. Recheck F01–F28 against actual source before fixing already-changed code. Capture exact legacy method/transport/UI behaviors and narrow baseline checks. Independently inspect native published artifacts before assuming public bridge access. Start BRIDGE feasibility early, not after deleting the SDK host.

The documentation cleanup installing this roadmap removes only duplicate planning files and updates references. Runtime implementation tasks below remain open. Keep current operating docs true until corresponding target behavior ships.

## Task ledger

Check a task only with completion evidence. FC01–FC33 are required traceability rows, not optional suggestions. Expand discovered methods/tests under the appropriate row.

### Regressions and contracts

- [ ] FIX-01 — F/B: production restart actually exits through bounded teardown; executable/service regression (F02).
- [ ] FIX-02 — F/E: failed module persistence preserves effective state and live Browser handle (F03).
- [ ] FIX-03 — F/E: unchanged enable is a no-op; explicit Restart is separate (F04).
- [ ] FIX-04 — C/F: guard create/idempotency; reproduce selected-chat/empty-panel with selection/request generations (F07).
- [ ] FIX-05 — A/F: strict envelope/IDs and explicit v1/v2 duplicate semantics (F05).
- [ ] FIX-06 — B/E/F: native supported thinking levels throughout session/default/schedule paths, including max (F22).
- [ ] FIX-07 — B/F: agent source revisions, exclusive create, no-follow identity and stale/external edit conflicts (F23).
- [ ] FIX-08 — B/F: literal-loopback host binding and validated startup configuration (F26).
- [ ] FIX-09 — B/G/F: whole-service deadline covers construction/admin/extensions/inflight, not only provider abort (F27).
- [ ] API-01 — A: exhaustive browser/controller/host/native operation inventory mapped to FC rows; remove mandatory provider/Administration compatibility (F20).
- [ ] API-02 — A/B: v2 schema/bindings and fixture suite for epochs, snapshots, acceptance/settlement, capability/error/version negotiation.
- [ ] API-03 — A/B/F: paired durable authority independent of ephemeral transport; legacy binding migration/recovery-blocked behavior (F21).
- [ ] STATE-01 — A/C: independent selections, nullable project grouping, context, routes, draft/URL/layout migration and reducer invariants.
- [ ] MIG-01 — A/B/F: full metadata inventory/conversion/rollback, especially project-required queues/deletions and old assistant archive metadata (F25).
- [ ] MODULE-01 — A/E: contribution descriptors and sidebar-only/viewer-only fixtures; real scope authority, not tool fields.

### Shared assistant and retained features

- [ ] GO-01 — B: separate module/facade, CLI/config/discovery/read-only doctor and independent installations.
- [ ] GO-02 — B: bounded JSONL, writer/correlation/backpressure, cancellation and child exit.
- [ ] GO-03 — B: vanilla create/prompt/images/model/thinking/abort/reopen and native resource/trust semantics.
- [ ] GO-04 — B/F: clone/fork identity, empty drafts, bounded readonly catalog/history/unknown entries/external replacement.
- [ ] GO-05 — B/A/F: prepared/dispatching/accepted/settled/uncertain outbox, retries/compaction/continuations and exact Stop.
- [ ] GO-06 — B/E: supported native UI, latest passive state, draft conflicts and generation-safe replay.
- [ ] GO-07 — B/F: process residence/limits, explicit release, detached-work policy, complete descendant shutdown and idle TUI handoff.
- [ ] BRIDGE-01 — B/E/A: independently prove public native import origin/context/storage APIs in npm/standalone profiles; private bounded channel and opt-in asset loading.
- [ ] BRIDGE-02 — E/B: map every retained MCP admin operation to supported adapter APIs; no AgentSession private-tool access assumption (F28).
- [ ] BRIDGE-03 — E/B: exact UI cancellation and working-hint fidelity through supported observable APIs, or precise upstream blocker for FC15 (F24).
- [ ] GO-08 — B/E/A: FC17–FC28 administration/authoring/integration coverage complete in declared profiles; no silent TUI-only substitution.
- [ ] GO-09 — B/G: staged legacy compatibility and metadata rollback; retire old host/npm only after mandatory coverage passes.

### Required builds and release pipeline

- [ ] BUILD-01 — A/B/G: one assistant/host facade and shared lifecycle; no copied implementation/internal import violation.
- [ ] BUILD-02 — B/G: independently build/run assistant-only without controller/UI/worker dependencies.
- [ ] BUILD-03 — G/B: full-host single executable/service embeds assistant/controller/UI; core needs no separate assistant, web assets or Docker.
- [ ] BUILD-04 — G/A: Docker controller-only mode never starts Pi; combined private transport, pairing and ownership conflicts tested.
- [ ] BUILD-05 — G/F: whole-composition restart/stop, two unit alternatives, preserved outbox/state and mode switch.
- [ ] REL-01 — G/A: derive one sha-<12> release/tag/archive/binary identity from exact full source commit; no semver ordering or run-number fallback.
- [ ] REL-02 — G: same Docker commit tag, two runnable OCI platforms, source/version labels and index/platform digests in manifest.
- [ ] REL-03 — G/F: four archives plus image complete-set staging/provenance, immutable retries/collision/partial failure and non-regressing latest promotion.
- [ ] REL-04 — G: validate-only PR/schedule/docs paths and approved automatic main policy; no tag build loops or untrusted privileged publication.

### Workspace and Mewa

- [ ] UI-01 — C: six slots and independent selection reducers against fake transport; no mixed-content-tab ownership.
- [ ] UI-02 — C/D: real Chat + File split; independent collapse/focus/restore with draft/stream continuity.
- [ ] UI-03 — D/C: grouped/flat/ungrouped catalog, five-item recent expansion, native titles and explicit Archive.
- [ ] UI-04 — D/C: Schedules list/detail/run navigation and Settings primary sections using retained backend behavior.
- [ ] UI-05 — D/C: session details, multi-repository Git, file/diff views and context-safe secondary selection.
- [ ] UI-06 — C/F: exact v2 routes/back-forward, legacy persistence migration, stale/missing/unauthorized states, responsive layouts and accessible resizers.
- [ ] MEWA-01 — D: pinned token/component/adapter inventory and retained correct wrappers/integrity checks.
- [ ] MEWA-02 — D: one foundation owner and tested cascade/lifecycle; retire temporary mappings/generators only with migrated consumers.
- [ ] MEWA-03 — D/F: five content-filled modes, light/dark/zoom/keyboard/focus/overflow and no layout-driven runtime loss.

### Integrations, validation and documentation

- [ ] EXT-01 — E/B: all supported native UI rows, precise limitations and no fabricated acknowledgments.
- [ ] EXT-02 — E: one generalized registry; independent desired/runtime states, unrelated values preserved and no lock-held blocking startup.
- [ ] EXT-03 — E/C: Browser in slots 4/5/6 with existing leases/artifacts/cleanup; panel versus agent-tool availability separate.
- [ ] EXT-04 — E/A/F: generic per-session native MCP registration/credentials and revocation; forged IDs confer no authority.
- [ ] SEC-01 — F/G: actual Browser/auth/origin/CSRF/path/size posture and deployment flags in both modes.
- [ ] SEC-02 — F/G: concrete optional worker launcher/delegation/enclosure, hard limits, canary/egress/descendant tests on both architectures before Canvas/Design.
- [ ] PERF-01 — F: repeated amd64/arm64 total-process and content-filled UI benchmarks; bounds revised only with evidence.
- [ ] PKG-01 — G/B: final artifacts/units/configs and actual doctor/version/readiness/start/stop/restart/uninstall checks.
- [ ] PKG-02 — G: complete commit-named release set; fix/retire stale legacy npm package guards (F19).
- [ ] PKG-03 — G/F: real candidate Docker + assistant archives and full-host archives on both architectures; upgrade/rollback/mode-switch failure matrix.
- [ ] DOC-01 — G/A: root guidance/commands/contracts remain aligned as actual implementation paths land.
- [ ] DOC-02 — G: brief accurate operating docs for shipped profiles/builds; full link/anchor/example checks and no deleted-path references.
- [ ] COVERAGE-01 — A/F: every mandatory retained FC row has native/bridge/UI/artifact evidence; specific reductions require approval.
- [ ] CUTOVER-01 — A: all core gates pass before superseded runtime/UI paths are removed.

### Penultimate feature: Canvas

- [ ] CAN-01 — H/E/F: session authority, concrete worker bounds and offline enforcement.
- [ ] CAN-02 — H: immutable revisions, idempotent mutation results/quota reservations/tombstones and restart.
- [ ] CAN-03 — H: six tools/guide, bounded DOM/text and actual image bytes with exact version/render identity.
- [ ] CAN-04 — H/C/D: registered sidebar/view/cards, raster-first live preview and stale-version display.
- [ ] CAN-05 — H/F/G: cross-session/egress/disable/delete/overflow/restart tests and both deployment variants/architectures.

### Last feature: Openfig

- [ ] FIG-01 — I/F: independent pinned public parser/runtime with licensed real Design files and tested worker package.
- [ ] FIG-02 — I: bounded preflight/index, persistent single-instance document slot and transactional source retention/removal.
- [ ] FIG-03 — I/E: shared query service/five readonly tools, actual cover images and source/focus revision checks.
- [ ] FIG-04 — I/C/D: pages/layers/frame candidates, private versus explicit shared focus, reference insertion and removal while disabled.
- [ ] FIG-05 — I/F/G: structure MVP, hostile-file/offline/authority/recovery matrix and both modes/architectures.
- [ ] FIG-06 — I: actual public upstream-rendered frame previews with fidelity/offline/license evidence, or exact external API blocker; a cover is not completion.

## Gates

**Gate 1 — Contracts and regressions.** F02–F05/F07 and second-pass regressions have fixtures. Method/FC map, v2 capabilities, authority, optional projects, selections and two-build seam are agreed. Bridge feasibility begins here.

**Gate 2 — Independent slices.** Selected independent vanilla Pi executes a real conversation with no optional package; the new shell shows a real chat plus file. Both executable compositions share the same facade.

**Gate 3 — Continuity and metadata.** Images, native commands/retry/compaction, fork/clone, queued work, Stop, UI/dialog epochs, all layout modes, ungrouped migration and durable recovery bindings pass. No duplicate uncertain execution.

**Gate 4 — Retained administration/integrations.** COVERAGE-01 passes for declared profiles, including provider/auth/settings/resource configuration and supported UI/MCP fidelity. Browser uses the registry. A core-only binary with disabled old features cannot pass this gate without individually approved reductions.

**Gate 5 — Core cutover/distribution.** Four final archives and both image platforms pass real installation/systemd/mode-switch/rollback tests. Every surface has the same commit release identity; publication retry failures are tested. Docs match actual behavior. Only now retire legacy implementations. Actual remote publication can still await policy approval; that does not block local Canvas work.

**Gate 6 — Canvas.** All CAN work passes actual session authority and offline resource enforcement, not just a screenshot.

**Gate 7 — Openfig.** Structure and frame previews have separate evidence. FIG-06 is not satisfied by cover extraction. An external renderer/API block stays visible; no final claim of full completion while a required milestone is blocked.

## Carry-forward work

Standalone optional subagents and native child-launch patches belong to FC24/GO-08/PKG-03, not a forced npm release before Go. arm64 measurements belong to PERF-01. Reassess SDK/extension patches against selected native runtime and retain until tested replacement/retirement; upstream submission is separately authorized. Right-rail modules are EXT/UI work. Non-disruptive per-session reload remains deferred. Completed host relocation/history rewriting are not tasks to repeat.

## Completion evidence

For each completed task record:

```text
Task and FC IDs:
Implementation commit(s):
Behavior/contracts and supported profiles:
Commands/tests actually executed, outcome and artifacts:
UI/native/process/systemd/release evidence:
Migration, authority and rollback impact:
Limitations or exact external blocker:
```

Keep links to machine test artifacts where available. Static review, mocked events, independent native execution and final service artifacts are different evidence. Do not copy old test counts or call planned commands executed. The current second-pass documentation change records review/plan corrections only; all runtime/bridge/release implementation checks above still require execution.
