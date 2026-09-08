# Workspace UI implementation plan

The five supplied wireframes define desktop layout modes, not six simultaneously usable panes at every screen width. Track UI and MEWA tasks in [execution.md](execution.md).

Current code gives chats/files/diffs/Browser one active content-tab slot and hard-codes Files/Changes on the right. Replace that ownership model rather than reskinning it. Preserve useful chat rendering, recovery safeguards, read-only viewers, and tested interactions. [Source baseline](sources.md#workspace-and-mewa).

## 1. Six stable slots

```text
1              2                 3                4                 5                6
Primary rail | Primary sidebar | Primary view | Secondary view | Secondary sidebar | Secondary rail
```

Left and right select independently. Selection never moves across the center boundary.

| Slot | Owner | Content |
| --- | --- | --- |
| Primary rail | Shell | Chats, Archive, Schedules; Settings at bottom |
| Primary sidebar | Active primary area | Grouped/flat sessions, archive, schedules, settings sections |
| Primary view | Left selection | Conversation, schedule details, settings section |
| Secondary view | Right selection | File, diff, Browser, Canvas or Design viewer |
| Secondary sidebar | Active secondary area | Session details, file/Git tree, module controls/inspector |
| Secondary rail | Shell and module registry | Details, Files, Git and optional modules |

Align headers across content/sidebar columns. Avoid a global header repeating selected session/file/project labels. Put connection status in a compact persistent location with accessible diagnostics.

Column 3 retains primary ownership while hidden by secondary focus. Column 4 has no track or divider without a secondary selection. Selecting a right area can open column 5 without opening column 4.

## 2. Layout modes

| Mode | Visible desktop slots | Behavior |
| --- | --- | --- |
| Split | 1, 2, 3, 4, 5, 6 | Chat and selected secondary resource coexist |
| Secondary focus | 1, 2, 4, 5, 6 | Preserve primary selection/draft; Restore returns to split |
| Primary with context | 1, 2, 3, 5, 6 | Right sidebar without open preview |
| Primary with sidebar | 1, 2, 3, 6 | Right sidebar/preview hidden; rail available |
| Primary focus | 1, 3, 6 | Both sidebars hidden; selections preserved |

Left collapse, right collapse, and content focus are independent controls. Close clears a secondary selection; Hide preserves it. Opening a file never replaces chat. Hiding/unmounting a view never stops accepted work.

Clicking an inactive rail item selects its area and opens that sidebar. Clicking the active rail item toggles the sidebar without clearing content. Indicate active area even while collapsed.

Provide Close, Focus and Restore in panel headers, not only shortcuts. Escape handles the topmost dialog/transient interaction first and does not unexpectedly close work.

## 3. Selection and state

Use small shell state independent of agent runtime. Conceptual types, to reconcile with existing domain types:

```ts
type PrimaryArea = "chats" | "archive" | "schedules" | "settings";
type SecondaryArea = "details" | "files" | "git" | `module:${string}`;
type ResourceContext =
  | { scope: "instance"; instanceId: string }
  | { scope: "project"; projectId: string }
  | { scope: "session"; sessionId: string; projectId?: string };
type PrimarySelection =
  | { kind: "session"; sessionId: string; projectId?: string }
  | { kind: "schedule"; scheduleId: string; projectId: string }
  | { kind: "settings"; sectionId: string }
  | null;
type SecondarySelection =
  | { kind: "file"; projectId: string; resourceId: string }
  | { kind: "diff"; projectId: string; resourceId: string; reviewId: string }
  | { kind: "module"; moduleId: string; resourceId: string; context: ResourceContext }
  | null;
interface WorkspaceLayout {
  leftCollapsed: boolean;
  rightCollapsed: boolean;
  focus: "none" | "primary" | "secondary";
  leftWidth: number;
  rightWidth: number;
  primaryFraction: number;
}
```

Resource IDs are opaque validated references, not arbitrary filesystem paths used as authorization. Declared context is selection data; the server must independently authorize it.

Keep canonical runtime, transcript, draft, queue and pending dialogs outside components. Navigation controls views/subscriptions, not session existence. A keyed Svelte unmount must not destroy a live session.

Persist layout preferences locally with schema/version/bounds. One URL driver owns primary/secondary selection and back/forward. Do not persist secrets, full transcripts or pending-response credentials in layout state or URLs. Validate restored IDs.

### Selection invariants

A selection resolves to content, loading, unavailable, missing or failed, never accidental no-selection. Async work has an owner/generation; stale responses cannot activate old resources.

Project changes clear incompatible secondary items. A file can remain selected across sessions in the same project; session details must follow the new session. Modules explicitly declare instance/project/session scope. Entering global Settings initially hides incompatible old session inspectors. Returning to Chats restores the previous valid session. Instance-wide Design still declares its scope rather than borrowing an old session context.

A missing item gets a recovery view in its own column; do not redirect to the first project or replace a newer selection. Removing a project does not implicitly delete native sessions.

### Existing-state migration

Translate valid old chat selections into primary selections and valid file/diff/Browser selections into secondary selections. Restore only compatible contexts. Ignore invalid/retired tab IDs safely while preserving drafts and runtime. A bounded migration is preferable to retaining the generic mixed tab strip.

## 4. Primary areas

### Chats

Grouped and flat views use one catalog. Include sessions without named projects: cwd and project membership are different things. Projects expand independently with a concise recent subset and Show more; do not hide the selected session behind the subset. Running state, timestamps and truncation need accessible text, not color alone.

New session is one guarded request per action. On success select the returned identity; on failure preserve navigation/input context. Use native titles and an explicit unresolved fallback, not a second model call to generate titles. Legitimately duplicate titles are allowed and distinguishable by context/activity.

### Archive

Archive is explicit Pixie metadata, separate from closing a view or deleting a transcript. Show archived sessions through the same catalog. Restore does not clone them. Use existing lifecycle controls with clear destructive confirmation and running-session policy.

### Schedules

Use a schedule list/filter in column 2 and selected prompt, timing/timezone, next occurrence, last outcome, run ledger and explicit pause/run/stop in column 3. Reuse backend CRUD and uncertainty/idempotency behavior.

Definitions and run sessions are separate identities. Open a run by navigating to its native session, not by changing schedule identity. No second scheduler in assistant or browser; schedules remain project-scoped unless separately redesigned.

### Settings

Settings is a primary area, not a giant modal. Inventory existing sections and group by ownership: Pixie appearance/workspace, Pi connection/native resources, supported providers, modules and diagnostics. Do not invent speculative pages.

Distinguish configured, supported, connected and available. A missing provider disables model work, not navigation/settings. Unsupported native administration has an explicit fallback rather than an enabled dead control. Dialogs remain appropriate for confirmation, pickers and native extension interactions.

## 5. Secondary areas

Session details can be sidebar-only: identity, model/thinking, usage/context, status and supported extension metadata. Unknown usage is not zero.

Files uses the admitted-root tree in column 5 and retained read-only viewers in column 4. Preserve type/size limits, binary/image handling, safe Markdown and root revalidation. No editor, terminal or implicit writes.

Git uses column 5 for repository/review scope and changed files, column 4 for diff. A project can have zero, one or several repositories. Resolve repository/review scope explicitly and keep Git observational.

Browser uses column 5 for controls/status and column 4 for its current view. Preserve the existing rendering transport and limitations; do not assume an iframe can browse every site.

Canvas later contributes session-scoped revision/status controls and its selected preview. Design later contributes instance-wide document/page/layer/shared-focus controls and selected-node/preview content. Neither changes shell routing or becomes a dependency of core views. Their detailed contracts are in [canvas.md](canvas.md) and [openfig.md](openfig.md).

Start with one selected secondary item. Switching files replaces that preview. Do not rebuild a second generic multi-tab workbench unless a demonstrated need justifies it.

## 6. Mewa integration

Pixie's vendor lock pins Mewa 0.1.2 and asset hashes. Retain this reproducibility. Audit the installed release's component documentation and executable contract, not merely current upstream docs. Any library upgrade is a separate tested change. [Mewa references](sources.md#workspace-and-mewa).

### Foundation ownership

Mewa owns semantic colors, typography, borders, shapes and spacing primitives. Pixie owns shell widths, panel relationships and feature layout. Migrate independent `tr-*` typography and color/spacing generators through an explicit temporary adapter map. Remove retired generators, imports and usage tests together after migration.

Retain useful Tailwind layout utilities without another visual system. Framework removal is not a prerequisite. Establish tested cascade/layer ownership; avoid increasingly specific overrides and `!important` fights.

Target square, continuous, border-defined UI. Follow Geist/Geist Mono roles; terminal-like does not mean all body text is monospace. Reserve status color for status. Avoid nested cards, decorative shadows, duplicate borders and oversized full-width local actions.

Use actual pinned semantic roles such as background/surface/selection/border/text tokens, not familiar-looking invented APIs. Patterned outer margins in the reference are optional decoration, not extra columns or a reason to reduce useful space.

### Adapter layer

Inventory Button/Icon, navigation, trees, scroll regions, resizers, menus/popovers/dialogs, fields/toasts, messages/composer, tool cards and source/diff presentation. Retain wrappers already matching Mewa.

Adapters map Svelte props to documented markup/behavior; they do not redesign controls. Use scoped attachments, no auto-initialization over Svelte-owned regions, and one owner per ARIA/interactive state. Destroy controllers/listeners on unmount; isolate controller-generated DOM.

Use a development-only representative fixture page, not a second application or a design-system documentation project. Check selected/hover/focus/disabled/loading/error/empty states with actual content.

## 7. Geometry, resizing and responsive behavior

Initial targets to test with real content: roughly 48–52 px rails and aligned headers, 240–280 px sidebars, and about 360 px minimum for each content pane. Follow Mewa's 40 px default and 32 px compact interactive contracts where applicable; don't shrink controls to match a scaled image.

Two 48 px rails, two 240 px sidebars and two 360 px content panes need roughly 1,296 px before borders. This is a budget, not a universal breakpoint. Collapse sidebars before producing unreadable content; preserve explicit focus choices.

Use CSS Grid tracks and independent overflow regions with correct min-width/min-height constraints. The document must not scroll because a pane outgrows the viewport. Keep composer and headers anchored to their own regions.

Resizers need pointer/keyboard operation, bounds, reset and persistence. Automatic responsive collapse must not overwrite the user's saved preference; widening the viewport restores appropriate layout.

Narrow screens show one content surface, accessible navigation/sidebar drawers and a clear return from secondary content. Preserve the same selection model instead of a miniature desktop. Trap focus only for true modal drawers; restore on close and make hidden regions inert.

## 8. Scope-local availability and performance

Keep the shell mounted across connectivity changes. Stale readable content may remain with an explicit stale indication; disable authority-dependent actions until revalidated. Never label cached data current.

Give connection/module polling one shared owner. Prefer events; otherwise use bounded visibility-aware polling. Each mounted project view must not start an independent global status loop. Module failure must not replace the whole application.

Retain history paging/virtualization and scroll anchoring. Lazy-load large viewers/settings/modules. Avoid reparsing or re-highlighting the whole transcript on each delta. Measure navigation/layout separately from model latency.

## 9. Acceptance matrix

Use all five modes as named screenshot fixtures in light and dark with content, not only empty black columns.

1. Select chat, open file, stream, focus/restore file, close it. Preserve chat, draft, stream, selection and focus.
2. Toggle grouped/flat sessions, include ungrouped sessions, expand projects, archive/restore and distinguish close/archive/delete.
3. Select schedule/run, enter Settings/return, restore missing/deleted routes without unrelated redirects.
4. Switch project/session during slow file/history/module requests; stale completions cannot win.
5. Reconnect mid-message/tool/dialog with no duplicated prompt or transcript and correctly scoped native answers.
6. Test missing provider, missing extensions, disabled/unavailable Browser, malformed module response and independent shell/core availability.
7. Keyboard-only operation, screen-reader labels, reduced motion, 200% zoom, long names, narrow viewport, large history and repeated mount/unmount.

Assert selection invariants, geometry, independent scrolling, composer visibility, no page overflow, focus restoration, single lifecycle ownership and active-session continuity. Adapt existing production-asset acceptance tests rather than deleting their recovery coverage.

## 10. Delivery order

Land selections/reducers behind a temporary shell switch, then Mewa adapters/content-filled fixture, then Chats + Files. Integrate Git, Archive, Schedules, Settings and registered Browser. Responsive/focus modes land before legacy removal. Canvas and Design integrate only in the final roadmap phases.

One owner integrates shell/store changes. View and adapter streams work against agreed props and fake transport. Remove the switch, mixed-tab state, obsolete restore paths and CSS/tests only after the accepted scenarios pass.
