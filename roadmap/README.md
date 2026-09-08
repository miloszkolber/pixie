# Pixie implementation roadmap

Start here. This directory is the coding-agent handoff for the Go assistant, workspace rebuild, two release builds, documentation cleanup, and final Canvas/Openfig modules. Read [execution.md](execution.md) before editing code, then the assigned plan.

The review baseline is `f63d0d5bcb7f6e342058734b2e741ad2619d8867`; recheck against the checkout. These are implementation directions, not claims of shipped functionality. For this task they supersede planning directions in `docs/roadmap.md`, `docs/mcp-draft-canvas.md` and `docs/mcp-draft-openfig.md`. The documentation work replaces those with links here; pinned originals remain in [sources.md](sources.md).

## Product and ownership

Pixie is a lightweight web workspace for an existing Pi installation. Pi owns the agent workflow. Pixie owns navigation, conversation views, read-only project inspection, schedules and optional workspace tools.

The assistant launches the selected host Pi executable. It does not install a second SDK, copy native configuration, prescribe extensions or implement another agent loop. Basic chat needs no optional extension. Native credentials/models/settings/tools/retries/compaction/branches remain Pi-owned.

“Attach” means use that installation and reopen its sessions, not take control of an arbitrary running TUI process. Simultaneous TUI/Web work uses separate sessions unless Pi provides a tested shared-process mechanism. A Pixie-only lock cannot coordinate an unrelated TUI.

Projects organize sessions but are not mandatory session identities. Grouping/archive metadata must not move transcripts or change cwd. File browsing requires an admitted root independently of session discovery. Files/Git remain inspection tools, not an IDE.

The shell has independent left/right selections. Mewa owns shared UI foundations; Pixie owns composition. Native Pi extensions, native MCP integration and workspace modules are separate systems.

## Required release builds

Every release includes both variants on Linux amd64 and arm64:

| Build | Deployment |
| --- | --- |
| `pixie-assistant` | Assistant-only host service, used by a Docker Pixie controller/interface |
| `pixie` | One host binary/service containing the assistant, controller and embedded Pixie interface; no separate assistant installation or Docker required |

Both use the same assistant source and installed Pi. The full build is not two separately managed binaries in an archive. The Docker controller explicitly runs without starting a local assistant. Pi's own runtime and optional isolated module workers remain separate documented dependencies.

The four required archives share one release version/revision and must all pass before release promotion. See [builds-and-releases.md](builds-and-releases.md) for composition, services, build targets, artifact matrix and migration.

## Reading map

| File | Purpose |
| --- | --- |
| [execution.md](execution.md) | Task ledger, dependencies, parallel ownership and completion evidence |
| [repository-review.md](repository-review.md) | Eighteen source findings and verification limits |
| [assistant-go.md](assistant-go.md) | Native discovery/RPC, supervision, catalog, compatibility and service lifecycle |
| [workspace-ui.md](workspace-ui.md) | Six-slot shell, state, views, Mewa and responsive acceptance |
| [extensions.md](extensions.md) | Native UI translation, module contract, Browser and scoped MCP authority |
| [builds-and-releases.md](builds-and-releases.md) | Required assistant-only/full-host builds and release gates |
| [security-and-validation.md](security-and-validation.md) | Failure tests, real deployment boundaries, artifacts and performance |
| [documentation.md](documentation.md) | File responsibilities, concrete corrections and editorial rules |
| [draft-review.md](draft-review.md) | Canvas/Openfig draft decisions, corrections and evidence |
| [canvas.md](canvas.md) | Penultimate feature: session-scoped HTML drafting and screenshots |
| [openfig.md](openfig.md) | Last feature: local .fig inspection and separate frame-preview milestone |
| [sources.md](sources.md) | Pinned references, external contracts and evidence qualifications |

## 1. Repair reliability defects

Fix production assistant restart, transactional/idempotent MCP enablement, duplicate-create admission and strict host request validation. Add a reproduction for the supplied selected-chat/project-empty screenshot; do not assume its cause from the image.

Preserve bounded replay, connection generations, pending dialogs, read-only filesystem checks and uncertain-delivery behavior. Write failing regressions before fixes and carry executable restart/transport fixtures into the Go runtime.

**Exit:** confirmed defects have tests/fixes. The screenshot scenario is reproduced/fixed or explicitly isolated as unresolved, not silently marked solved. See F02–F05/F07 in the [review](repository-review.md).

## 2. Establish shared contracts

Inventory actual browser/controller/assistant/native methods. Define strict envelopes and one schema owner in `package/contracts`, with generated bindings where useful. Keep native request IDs, session/entry IDs, delivery IDs, boot epochs and selection generations distinct.

Agree on host capabilities, primary/secondary selections, module contributions and two-build composition before parallel implementation. Capabilities describe complete usable operation sets in context, not package presence. Missing administration/MCP must not make core chat incompatible.

Every retained feature needs a supported native API, optional bridge, explicit TUI fallback or approved deferral. Do not remove difficult features as cleanup. One owner handles each delivery stage.

**Exit:** common protocol fixtures, fake-transport shell, sidebar-only/viewer-only module fixtures, explicit unsupported behavior and shared assistant facade exist as agreed contracts. See [execution](execution.md) and [assistant compatibility](assistant-go.md#8-capability-disposition-and-compatibility).

## 3. Implement the shared Go assistant

Use `assistant/` as a separate Go module and retain the application module under `package/`. The standalone entrypoint and full-host entrypoint use one narrow public assistant facade; internals remain independent of controller/UI. Do not copy RPC/supervisor code or combine this with a repository-wide move.

Resolve the operator's Pi executable, run native JSONL RPC under the right environment/cwd, supervise children and adapt events. Preserve native files/unknown records and use native operations for reopen/rename/model/thinking/clone/fork/compaction. A draft without a persisted native identity remains a draft.

Separate acceptance from settlement and do not resend uncertain work. Keep accepted runs alive when views disappear; bound shutdown/residence/output; restore valid partials/dialogs by process epoch. Native SDK administration gaps require tested dispositions, not a new Go provider system.

**Exit:** vanilla/configured independent Pi installs pass outside the checkout, optional packages can be absent, native identity and rollback survive, and both executable compositions use the same runtime. See [assistant-go.md](assistant-go.md).

## 4. Build the six-column workspace

| Column | Responsibility |
| --- | --- |
| 1 | Primary rail: Chats, Archive, Schedules, Settings |
| 2 | Sidebar for the primary area |
| 3 | Selected left-side item: session, schedule or settings section |
| 4 | Selected right-side item: file, diff, Browser or module viewer |
| 5 | Sidebar for the secondary area |
| 6 | Secondary rail: details, files, Git and optional modules |

Column 4 is absent without a right selection. Sidebars collapse independently. Secondary focus hides primary content temporarily while preserving selection/draft. Close, Hide, Archive, Delete and Stop are distinct actions.

Replace mixed chat/file/Browser tabs with the two-selection model. Chats has grouped/flat catalogs and ungrouped sessions; Archive is not closed-tab history. Schedules and Settings have sidebar/detail views. Resolve session/project/instance context explicitly.

Keep the shell available during Pi/provider failure. A selection resolves to content/loading/unavailable/missing/error, never an unrelated empty screen. Label stale readable data and disable authority-dependent actions until revalidated.

**Exit:** split, secondary focus, primary with context, primary with sidebar and primary focus all work with actual content, back/forward, independent scrolling, focus restoration, mobile navigation and stale-response rejection. See [workspace-ui.md](workspace-ui.md).

## 5. Make Mewa the visual foundation

Retain the pinned release/integrity checks and correct adapters. Mewa owns shared colors, typography, spacing primitives, geometry, borders, appearance and interaction contracts; Pixie owns shell dimensions and feature composition.

Migrate independent Pixie generators through a temporary mapping, then remove obsolete generators/imports/tests together. Useful layout utilities can remain; framework removal is not another prerequisite.

Use square continuous surfaces and structural borders. Follow Geist/Geist Mono roles rather than uppercase monospace everywhere. Reserve color for status; avoid nested decorative cards and repeated chrome.

**Exit:** content-filled light/dark fixtures pass keyboard/zoom/overflow/focus/lifecycle checks, with one owner for every visual foundation. See [Mewa integration](workspace-ui.md#6-mewa-integration).

## 6. Complete the extension foundation

Translate supported native dialogs, notifications, text widgets, status/title and editor-text requests. Preserve ownership/deadlines, single settlement and draft safety. Report terminal-only component limits honestly.

Register Browser first. Controls go in slot 5 and view in slot 4, reusing leases, quotas, artifact mediation/cancellation and existing transport. Duplicate enable is a no-op; Restart is explicit.

Generalize the backend once for Browser, Canvas and Design. Use a small compile-time contribution boundary, not remote code discovery. A generic MCP connection is not automatically a rich renderer. Canvas requires authenticated session scope, not a tool-supplied session ID; Design explicitly uses broader instance scope.

**Exit:** vanilla chat works without MCP, UI/tool availability is distinct, fixture modules need no shell branches, failures stay local and scoped authority is tested. See [extensions.md](extensions.md).

## 7. Deliver both builds and validate deployment

Build assistant-only and full-host variants from the exact same revision on amd64/arm64. Assistant-only serves the Docker instance over authenticated loopback. Full-host mode embeds assistant/controller/UI in one executable/user service, with no separate assistant or asset installation.

Give the Docker controller an explicit controller-only entrypoint so it never launches a second Pi owner. Both host variants share lock/identity, detect conflicting ownership and preserve native files during upgrades or mode switches. In-process libraries return lifecycle signals; entrypoints own complete shutdown/restart.

Test final archives, private configuration, diagnostics, units, descendant cleanup, upgrade/rollback and mode switching. Separate validation from publication. Publish neither a missing-variant release nor development fallback versions.

Verify the real Browser/worker boundary in both deployments. Same UID, environment filtering, host networking and read-only roots are not isolation. Canvas/Design workers stay optional but their promised containment is mandatory before use. Measure assistant plus Pi children, not supervisor RSS alone.

**Exit:** all four required archives pass clean installation and the two-mode test matrix; docs match behavior; exact-source publication awaits separate approval. Artifact readiness does not block later local feature work while that approval is pending. See [builds-and-releases.md](builds-and-releases.md) and [security-and-validation.md](security-and-validation.md).

## 8. Rewrite documentation

Keep one home per fact. Remove operator paths/backups, one-off timings, release history, completed operations, obsolete source paths and false mount/isolation claims from user docs. Retain constraints needed for correct operation.

Update root agent guidance early for actual paths, shared Go composition, test placement and this roadmap. Convert the old roadmap/drafts to concise links when the documentation task lands. Explain the two build choices directly. Future behavior becomes present tense only when shipped.

**Exit:** examples run in disposable environments, links resolve, method docs match contracts, both install paths are tested and normal user documentation stays brief/factual. See [documentation.md](documentation.md).

## 9. Add Canvas

After core Gate 5, add `canvas` / `pixie-canvas` at `/mcp/canvas`, disabled by default. One canvas per native chat, full-document optimistic revisions, retry identity, durable deletion, bounded storage and module-owned rendering sessions.

Prove session authority and actual offline renderer isolation. The draft's iframe/CSP-only scheme is insufficient for that promise. Use version-aware safe live previews and actual bounded screenshot content without borrowing Browser sessions or exposing credentials. [Draft corrections](draft-review.md).

**Exit:** create/write/stale-write rejection/read/screenshot/iterate/remove works for the same draft the human sees. Cross-session, egress, stale render and restart resurrection tests pass in both deployments. See [canvas.md](canvas.md).

## 10. Add Openfig Design inspection

The final feature is `design` / `pixie-design` at `/mcp/design`, disabled by default. Retain the explicitly labelled instance-wide single-document scope, not inferred project privacy.

Use a bounded optional application-side worker with a pinned public `openfig-core` API. Persist original .fig until removal. Deliver pages/layers/frame candidates/direct text, a valid saved cover, bounded MCP queries and explicit shared focus without Figma or a live browser tab.

Frame previews are independent: recheck released public upstream APIs, offline fonts/assets, rights and fidelity. A cover is not a frame render. Structure remains usable while a verified renderer API gap blocks only that milestone.

**Exit:** structure and its failure/authority matrix pass in both deployments; real frame previews are independently verified or explicitly blocked, never implied complete. See [openfig.md](openfig.md).

## Completion

Update [execution.md](execution.md) as tasks land with implemented behavior, changed contracts, migrations, tests actually run, UI/process/artifact evidence and limitations. Do not mark the roadmap complete while mandatory gates or an explicitly required milestone remain unresolved.

Suggested agent instruction:

> Implement roadmap/README.md using roadmap/execution.md. Follow the dependency gates and parallel ownership, preserve native Pi state, and record completion evidence. Every release must contain assistant-only and full-host builds. Canvas and Openfig are the last feature phases. Do not publish releases or change the live deployment without approval.
