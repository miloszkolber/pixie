# Execution and handoff

Read [README.md](README.md), [repository-review.md](repository-review.md), and the plan for the assigned area. Root AGENTS.md defines repository conventions; this directory specifies the requested target architecture. The two release variants in [07](07-build-release.md) are mandatory throughout, including later module releases.

## Working method

Inspect the checkout, preserve unrelated changes and map review findings to current source. Write regressions before fixing confirmed defects. Distinguish a source defect from an unverified screenshot explanation. Build one real native conversation and one real Chat + File layout before broad replacement.

Use small single-outcome commits and do not rewrite history. Keep legacy runtime selectable until cutover requirements pass, but never let two hosts write one session. User documentation tracks implemented behavior rather than the future plan.

A owns shared contract changes with affected callers/fixtures. Implementers can work against agreed mocks, not competing methods or selection stores. One agent may fill all roles sequentially; multiple agents should follow these file boundaries.

## Parallel ownership

| Owner | Owns | Coordination boundary |
| --- | --- | --- |
| A — Integration | Schemas, capability dispositions, context/selection/composition contracts, migration fixtures | Shared contract changes |
| B — Assistant | assistant/ Go engine, public host facade, child I/O, supervision, catalog and projections | Native lifecycle; E owns primitive semantics |
| C — Shell | Workspace shell/routes, selection reducer and layout persistence | Shared navigation/store migration |
| D — Mewa/views | Adapters, foundation styles, primary/secondary views | Props with C; no independent shell state |
| E — Modules | Publisher registry, Browser contribution, native UI mapping | A's contracts and B's native transport |
| F — Reliability | Regressions, auth/filesystem boundaries, recovery and measurements | Production fixes with their code owner |
| G — Delivery/docs | Both binary compositions, units/archives, CI/release, install tests and docs | CLI/facade with B; controller composition with A |
| H — Canvas | Canvas store/worker/MCP and registered UI | Starts after K5; no registry rewrite |
| I — Design | Openfig worker/document service/MCP and registered UI | Final stage; established module/worker boundaries |

Avoid simultaneous edits to the dependency lock, Dockerfile, global workspace store, protocol schema or registry. Assign shared edits to one owner and integrate dependents afterward.

## Initial tickets

| Ticket | Owner | Deliverable |
| --- | --- | --- |
| FIX-01 | F/B | Executable restart regression, bounded termination and changed process identity |
| FIX-02 | F/E | Transactional enablement and duplicate-enable no-op preserving active Browser |
| FIX-03 | F/C | Duplicate-create guard and selected-chat/project-empty reproduction fixture |
| FIX-04 | A/F | Strict request IDs and dispatcher-based host method inventory |
| API-01 | A | Capability disposition, schemas, error/epoch rules and conformance recordings |
| STATE-01 | A/C | Independent selections, optional project membership and route/persistence invariants |
| DESIGN-01 | D | Pinned Mewa inventory, adapter ownership and content-filled fixture |
| MODULE-01 | A/E | Context/lifecycle descriptor and sidebar-only/viewer-only fixtures |
| BUILD-01 | B/G/A | Reusable assistant facade, standalone/combined entrypoints, exact-checkout dependency and four-archive matrix |

FIX-01–04 and contract work precede broad replacement. Do not block unrelated work on an unreproduced UI report: retain its diagnostics and continue independent tasks.

## Delivery packages

**B1 — Native vertical slice.** Discover independent Pi, create, stream, stop, reopen and reconnect through the Go engine. Exercise the standalone host and the same engine embedded in pixie. Then complete history, clone/fork, images, retry/compaction, queues, dialogs and capability dispositions, followed by migration/artifacts. See [02](02-assistant-go.md).

**C1/D1 — Workspace vertical slice.** Real conversation and file together, focus/restore, independent collapse, retained draft. Then Chats, Archive, Schedules, Settings, Details, Files and Git; saved-state migration; removal of generic tabs; responsive/accessibility fixtures. See [03](03-workspace-ui.md).

**E1 — Integration vertical slice.** Registry transactions, registered Browser, one native UI response path, fixture modules, scoped credentials, disabled/unavailable states and independent lifecycles. See [04](04-extensions.md).

**F1 — Cross-boundary validation.** Failure/concurrency tests while B–E develop; retain replay/schedule/path/dialog/browser assertions. Test both deployment compositions and their actual boundaries, with repeated measurements. See [05](05-reliability-security.md).

**G1 — Two-build delivery.** Each release provides pixie-assistant for Docker and a single complete host pixie binary, both amd64/arm64, same revision/version. Prepare two alternative units, clean-install and mode-switch tests, schema-aware rollback and all-or-nothing release checks. Update runtime docs when code lands; separate validation from publication. See [06](06-documentation.md) and [07](07-build-release.md).

**H1 — Canvas.** Scoped authorization and enforced rendering boundary first, then transactional documents, six tools and a live registered viewer. Pass failure/network/deletion tests before enabling. See [08](08-canvas.md).

**I1 — Design structure; I2 — Design rendering.** Released public parser and bounded worker, persistent single-document slot, five read-only tools, UI/shared focus. Actual frame previews follow only after the renderer gate. A renderer blocker does not prevent completing structural inspection. See [09](09-openfig.md).

## Acceptance gates

| Gate | Required evidence |
| --- | --- |
| K1 — Contracts | FIX regressions, actual catalog, dispositions, selection invariants, module fixtures and shared-engine build composition |
| K2 — Vertical slices | Vanilla Pi conversation via standalone and embedded engine; Chat + File shell independently testable |
| K3 — Continuity | Native clone/fork/images/retry/compaction/queues; mid-run/tool/dialog reconnect; five layouts; no duplicate dispatch or stale activation |
| K4 — Optional integrations | Registered Browser, MCP absent without breaking chat, scoped native UI and optional native integrations |
| K5 — Core release-ready | Both variants × amd64/arm64, Docker integration, combined embedded UI, both units/install paths, mode-switch migration/rollback and validated docs/security |
| K6 — Canvas | Isolated rendering, same-session tools/view, CAS writes and real image bytes, quota/restart/delete races and core regressions |
| K7a — Design structure | Real .fig parse, offline bounded worker, retained slot, structure/text/shared focus, disabled removal and three-module regression |
| K7b — Design frames | Supported renderer, actual frame images, licensed offline assets/fonts, fidelity, bounded pixels/cache and cancellation |

K5 is not publication approval. Canvas starts after K5; Design implementation follows K6. Research and licensed fixture preparation may happen earlier. If K7b is blocked by an upstream API, record the exact capability and evidence, finish K7a/all other unblocked work, and leave K7b open. Do not omit either binary build from a release because it has no feature changes.

## Status ledger

Only this table records delivery status. Plans define requirements; the review records baseline findings. Set `in progress`, `blocked`, or `complete` with commit/test/evidence references. This documentation consolidation implements no runtime feature.

| Package | Initial status | Evidence or blocker |
| --- | --- | --- |
| FIX-01–04 / K1 | Not started | Source findings; regressions to execute |
| A / contracts | Not started | 01-contracts.md |
| B / assistant engine | Not started | 02-assistant-go.md |
| C+D / workspace | Not started | 03-workspace-ui.md |
| E / integrations | Not started | 04-extensions.md |
| F / reliability | Not started | 05-reliability-security.md |
| G / both builds, docs and delivery | Not started | 06-documentation.md and 07-build-release.md |
| H / Canvas | Pending K5 | 08-canvas.md |
| I1 / Design structure | Pending K6 | 09-openfig.md |
| I2 / Design frames | Pending renderer verification | Distinct from cover-thumbnail support |

## Approval boundaries

Implement/test in disposable environments. Ask separately before pushing implementation branches, merging, submitting upstream contributions, publishing tags/packages/images/releases, dispatching release workflows, restarting operator services, relocating/deleting native Pi state or rewriting history. A push approval must identify publication it triggers. Keep existing releases immutable. Never publish fallback development versions.

The commit installing this roadmap is the requested documentation operation, not blanket approval for later operations. Ordinary local coding/tests within an implementation request need not stop for repeated approvals.

## Completion report

Report changed paths/commits, implemented behavior, tests actually run/results, UI screenshots or executable/artifact evidence, unresolved limitations and migration impact. Report the two build/architecture results separately. Static review is not runtime testing. Do not delete regressions because the replacement fails them; explain intentional changes and use the agreed equivalent assertions.
