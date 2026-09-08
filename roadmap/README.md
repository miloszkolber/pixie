# Pixie implementation roadmap

Start here, then read [execution.md](execution.md), [contracts.md](contracts.md), [feature-coverage.md](feature-coverage.md) and [acceptance.md](acceptance.md). Read the assigned feature plan before implementation. The wireframe behavior is described in the workspace plan; conversation attachments are not required.

Implement the work, not another proposal. Preserve retained behavior, use the stated defaults, add regressions and record evidence. Continue independent work when an exact external dependency is blocked; do not silently reduce scope or call a blocked capability complete.

These are target requirements, not shipped functionality. Recheck the implementation checkout and concurrent changes. All repository findings and Canvas/Openfig draft observations are consolidated in [repository-review.md](repository-review.md), with pinned evidence in [sources.md](sources.md). Only execution.md records implementation status; contracts and feature plans contain the current requirements. The reconciled roadmap on main supersedes the separate planning branches.

## Product and ownership

Pixie is a lightweight web workspace around the user's installed Pi. Pi owns execution, transcripts, providers, credentials, models, settings, trust, tools, extensions, retry and compaction. Pixie owns views, navigation, read-only inspection, grouping/archive, schedules and optional modules. Basic chat needs no optional extension.

The shared Go assistant launches selected native Pi, not a private SDK or replacement agent loop. It does not supply model-routing policy, new prompts, tool interception or a second native MCP client. An explicitly enabled generic bridge may use public APIs from that installation for retained administration. A disabled Web UI control and TUI instructions are not feature parity; coverage gates govern legacy retirement.

Sharing an installation is not attachment to an arbitrary running TUI. Use separate sessions or an explicit idle handoff that terminates the managed writer. A Pixie lock does not coordinate unrelated native processes. Projects are optional organization; native cwd and file/Git admission are separate. Removing a project does not delete conversations. Files/Git stay non-mutating inspection, without repository-configured helper execution, an IDE or a terminal.

## Every release

One immutable identity, `sha-<first 12 lowercase hex characters of the source commit>`, names the Git tag, GitHub Release, archive version, binary build and Docker tag. Full source SHA and digests remain authoritative. Do not use semantic-version ordering or workflow counters for these builds.

| Build | Deployment |
| --- | --- |
| pixie-assistant | One host Go assistant service used by the Docker controller/interface |
| pixie | One host Go binary/service containing the same assistant engine, controller, registry and embedded UI; no separate assistant installation |

Both host variants ship for Linux amd64 and arm64. The matching `ghcr.io/miloszkolber/pixie:sha-<12>` is a multi-architecture controller-only image that never starts Pi. A release requires all four host archives and both image platforms from the same commit. Optional Browser/Canvas/Design workers are declared separately and never prerequisites for core chat. The full-host archive is not two services hidden behind an installer.

[Builds and releases](builds-and-releases.md) owns exact names, continuous-release approval, candidate tests, immutable retries, provenance, services and mode switching. This documentation merge does not alter or enable publication workflows. Publication approval does not imply live deployment approval.

## Plan ownership

| File | Responsibility |
| --- | --- |
| [execution.md](execution.md) | Task IDs, parallel owners, dependencies, status and evidence |
| [contracts.md](contracts.md) | Identity/authority, transport, persistence/lifecycle, configuration and bounds |
| [feature-coverage.md](feature-coverage.md) | FC01–FC33, native/bridge routes and no-loss cutover |
| [acceptance.md](acceptance.md) | Cross-boundary tests mapped to feature rows and tasks |
| [migration.md](migration.md) | Detailed state inventory, staged conversion, topology switching and rollback under the shared contracts |
| [assistant-go.md](assistant-go.md) | Native discovery/RPC, shared engine, supervision and bridge integration |
| [workspace-ui.md](workspace-ui.md) | Six slots, selections/views, Mewa, responsiveness and interactions |
| [extensions.md](extensions.md) | Native UI translation, registered HTTP/UI contributions and module lifecycle |
| [security-and-validation.md](security-and-validation.md) | Threat model, layered validation and performance methodology |
| [builds-and-releases.md](builds-and-releases.md) | Commit-named artifacts/Docker, both compositions, services and publication |
| [documentation.md](documentation.md) | Brief accurate operating docs and example validation |
| [repository-review.md](repository-review.md) | Single consolidated finding/draft-review record and evidence limits |
| [sources.md](sources.md) | Pinned source references, not a separate review or task list |
| [CHANGELOG.md](CHANGELOG.md) | Completed roadmap items and their implementation evidence |
| [canvas.md](canvas.md) | Penultimate feature: same-session HTML drafting and isolated screenshot iteration |
| [openfig.md](openfig.md) | Final feature: offline Design inspection and separate actual frame rendering |

Do not recreate removed planning files in docs/, separate review-pass files or parallel compatibility matrices. Current operating documentation remains under docs/ and changes when behavior ships. Shared contracts govern detailed plans; the migration procedure and review do not introduce alternative defaults.

## Six-column workspace

| Column | Content |
| --- | --- |
| 1 | Primary rail: Chats, Archive, Schedules, Settings |
| 2 | Sidebar for the primary area |
| 3 | Selected primary item: conversation, schedule or settings section |
| 4 | Selected secondary item: file, diff, Browser or module viewer; no track without selection |
| 5 | Sidebar for the secondary area: details/tree/module controls |
| 6 | Secondary rail: Details, Files, Git and registered modules |

The two selections are independent. Sidebars collapse independently; focus/hide preserves drafts and runtime. Close, Stop, Archive, Delete, Release idle runtime and Release to TUI are distinct. Implement split, secondary focus, primary with right context, primary with left sidebar and primary focus. Narrow screens keep the model but show one usable content surface. Mewa owns foundations/control semantics; Pixie owns composition and state.

## Integration order

1. Capture regressions and contract fixtures, including authority, non-executing Git, persistence outcomes and aggregate limits. Establish bridge feasibility early.
2. Build a real vanilla Pi conversation through the shared Go engine and a real Chat + File shell in parallel with Mewa, module and build composition work.
3. Complete native continuity, retained administration/integrations, all primary/secondary areas, registered Browser and state migration.
4. Pass core Gate 5 with both real host variants, both Docker platforms, supported profiles, systemd/mode switching/rollback, browser-upgrade recovery and accurate docs. Only then retire replaced runtime/UI code.
5. Add Canvas through the established routing, scope and contained-worker contracts. Pass its actual authority/rendering/storage tests.
6. Add Openfig last. Structure and saved cover are separate from supported offline frame rendering; neither substitutes for the other.

Fully tested unpublished core artifacts satisfy the engineering dependency for Canvas. Public API gaps and containment failures block their affected profile/milestone, not independent local work. No blanket Go-parity reduction is approved.

## Agent instruction

> Implement roadmap/README.md using execution.md, contracts.md, feature-coverage.md, acceptance.md and the assigned plans. Work through dependencies and record actual verification. Preserve installed Pi, native state and retained features. Publish both host variants and the Docker image under the same commit release ID only under the authorized release policy. Canvas and Openfig are the last feature stages.
