# Pixie implementation roadmap

Start with this file, [execution.md](execution.md), [contracts.md](contracts.md) and [feature-coverage.md](feature-coverage.md). Then read the assigned implementation plan. This directory is sufficient task input; the agent does not need the conversation or its attachments to recover requirements.

Implement the work, not another proposal. Use the specified defaults, preserve retained behavior, add regression tests, and keep the execution ledger current. Continue independent work when an external dependency is blocked. Do not silently reduce scope or mark a blocked capability complete.

These are target requirements, not claims of shipped functionality. The second-pass baseline is `71590cac48925b31b9d5d3c7d1746ee94b351772`; its runtime sources match the first review's `f63d0d5bcb7f6e342058734b2e741ad2619d8867`. Reconcile with the checkout before editing. Read the [first review](repository-review.md), [second pass](second-pass-review.md), [draft review](draft-review.md) and [source appendix](sources.md) as evidence, not additional competing task lists.

## Product

Pixie is a lightweight web workspace around an existing Pi installation. Pi owns execution, transcripts, providers, credentials, models, native settings, tools, extensions, retry and compaction. Pixie owns presentation, navigation, read-only project inspection, archive/grouping, schedules and optional workspace modules. Basic chat requires no optional native extension.

The Go assistant launches the selected host Pi. It does not install a second Pi SDK, copy native configuration, supply another agent loop, replace prompts or implement another MCP client. An optional native administration bridge may use that installation's public APIs; its exact boundary and coverage gates are in [feature-coverage.md](feature-coverage.md).

Sharing an installation is not live attachment to an arbitrary running TUI. Separate processes must not write one session concurrently. Use different sessions, or explicit idle handoff that terminates the managed owner before native resume. A Pixie-only lock cannot coordinate an unrelated TUI.

Projects organize sessions; they are not mandatory session identities. Native cwd, grouping and filesystem admission are different fields. Removing a project does not delete native conversations. Files and Git stay read-only; no editor, terminal, automatic worktrees or model-routing framework.

## Every release

Each release uses one immutable source-commit identity: `sha-<first 12 lowercase hex characters of the full commit SHA>`. That exact value names the Git tag, GitHub Release and Docker image tag. The full 40-character revision remains authoritative in the manifest, binary metadata and OCI labels. Do not introduce a separate semantic version or run-number counter.

For example, a release built from `71590cac48925b31b9d5d3c7d1746ee94b351772` would use `sha-71590cac4892`. This is an example, not a release created by this documentation change.

| Required build | Deployment |
| --- | --- |
| `pixie-assistant` | One Go host assistant binary/service used by the Dockerized Pixie controller/interface |
| `pixie` | One Go host binary/service containing the same assistant engine, controller, module registry and embedded web UI; no separate assistant installation |

Both variants ship for Linux amd64 and arm64: four host archives per release. The matching Docker multi-architecture image uses `ghcr.io/miloszkolber/pixie:sha-<12>` and explicitly runs controller-only mode. Neither Pi nor an assistant is started inside that container. A release is incomplete without both binary variants and the matching Docker image.

Optional Browser/Canvas/Design workers are separately declared dependencies, not requirements for basic chat. The full build is not two executables hidden in one archive. Read [builds-and-releases.md](builds-and-releases.md) for exact artifacts, triggers, immutable retries, provenance, systemd and mode switching.

## Plan ownership

| Document | Owns |
| --- | --- |
| [execution.md](execution.md) | Task IDs, dependencies, parallel ownership, gates and evidence |
| [contracts.md](contracts.md) | Exact identities, protocol semantics, lifecycle, migration rules and initial bounds |
| [feature-coverage.md](feature-coverage.md) | Every retained feature, native/bridge implementation route, limitations and cutover evidence |
| [assistant-go.md](assistant-go.md) | Native discovery/RPC, reusable Go engine, supervision and host service |
| [workspace-ui.md](workspace-ui.md) | Six-slot composition, Mewa, views, responsiveness and interactions |
| [extensions.md](extensions.md) | Native UI mapping, workspace contributions, Browser and scoped module authority |
| [security-and-validation.md](security-and-validation.md) | Threats, worker enforcement and layered runtime/artifact tests |
| [builds-and-releases.md](builds-and-releases.md) | Commit-named releases, both builds, Docker tags, installation and rollback |
| [documentation.md](documentation.md) | Short factual current-state documentation and example validation |
| [canvas.md](canvas.md) | Penultimate feature: session-scoped HTML drafts and isolated screenshot feedback |
| [openfig.md](openfig.md) | Final feature: offline Design inspection and separately verified frame previews |

Each fact has one owner. Contracts own numeric defaults and state transitions; feature coverage owns migration completeness; the build plan owns release naming/publication. Feature plans compose those contracts rather than inventing alternative values or fallback policies.

## Workspace

| Column | Responsibility |
| --- | --- |
| 1 | Primary rail: Chats, Archive, Schedules, Settings |
| 2 | Sidebar for the selected primary area |
| 3 | Selected left-side item: conversation, schedule or settings section |
| 4 | Selected right-side item: file, diff, Browser or optional module viewer |
| 5 | Sidebar for the selected secondary area |
| 6 | Secondary rail: session details, Files, Git and optional module contributions |

Primary and secondary selections are independent. Column 4 disappears when there is no selection. Sidebars collapse independently. Secondary focus preserves the hidden conversation, draft and runtime. Hide, Close, Archive, Delete and Stop are different operations.

Implement the five reference modes: split; secondary focus; primary with right context; primary with left sidebar; primary focus. On narrow screens retain the same selection model but show one usable content surface. Mewa owns shared appearance and control semantics; Pixie owns layout and domain state.

## Integration order

1. Fix confirmed reliability defects and capture legacy fixtures. Establish contracts, feature dispositions and shared composition boundaries.
2. In parallel, implement one real vanilla Pi conversation through Go and one content-filled Chat + File split through the new shell.
3. Complete native lifecycle/recovery, retained feature coverage, Mewa, Archive/Schedules/Settings and registered Browser.
4. Validate both binary variants and Docker on amd64/arm64, migrations, systemd, security and documentation. Remove legacy implementation only after core cutover gates pass.
5. Implement Canvas after core acceptance, including real session authority and offline worker enforcement.
6. Implement Openfig last. Verify structure independently from actual frame rendering. A saved cover image does not complete frame previews.

Unpublished but fully tested core artifacts satisfy the engineering dependency for Canvas; waiting for publication approval does not block local module implementation. Mandatory runtime/API gaps block their affected cutover milestone, not unrelated work.

## Approval and completion

The implementation request authorizes local coding, tests and coherent local commits. Remote writes, enabling publication workflows, upstream submissions and changes to live services/native state require their respective authorization. The approved future continuous-release workflow can publish automatically under its recorded policy; this documentation commit does not enable it or authorize a live deployment.

A feature is complete only when its coverage row and execution gate include actual evidence. Tests to be written are not passing tests. Report an exact external dependency when necessary; do not manufacture certainty about an unavailable upstream API.

Suggested agent instruction:

> Implement roadmap/README.md. Follow execution.md, contracts.md and feature-coverage.md; continue through all unblocked tasks and record verification. Preserve installed Pi and retained features. Publish both binary variants and the Docker image under the same source-commit release ID when publication is authorized. Canvas and Openfig are the last feature phases.
