# Implementation roadmap

Start here when implementing Pixie's next version. This directory is the canonical implementation plan. It includes the repository review, shared contracts, Go assistant, workspace redesign, validation, documentation, both release builds, and the final Canvas and Openfig modules.

The target is a lightweight web workspace around the user's installed Pi. Pi owns agent execution and native state. Pixie owns presentation, navigation, read-only project inspection, schedules, and optional workspace tools. Mewa owns the shared visual foundation.

This is a plan, not a claim that the described behavior is already implemented. The reviewed source baseline is `f63d0d5bcb7f6e342058734b2e741ad2619d8867`. Reconcile findings with the checkout before changing code. Evidence and verification limits are in [repository-review.md](repository-review.md) and [sources.md](sources.md).

## Agent instruction

> Implement this roadmap. Read execution.md and the relevant numbered plans before editing. Begin with the reliability regressions and shared contracts, then work through the dependency graph in small, independently verifiable changes. Keep execution.md current with evidence. Continue through all unblocked work; do not stop after producing another plan. Deliver both release builds from the same source revision. Complete the core foundation before Canvas, and implement Openfig last. Do not publish, deploy, change live Pi state, or rewrite Git history without separate authorization.

An implementation request authorizes coding, local validation, and coherent local commits. It does not authorize remote publication. The publication boundary applies even when a push would trigger it indirectly. See [execution.md](execution.md#approval-boundaries).

## Required release builds

Every release, including prereleases, contains both build variants for each supported architecture:

| Build | Use | Runtime composition |
| --- | --- | --- |
| `pixie-assistant` | Host assistant used with the Dockerized Pixie interface | One Go assistant binary; the Docker controller connects to its authenticated host endpoint |
| `pixie` | Complete Pixie directly on the host | One Go binary containing the same assistant engine, controller and embedded web interface; one systemd user service, no separate assistant installation |

Both use the existing host Pi. Neither bundles a replacement Pi SDK/runtime. Optional Browser/Canvas/Design dependencies remain separately declared; their absence does not block core chat. Linux amd64 and arm64 initially mean four host archives per release, not one variant per architecture. A release is incomplete if either build variant is missing. [07-build-release.md](07-build-release.md) defines composition, assets, units and the verification matrix.

## Plans

| Stage | Plan | Outcome |
| --- | --- | --- |
| 01 | [Shared contracts](01-contracts.md) | Exact host operations, capabilities, workspace selections, module boundaries and migration fixtures |
| 02 | [Go assistant](02-assistant-go.md) | A reusable host supervisor using installed Pi through native RPC, shared by both binaries |
| 03 | [Workspace and Mewa](03-workspace-ui.md) | Six logical columns, independent selections and one visual foundation |
| 04 | [Extensions and Browser](04-extensions.md) | Native UI translation and a reusable workspace-module contract |
| 05 | [Reliability and security](05-reliability-security.md) | Regression fixes, recovery, bounded resources and measured deployment boundaries |
| 06 | [Documentation](06-documentation.md) | Short, factual documentation matching shipped behavior and both installation paths |
| 07 | [Build and release](07-build-release.md) | Assistant-only and all-in-one builds, systemd, migration, rollback and approved release path |
| 08 | [Canvas](08-canvas.md) | Optional session-scoped HTML drafts with isolated rendering and screenshot feedback |
| 09 | [Openfig / Design](09-openfig.md) | Optional offline .fig inspection, then actual frame previews behind an upstream renderer gate |

Stage numbers express integration order. Stages 02–07 can progress in parallel after their shared contracts are agreed. Reliability triage and documentation corrections can begin immediately. Stage 08 follows core acceptance; stage 09 is the final implementation item. Publishing the core release is not a prerequisite for continuing local module work.

## Ownership rules

The shared assistant engine launches the selected host `pi` executable. It does not install a second SDK, copy native configuration, implement another agent loop, or mandate an extension bundle. The standalone assistant and all-in-one application share implementation and contracts, not two competing session managers.

“Attach” means use an existing installation and reopen its native sessions. It does not mean take control of an arbitrary running TUI. Separate vanilla Pi processes do not coordinate writes to one session. Keep concurrent TUI/Web work in separate sessions or use an explicit idle handoff. Do not run the two release variants as writers to the same session at once.

Projects organize sessions; they are not required for session identity or listing. Native working directories remain authoritative. Admission of a directory for file/Git browsing is separate from discovering a session. Grouping and archive are Pixie metadata, not transcript rewrites.

Left selections belong to the primary view. Right selections belong to the secondary view. Hiding or unmounting a view does not stop a session. Closing, archiving, deleting and stopping are distinct operations.

Mewa owns shared appearance and control behavior. Pixie owns composition and feature state. Keep files/Git read-only. Do not introduce a terminal, IDE, automatic worktree manager, model-routing framework or arbitrary remote frontend-code loader.

Native Pi extensions, native MCP connections and Pixie workspace modules are separate systems. Vanilla chat requires none of the optional modules. Canvas and Design remain disabled by default and cannot become prerequisites for core startup.

## Completion

Use the gates and status ledger in [execution.md](execution.md). A feature is complete only with its required runtime, persistence, UI and compatibility evidence. Mocks are useful during development but do not establish native compatibility. An embedded .fig thumbnail does not complete frame rendering; CSP alone does not complete Canvas network isolation.

The old `docs/roadmap.md` and MCP draft paths point here. Do not maintain another competing roadmap under `docs/`.
