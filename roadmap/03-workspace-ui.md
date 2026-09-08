# 03 — Workspace and Mewa

Owners: C for shell/navigation/state; D for adapters and feature views. Build against the agreed fixture transport while the Go assistant progresses independently. Preserve working conversation rendering, recovery and bounded read-only viewers. [Reviewed UI and Mewa](sources.md#workspace-and-mewa).

## Six stable slots

```text
1              2                 3              4                5                   6
Primary rail | Primary sidebar | Primary view | Secondary view | Secondary sidebar | Secondary rail
```

| Slot | Owner and content |
| --- | --- |
| 1 | Shell: Chats, Archive, Schedules; Settings at the bottom |
| 2 | Primary area: grouped/flat sessions, archive, schedules, settings sections |
| 3 | Left selection: session, schedule details or settings section |
| 4 | Right selection: file, diff, Browser or module viewer; absent with no selection |
| 5 | Secondary area: session details, file tree, Git tree, module controls |
| 6 | Shell/registry: Details, Files, Git and available optional modules |

Left and right selections do not move across the center boundary. Each sidebar/content region owns its aligned header. Avoid a second global header repeating session/file/project names. Put connection state and its diagnostic action in compact persistent chrome.

Column 3 retains its identity while hidden. Column 4 has no track or divider with no selected resource. A secondary area can provide only column 5. Keep one selected secondary resource initially; selecting another file replaces the preview. Do not rebuild another generic tab workbench as default behavior.

## Reference modes

The supplied five wireframes are captured by these required desktop fixtures; no external screenshot attachment is needed to understand the behavior.

| Mode | Visible slots | Contract |
| --- | --- | --- |
| Split | 1, 2, 3, 4, 5, 6 | Conversation and selected secondary resource visible together |
| Secondary focus | 1, 2, 4, 5, 6 | Primary selection/draft retained; Restore returns to prior split |
| Primary with context | 1, 2, 3, 5, 6 | Useful right sidebar without preview |
| Primary with sidebar | 1, 2, 3, 6 | Right sidebar/view hidden; rail available |
| Primary focus | 1, 3, 6 | Both sidebars hidden; selections retained |

Sidebars collapse independently. Focus is temporary visibility, not replacement of selection. Close clears a secondary item; Hide preserves it. An inactive rail action selects its area and opens the sidebar; activating the current rail item toggles that sidebar. Preserve its active indicator while collapsed.

Expose Close, Focus and Restore in panel headers. Escape first handles the topmost transient interaction; it must not unexpectedly close work. Moving a pane out of view moves/restores focus deliberately and makes hidden content inert.

## State and routing

Agree conceptual types with A rather than copying these into a second domain model:

```ts
type PrimaryArea = 'chats' | 'archive' | 'schedules' | 'settings';
type SecondaryArea = 'details' | 'files' | 'git' | `module:${string}`;
type PrimarySelection =
  | { kind: 'session'; sessionId: string; projectId?: string }
  | { kind: 'schedule'; scheduleId: string; projectId: string }
  | { kind: 'settings'; sectionId: string }
  | null;
type SecondarySelection =
  | { kind: 'file'; projectId: string; resourceId: string }
  | { kind: 'diff'; projectId: string; resourceId: string; reviewId: string }
  | { kind: 'module'; moduleId: string; resourceId: string; contextId: string }
  | null;
```

Layout state includes left/right collapse, focus mode, sidebar widths and split fraction. Keep responsive auto-collapse separate from saved user preferences. Resource/context IDs must be validated server-side, not used as permission by themselves.

Canonical session runtime, transcript, drafts, queues and pending native dialogs live outside component lifetimes. Keyed Svelte unmount is not session shutdown. One reducer owns selection invariants and one URL driver owns reload/back/forward. Store only versioned bounded preferences and validated identities, not secrets/transcripts/native response tokens, in layout persistence or shareable URLs.

A selected item is content, loading, missing, unavailable or error, never accidental no-selection. Late responses cannot activate an older selection. Track operation owner and generation. A missing route gets its own recovery view, not a redirect to the first project.

Session details follow the current session. A project file may survive a switch within the same project; switching projects clears incompatible previews. Modules declare session/project/instance scope. Hide incompatible secondary context on entering Settings and restore the previous valid context on return. Instance-wide Design must be labeled explicitly, not mistaken for project-private data.

Migrate old saved chat tabs to primary selections and old file/diff/Browser tabs to secondary selections. Preserve drafts/runtime, ignore invalid/retired IDs safely and validate context. Do not keep the old generic tab strip merely to avoid a bounded migration.

## Primary areas

### Chats

Grouped and flat lists use one native catalog and identical session identity. Include sessions without named projects; native cwd remains available. Projects expand independently, show recent subsets and a Show more action, and never hide the selected row behind that subset. Convey running state without color alone. Truncation preserves full accessible names; relative time has an accessible absolute value.

Guard New session while its create request is pending. A later deliberate new-session action still works. Select the exact returned identity; failure preserves input/navigation. Native title is preferred; distinguish unresolved/default labels without generating an extra model prompt. Duplicate legitimate names remain allowed.

Use the existing conversation renderer and composer in column 3. Put session controls in its header or compact composer-adjacent controls. Preserve tool cards, partial output, attachments and history scroll anchoring. Do not implement another terminal command interpreter in the view.

### Archive

Provide a dedicated persisted collection. Closing a view is not archive; archive is not delete; Stop is distinct. Keep backend run guards where required and offer explicit Stop rather than hiding a behavioral change in archive. Removing a project must not delete native sessions. Unarchive/reopen restores the session's own identity.

### Schedules

Column 2 lists schedules with status/project context. Column 3 shows prompt, timing/timezone, next occurrence, last outcome, run history and supported pause/run-now/stop actions. Reuse backend CRUD, mutation identities and uncertain restart claims. A run navigates to its native session, not another schedule identity. Do not add a scheduler to the assistant/browser or recreate an Automation settings framework.

### Settings

Settings becomes a primary area, not one giant modal. Inventory retained controls and group by ownership: Pixie workspace/appearance, Pi connection/native resources, providers where supported, modules/tools and system diagnostics. Do not invent speculative sections. Configuration, capability and readiness are different states; unsupported administration gets a useful native fallback. Small pickers, confirmations and native interactions may remain dialogs.

## Secondary areas

Details can be sidebar-only: identity, model/thinking, usage/context, live state and supported extension metadata. Unknown usage is not zero.

Files uses the admitted-root tree in column 5 and bounded source/Markdown/image/binary handling in column 4. Preserve root validation, size limits and safe rendering. No editing, LSP, terminal or implicit project write is introduced.

Git resolves repository and review scope explicitly; a project may contain zero or several repositories. Column 5 selects review/changed file and column 4 shows its diff. Keep Git observational; agents make changes through Pi tools.

Browser and later Canvas/Design register their own sidebar/view contributions. Preserve Browser's tested transport and leases rather than assuming an arbitrary website can be embedded as an iframe. Opening any secondary view leaves the conversation selected.

## Mewa integration

Preserve the vendor lock, revision/hashes and correct existing wrappers. Audit against the pinned release's actual contract; a library upgrade is a separate tested change, not assumed from current upstream docs.

Mewa owns semantic color, typography, borders, shapes, spacing primitives, appearance and interaction semantics. Pixie owns shell widths, panel relationships and feature composition. Replace independent tr-* type/color/spacing generators with a temporary explicit mapping; remove the mapping, retired imports/generators and obsolete usage tests only after their consumers migrate.

Keep Tailwind where useful for layout without another visual system. Removing it is not a prerequisite. Establish a tested cascade/layer policy; do not fight Mewa with escalating specificity or !important. Use actual documented semantic tokens, not familiar-looking invented names.

Use square continuous surfaces and one border per boundary. Follow Geist for interface text and Geist Mono for code/output/technical labels. Reserve red/amber/green for meaning, not decoration. Avoid nested cards, decorative shadows, repeated headers and unnecessarily full-width primary actions. Patterned outer wireframe margins are optional decoration, not columns or a reason to reduce usable width.

Inventory Button/Icon, rail/nav, lists/tree, scroll areas, splitters, menus/popovers/dialogs, fields, toast, messages/composer, tools and code/diff. An adapter maps Svelte props to documented markup; it does not restyle the library. One owner mutates each DOM/ARIA state. Use scoped attachments, destroy controllers/listeners on unmount, and never auto-initialize over Svelte-owned regions. Leave controller-generated DOM to its owner.

Build a development-only content-filled fixture page with states; do not create another application or duplicate design-system documentation.

## Geometry and responsiveness

Starting desktop targets, to validate with content: rails/headers about 48–52 px, sidebars about 240–280 px, and content minima around 360 px each. Respect Mewa's 40 px standard and 32 px compact interactions. These are design targets, not fixed accessibility overrides.

Two 48 px rails, two 240 px sidebars and two 360 px views need about 1,296 px before borders. Collapse sidebars before squeezing two unreadable content columns. Respect explicit focus and restore saved preferences when width returns.

Use CSS Grid, min-width: 0/min-height: 0 and independent overflow regions. Composer/header stay within their intended panes; the document must not scroll because a pane overflows. Resizers need pointer and keyboard operation, min/max bounds, reset and persisted preferences.

Narrow screens show one content surface with navigation drawers and an explicit return from secondary content. Keep the same selection model, not a miniature desktop. Trap focus only in real modal drawers, make hidden regions inert and restore focus on close.

## Availability and performance

Keep the shell mounted through provider/network changes. Safely readable cached data may remain with a stale indicator; live operations require revalidation. Missing provider/MCP/module cannot replace the entire app.

Centralize connection/module status subscriptions; prefer events and otherwise use bounded visibility-aware polling. A project view must not create another independent global poller. Lazy-load large module/settings/preview code. Preserve paging/virtualization and avoid re-parsing/highlighting the whole transcript on every delta. Measure layout/navigation separately from model latency.

## Acceptance and order

First land selection reducer/types and a temporary shell switch, then adapters/fixtures, then Chats + Files. Add Git, Archive, Schedules, Settings and registered Browser. Complete responsive/focus modes and persisted-state migration before removing the old shell/tab state and switch.

Required browser scenarios: streaming chat plus file, focus/restore/close with draft retention; grouped/flat/ungrouped sessions; archive/unarchive; schedule/run/settings routes; invalid/deleted selections; project switch during slow history/file/module requests; mid-message/tool/dialog reconnect; no provider/extensions/MCP; disabled/unavailable/malformed modules.

Capture all five modes with real content in light/dark themes, plus empty/loading/error states. Test keyboard-only, labels, reduced motion, forced colors where supported, 200% zoom, long names, narrow screens, large history and repeated mount/unmount. Assert column geometry, no document overflow, independent scroll, composer visibility, focus restoration and runtime continuity. An empty wireframe resemblance is not acceptance.
