# Pixie implementation roadmap

Start here. This directory is the implementation handoff for the shared Go assistant, six-column Mewa workspace, extension foundation, release builds, and final Canvas and Openfig modules. It contains requirements, decisions, tasks and acceptance criteria; it does not claim that the target implementation already ships.

## Agent instruction

> Implement this roadmap, not another replacement plan. Read execution.md, contracts.md and compatibility.md before editing shared behavior. Follow the dependency gates and complete all unblocked tasks in small, tested changes. Keep the task ledger and evidence current. Build both host variants and the matching Docker image from the same source commit. Canvas is the penultimate feature and Openfig is last. Do not publish, change live services/native state, or rewrite history without separate authorization.

Use the explicit defaults below. Technical implementation choices within these boundaries need evidence, not a new product discussion at each step. A missing upstream API or unenforceable security boundary is a named blocker with an owner and exit test, never permission to remove a feature silently.

## Required outcome

Pi owns execution, native transcripts, providers, credentials, settings, models, tools and extensions. Pixie uses the selected host Pi installation rather than bundling another SDK or agent loop. Basic chat works without optional extensions. Projects organize sessions but are not required for native identity; discovering a session does not admit its working directory for file browsing.

The workspace has independent primary and secondary selections. Mewa owns shared visual foundations and interaction contracts; Pixie owns composition, routes and feature state. Files and Git remain read-only. Native Pi extensions, native MCP connections and Pixie workspace modules are separate systems.

Sharing an installation means reopening native sessions, not taking over an arbitrary running TUI. Independent vanilla Pi processes do not coordinate writes to one session. Use separate sessions or an explicit idle handoff. Both release variants share the same assistant implementation and ownership lock.

## Release identity and builds

Every release, including prereleases, uses `sha-<first 12 lowercase hexadecimal characters of the source commit>`. The full commit is recorded in its manifest and build metadata. The Git tag, release title, binary version, archive names and Docker tag use that same release ID. No semver release counter, workflow-run number or moving branch name substitutes for it.

Example identity, not a published release: `sha-d8f73c35ae5f`.

| Required artifact | Deployment |
| --- | --- |
| `pixie-assistant_<release-id>_linux_amd64.tar.gz` and `_arm64.tar.gz` | One host assistant binary/service; the Docker controller connects to its authenticated endpoint |
| `pixie_<release-id>_linux_amd64.tar.gz` and `_arm64.tar.gz` | One host binary/service containing the same assistant engine, controller and embedded web UI |
| `ghcr.io/miloszkolber/pixie:<release-id>` | Multi-platform Docker controller/interface image for linux/amd64 and linux/arm64; does not start Pi |

Publish a GitHub Release only after all four archives, both image platforms, metadata and required verification are ready. Build the release candidate before publication, not after a release is already visible. PR/main CI and scheduled checks do not publish releases. Existing releases remain immutable. [Build and publication contract](builds-and-releases.md).

Neither host build bundles Pi. Optional browser/parser/render dependencies remain explicit and do not prevent core startup. A full-host archive is not two separately managed executables and does not require a separate assistant service.

## Reading map

| File | Owns |
| --- | --- |
| [execution.md](execution.md) | Work packages, parallel ownership, dependency gates, progress and evidence |
| [contracts.md](contracts.md) | Protocol, identity, delivery, selection, capability and module contracts |
| [compatibility.md](compatibility.md) | Retained-feature mapping, native RPC gaps, bridge strategy and no-loss cutover gate |
| [assistant-go.md](assistant-go.md) | Native executable discovery, RPC, process supervision, catalog and projection |
| [workspace-ui.md](workspace-ui.md) | Six slots, all reference modes, routes, feature views, Mewa and responsiveness |
| [extensions.md](extensions.md) | Generic native UI mapping, module registry, Browser and scoped MCP |
| [security-and-validation.md](security-and-validation.md) | Runtime trust boundaries, worker containment, failure cases and measurement |
| [migration.md](migration.md) | Exact state ownership, staged conversion, deployment switching and rollback |
| [builds-and-releases.md](builds-and-releases.md) | Commit naming, artifacts, two compositions, units and publication workflow |
| [documentation.md](documentation.md) | Brief current-state documentation and removal of superseded plans |
| [acceptance.md](acceptance.md) | Requirement-to-test coverage and observable completion assertions |
| [repository-review.md](repository-review.md) | First-pass source findings and verification limits |
| [second-pass-review.md](second-pass-review.md) | Additional source findings, reconciliation and unresolved evidence |
| [draft-review.md](draft-review.md) | Reviewed Canvas/Openfig decisions and attributed prior experiments |
| [canvas.md](canvas.md) | Penultimate feature: session-scoped HTML drafts and isolated screenshot iteration |
| [openfig.md](openfig.md) | Last feature: offline Design inspection and separately verified frame rendering |
| [sources.md](sources.md) | Pinned baseline sources and external contracts |

The contracts and compatibility documents govern detailed behavior. Family summaries elsewhere are not permission to waive their acceptance tests. Build identity is defined only in builds-and-releases.md; migration only in migration.md; delivery status only in execution.md.

## Implementation order

1. Capture regressions and agree contracts. Resolve the administration bridge feasibility early; do not defer it until after deleting the SDK host.
2. In parallel, deliver a real vanilla Pi conversation through the shared Go engine, a real Chat + File workspace, the Mewa adapter inventory, and release-build composition.
3. Complete native lifecycle, retained administration/integrations, all primary/secondary areas, reconnect, migration and both deployment paths.
4. Pass core Gate 5: no unapproved feature loss, complete artifact matrix, truthful security boundaries and verified installation/rollback documentation.
5. Implement Canvas through the established registry and enforced worker boundary. Pass Gate 6.
6. Implement Openfig last. Verify structure and actual frame rendering as separate Gate 7 milestones.

Release publication approval is not a dependency of later local development. Research and licensed fixture preparation can occur earlier, but optional modules cannot delay basic chat or force a new assistant architecture.

## Scope and evidence

The runtime source baseline remains `f63d0d5bcb7f6e342058734b2e741ad2619d8867`. This directory reconciles the roadmap work on `d8f73c35ae5f` and `71590cac4892`; runtime implementation must recheck the checkout. Original inputs remain available through commit-pinned links in sources.md, not duplicate planning documents in docs/.

The second pass is a source/plan review, not a new successful runtime test run. The supplied empty-content screenshot still needs reproduction. Native bridge feasibility, worker enforcement and Openfig frame rendering have explicit blocking tests. Do not describe those as solved merely because their implementation paths are specified.
