# Web UI redesign plan

This is the stream plan for the Pixie web UI redesign. It is subordinate to [roadmap.md](roadmap.md): it does not change the two-product target, the trust model, the six-slot workspace, the read-only Files/Git boundary, or the disabled-by-default module policy. It replaces the current visual system with the Mewa foundation and recomposes every screen against the approved reference set and the current mewa_ui contract.

Status: plan only. The operator resolved UD-01 through UD-10 on 2026-09-20; the resolutions are recorded below. This document authorizes no implementation, publication, release, or deployment beyond those recorded decisions. Running behavior stays in `docs/`.

## Gates

Tasks distinguish implementation readiness from acceptance using the same scale as [roadmap-aux.md](roadmap-aux.md):

- **G1** — source-only, implementable and testable now with no live host, credentials, network, or browser acceptance.
- **G2** — needs live runtime or browser acceptance evidence.
- **G3** — needs an upstream or design decision, possibly ending in a recorded deviation.
- **G4** — needs separate operator authorization: a release, an upstream submission, or a publication.

A G1 label covers only static/source/contract checks. Any render, computed-style, pointer, keyboard walkthrough, viewport, theme-flash or visual claim in a task's verification is G2 even when the implementation task is G1. Record blocked browser acceptance separately; never close it from source assertions. MU readiness uses local generic fixtures, while downstream Pixie tasks own consumer end-to-end checks.

## Relation to the other plans

- Mewa owns shared foundations, appearance, and control semantics. Pixie owns composition, state, and page-specific shell CSS. The redesign follows that split and adds no second generated visual system.
- The six-slot workspace stays: primary rail, primary sidebar, primary view, secondary view, secondary sidebar, secondary rail. This plan defines the slot grammar and removes the leftover fixture rules that currently own production geometry.
- The redesign includes a bounded mewa_ui workstream (MU tasks). Shared contract repairs land upstream before consumer migration; each vendor update is one coherent versioned pin. A later contract defect may require a verified re-pin, never mixed revisions.
- [roadmap-aux.md](roadmap-aux.md) owns transcript streaming internals (AUX-16, AUX-17), provider usage projection (AUX-06), and file/Git containment (AUX-21). This plan owns their presentation only and does not change their behavior.
- [roadmap-canvas.md](roadmap-canvas.md) and [roadmap-openfig.md](roadmap-openfig.md) own module behavior. This plan only restyles module chrome and keeps both streams disabled by default.
- [docs/sdk-coverage.md](../docs/sdk-coverage.md) remains the record of what the host supports. The redesign must not advertise a capability the controller cannot serve.

## Audit: current web UI defects

The initial audit at `176052e` is historical evidence. The 2026-09-20 source follow-up inspected Pixie HEAD `13062926ab80fbc60e8868c61af71bd6f4f07519` and mewa_ui HEAD `e66d8af4e9b7eb69cdf400e3828bef01a8500776`; paths below are source evidence, not measured cascade winners or pixels. Preserve stores, connection lifecycle, capability gating and six-slot selection; historical test counts are not a current execution result.

| ID | Defect | Evidence | Root cause |
| --- | --- | --- | --- |
| D1 | Two color systems define the same names; Mewa loads last and wins | `webui/src/index.css`, `webui/src/styles/palette.css`, `webui/src/styles/generated/colors.css` vs `webui/vendor/mewa-ui/css/tokens.css` | Pixie's generated color layer is a second authority under a later vendor authority |
| D2 | The production shell carries the layout probe fixture | `webui/src/workspace/views/project-work-area.svelte:1057-1065`, `webui/src/foundation/layouts.css`, `tests/webui/mewa/layout-integration.test.ts` | One DOM region is owned by both the product shell and the probe CSS |
| D3 | Diagonal hatch stripes on viewport edges | Shell `app-shell-edge` and settings/shell `app-sidebar` using stale vendored CSS; current authored Mewa Sidebar has no stripe | Inherited vendor decoration plus stale generated artifacts; verify clean rebuild, do not re-add a removed stripe |
| D4 | The standalone settings sidebar clips | `.app-sidebar` is sticky with `height: 100dvh` inside a `100dvh` shell; `webui/src/settings/settings-area.svelte:214-218` adds `overflow: hidden` | Composition conflict between the page shell and the Sidebar contract |
| D5 | Restore and focus affordances are duplicated | `restore-primary`, `restore-secondary`, `restore-secondary-sidebar`, `expand-left-panel`, `expand-right-panel` in `webui/src/workspace/views/project-work-area.svelte`; archive restore has two owners | Every slot owns a local restore control; archive restore is implemented twice |
| D6 | Oversized green page heading | `webui/src/workspace/views/welcome-panel.svelte` uses `tr-brand-hero` with `color: var(--primary)`; `--accent` is `#8dff4f` | Brand hero class reused as a page title; a product accent the references do not use |
| D7 | Uppercase micro-labels on every panel and section | `eyebrow` in `webui/src/workspace/panel-header.svelte`; `tr-text-eyebrow` in settings | No single owner for the slot label; the token is applied to content too |
| D8 | Mono body text against sans controls; two font families loaded | `webui/src/styles/generated/typography.css` (`--tr-font-family-code`), `webui/vendor/mewa-ui/css/button.css`, `webui/src/main.ts` | A Pixie typography manifest with no UI family under a vendor control layer that assumes one |
| D9 | Boxed rows instead of separators | Repeated `u-rounded u-border u-bg-control-bg` recipe in provider cards, schedule rows, and settings rows | The `u-*` layer is used as a component style system |
| D10 | Mixed corner radii | `border-radius: 0` forced in `webui/src/styles/shell.css:379-381`; `u-rounded` and `--radius-md` used in cards and the composer | Square shell contract vs product-card styling without one radius authority |
| D11 | Two segmented-choice idioms with different heights | `.btn[aria-pressed]` (Grouped/Flat) vs `.toggle` (List/Tree) | No single owner for pressed-choice controls |
| D12 | Two button authoring paths | `<Button>` wrapper vs raw `.btn`/`.toggle` in changes, file tree, and diff panes | The wrapper contract is not enforced across all surfaces |
| D13 | Three scroll owners co-applied to one region | `pixie-panel-scroll`, `mewa-layout-probe__scroll`, and `scroll-area` on the same elements | Fixture scroll class promoted to production beside two existing owners |
| D14 | Magic chrome height | `u-max-h-shell` is `calc(100dvh - 4.5rem)`; `webui/src/chat/chat-view.svelte:864` repeats `4.5rem` | A guessed constant instead of flex measurement |
| D15 | The same action flips between filled and outline | Provider row variant depends on `canOAuth` | No action hierarchy per local group |
| D16 | Width-only border utilities fall back to `currentColor` | `u-border`, `u-border-b`, `u-border-t`; uncolored call sites in settings and the work area | Border width utilities split from border color |
| D17 | Settings lifecycle implemented twice | `webui/src/settings/settings-area.svelte` and `webui/src/workspace/views/project-work-area.svelte` duplicate loading, retry, visited sections, and keyboard handling | Two surfaces with two owners for one lifecycle |
| D18 | View preference persistence is inconsistent | Shell layout persists; catalog view resets per mount; changes view is session-only | No single owner for the per-area view preferences; UD-04 removes the toggles that created the split |
| D19 | Stale dev watch path | `webui/scripts/dev.ts:10` resolves a shared path that does not exist and passes it to `watch()` | Path not updated by the source-tree flatten |
| D20 | Stale embed comment | `webui/webui.go:5-8` describes a tracked placeholder; the build directory is ignored | Comment not updated by the flatten commit |
| D21 | Native UA chrome leaks into controls | Project-tree and settings use `button.tree-leaf`; project-work-area filters use unsupported `class="input"`; Mewa `tree-view.css` lacks a complete resting native-button reset and documents `text-field-input` instead | Missing bounded background/border/radius normalization and unsupported class contracts; MU-06 repairs reusable semantics, consumer tasks use documented classes |

## Review trace

The tables tie each review input to the audit and the owning tasks. UI-08 records the supplied ordering and visual interpretation before MU work; UI-42 compares the implementation, not the initial identity of the references.

Current captures:

| Capture | Surface | Audit IDs | Owning tasks |
| --- | --- | --- | --- |
| image-1 | Project list and project home | D2, D6, D7, D9, D10, D13, D14, D16 | UI-10, UI-11, UI-16, UI-20, UI-21 |
| image-2 | Chats workspace with the details inspector | D1, D3, D4, D7, D8, D9, D16, D18 | UI-03, UI-04, UI-10 through UI-12, UI-24, UI-32 |
| image-3 | Schedules | D7, D9, D11, D16 | UI-20, UI-26 |
| image-4 | Settings providers | D7, D9, D10, D12, D15 | UI-27 |
| image-5 | Changes and diff | D9, D11, D12, D13, D14 | UI-15, UI-31 |
| image-6 | Files | D9, D13, D14, D16 | UI-15, UI-30 |

Reference captures:

| Capture | Target surface | Owning tasks |
| --- | --- | --- |
| 01 | Rails-only chat | UI-10 through UI-14, UI-22 through UI-24 |
| 02 | Grouped projects + chat | UI-20 through UI-24 |
| 03 | Projects + chat + Files | UI-12, UI-20, UI-30 |
| 05 | File-focused central view + Files | UI-12 through UI-14, UI-30 |
| 04 | Chat/file split + Files | UI-12 through UI-14, UI-30 |
| 04-2 | Settings sidebar | UI-11, UI-27 |
| 03-2 | Scheduled sidebar | UI-20, UI-26 |
| 02-2 | Archive sidebar | UI-20, UI-25 |

## Target design language

The approved reference set is the dark capture series `01.png`, `02.png`, `03.png`, `04.png`, `04-2.png`, `03-2.png`, `02-2.png`, and `05.png`. Exact original files are not stored in the repository. UI-08 records their interpretation and provisional dimensions and preserves originals if available; never fabricate original fixtures. Supplied captures include subtle edge hatch: removing it is the settled product deviation, not a claim that the references lack it. UI-42 stores approved generated baselines separately from original review inputs.

### Shell grammar

The shell is one continuous canvas with six named regions and no global product header.

- Primary rail (provisionally about 3rem at wide widths): brand mark, primary areas (Chats, Archive, Schedules), then settings, connection status and account actions at the bottom. Secondary navigation belongs only in the secondary rail. The active item uses the selected surface role and `aria-current`; compact widths use accessible drawer triggers rather than permanently consuming content width.
- Primary sidebar (15-20rem): one panel header per area (uppercase slot label, count, and icon actions) over one tree, list, or form. Collapsible; the collapsed handle lives in the same region.
- Primary view (fluid): one view header row and a labelled section inside the route-owned main content region. The main landmark surrounds selectable primary/secondary content and stays accessible when either view is hidden; individual slots never own competing main landmarks.
- Secondary rail (provisionally about 3rem at wide widths): inspector, files, changes, and capability-available module icons. Each toggles the matching secondary surface; compact widths use a labelled trigger and drawer.
- Secondary sidebar (15-20rem): the context inspector for the current primary view (Files tree, Changes list, Details, module sidebar).
- Secondary view (fluid, on demand): file preview, diff, or module view. Wide widths split the content tracks; compact widths select one surface through the state machine below, not CSS-only stacking of hidden or inert content.

Focus and collapse stay as capabilities. Each slot owns exactly one collapse control in its own header; one reset command restores the default layout. No screen repeats the words "Focus" and "Restore" in headers.

### Responsive contract before implementation

These deterministic implementation regimes use CSS viewport width in rem (including zoom), not device identity. UI-08 records provisional geometry before coding; UI-12 and UI-14 implement state and layout together. Existing `mobilePane` and `mobileSecondarySurface` in `project-work-area.svelte` govern visibility; CSS alone cannot restore a hidden/inert slot.

| Width | Slots and navigation | State, focus and resize |
| --- | --- | --- |
| At least 90rem | All six slots eligible, two central views when selected | All three separators active only between visible tracks; clamp persisted widths to available space |
| 64rem to below 90rem | Rails and central split retained; at most one sidebar open, latest requested wins | Preserve inactive sidebar preference; its trigger restores it; no hidden separator in tab order |
| 48rem to below 64rem | One selected central view, both sidebars in dismissible drawers; no structural change at 60rem | Suspend drag handles; primary/secondary switch selects actual mounted surface, not CSS stacking; restore focus to trigger on dismissal |
| Below 48rem, including below 37.5rem and 320px | One content surface with labelled primary and secondary drawer triggers; no unconditional always-visible rails | `mobilePane` and `mobileSecondarySurface` choose projects, primary, secondary view or sidebar; hidden surfaces inert and absent from tab order; no drag handles |

Crossing a regime preserves selection, drafts and stored desktop dimensions without persisting temporary clamps. Persisted secondary focus opens the selected secondary surface at compact widths; if that selection disappears, choose primary and move focus to a visible heading/trigger. Resize cancels active drags and repairs focus before hiding a slot. Reset clears focus/collapse/size overrides, closes drawers, selects primary and restores regime defaults without clearing content selections or drafts. Escape closes drawers and returns focus; opening a drawer uses the documented focus containment contract. Browser acceptance tests threshold crossings in both directions and reload with secondary focus, not only static viewport screenshots.

### Visual rules

- Mewa semantic roles are the only colors: `--background`, `--surface-*`, `--text-*`, `--border-*`, and status roles. No product accent color and no raw color in Pixie source.
- One font family for all interface text, from the upgraded Mewa foundation (Google Sans Code, monospace). Controls do not switch families.
- Square geometry. `--border-radius-000` everywhere; no consumer radius tokens.
- Borders show structure. One border defines one boundary; connected rows share separator borders and never become a card per row.
- Default single-line controls are 36px (UD-10), with documented compact and multiline exceptions and no Pixie height overrides. Current Mewa source is mixed: buttons/tabs/toggle use 36px, while select/field/text-field/time-field/file-input/date-range and tree branches use 40px. MU-02 changes implementation and docs, not the numeric meaning of `--size-1000`.
- Motion uses Mewa duration and easing tokens only. No continuous animation except Spinner.
- Status uses color plus text. No color-only state.
- Both themes are supported. Dark is the primary reference capture; light is contrast-checked with equal acceptance.
- Empty, loading, and error states use App Shell and Callout contracts. Retain the inspector's legitimate no-selection sentence and real data states; add no dummy values or dead controls.
- Page-level horizontal scrolling is forbidden at 320px width and 200% zoom.

### Anti-patterns to remove

- A card or outline box around every row, input, or list.
- A second generated visual system, generated type classes, or `tr-*` classes.
- Fixture CSS, probe attributes, or two owners for one region.
- Decorative viewport hatch stripes.
- A global header that duplicates the brand already in the rail.
- A hero heading as a page title.
- Duplicate restore affordances and mixed `Focus`/`Restore` text controls.
- Raw `.btn`, `.toggle`, or vendor class strings outside the documented component wrappers.

## Decisions

| ID | Decision | Resolution | Gate |
| --- | --- | --- | --- |
| UD-01 | Mewa pin upgrade | Upgrade from 0.1.2 at `c1bfb64` to a distinct versioned, checksum-verified release from current local `mewa_ui` master (Google Sans Code mono, Remix icons, flat CSS, renamed tokens), with an eventual authorized release tag. SHA-256 integrity is not signing. Upgrade direction settled 2026-09-20; actual builds, tags and releases remain planned, not authorized by this documentation task; local archive vendoring need not wait for remote publication | G1 implementation; G4 release authorization |
| UD-02 | Token authority | Approved as recommended: Mewa roles are the only visual authority; delete the Pixie palette and the generated color and typography system; keep grid geometry plus one minimal syntax and selection exception with a resolution check. Overlays use the Mewa `--overlay-*` roles and shadow uses become surface and border roles | G1 |
| UD-03 | Probe harness | Remove the probe harness entirely: fixture markup, classes, CSS, query handling, and the probe tests. Production ships no probe code | G1 |
| UD-04 | Catalog idiom | Remove both toggles. Catalogs and archive render grouped by project; files and changes render as trees. No flat or list presentation exists, so no view preference is persisted | G1 |
| UD-05 | Restore and focus UX | Approved as recommended: one collapse control per slot header, one reset-layout command, one shared archive restore component | G1 |
| UD-06 | Settings navigation | Approved as recommended: one settings implementation with a vertical section list and App Shell sections in the detail | G1 |
| UD-07 | Split resizing | Adopt the Mewa Resizable contract for the split handle and extend it in mewa_ui if it cannot own the six-slot tracks (MU-03). No second resizer | G1 |
| UD-08 | Decorative chrome | Pixie omits `app-shell-edge` and ships no default sidebar stripe or consumer override. Current authored Mewa Sidebar already has no stripe; MU-01 verifies the clean build instead of reintroducing a removed feature. Existing optional upstream edge effect stays unless a separate need warrants change | G1 |
| UD-09 | Theme default | Approved as recommended: both themes, OS default, persisted override; the captures are dark | G1 |
| UD-10 | Density | Adopt 36px default single-line controls and override no component height. Align mixed current mewa_ui implementation and documentation (MU-02), preserving documented compact/multiline exceptions | G1 |

## Decision record

The ten design decisions were resolved 2026-09-20; UI-00 is complete. Mewa changes are in scope, not external requests. This documentation-only assignment authorizes no source edit, build, tag, commit, publication or deployment; implementation verification below remains planned. The focus contract repair in MU-05 implements existing accessibility intent and needs no new product decision.

## Target composition by screen

| Screen | Slot layout | Target composition | Retained behavior |
| --- | --- | --- | --- |
| Project list and project home | Primary rail + primary sidebar + primary view | `page-overview` with the project name, path, and one primary action; recent sessions as a dense list; compact empty state | Project add/remove, session open, scope routing |
| Chats workspace | All six slots | Panel header, message scroller, existing attachment/mention actions and provider/model/thinking controls with capability-aware send/steer/follow-up; no invented Mode or tool-policy control | Transcript, streaming, queue, history/stop, slash commands, attachment errors, IME, goals, plans, lineage, usage |
| Archive | Rail + sidebar + view | Grouped archived sessions plus Ungrouped and distinct closed chats; one shared immediate metadata-only restore action, no new mandatory confirmation | Archive capability gating, unarchive, closed-chat handling |
| Schedules | Rail + sidebar + view | Rows with schedule name, next occurrence, and enabled state; detail with form, timing, next occurrence, and run ledger; dispatch warning as a callout; create in the page overview | CRUD, enable/disable, run ledger, filtering |
| Settings | Rail + sidebar + view | One shared surface; vertical section list; App Shell sections; provider rows as a dense list with one action hierarchy | Sections, providers, models, tools, extensions, browser, system, diagnostics |
| Files | Rail + sidebar + view + secondary rail + secondary sidebar + secondary view | File tree in the secondary sidebar, preview in the secondary view, `code-block` for text, restyled markdown preview | Read-only inspection, tab previews, containment policy |
| Changes and diff | As Files | Panel header with branch and scope, toolbar icon actions, `file-diff` rows, diff in the secondary view, callout for the raw-bytes and LFS notice | Read-only Git inspection, refresh, scope selection |
| Details inspector | Secondary sidebar | Restyle existing real status and usage, including the legitimate no-selection empty state | Unknown versus zero, offline/stale values, retries, generation guards, Release idle runtime and refusal reasons |
| Canvas and Design | As Files | Module view and sidebar composed from the same shell and panel components | Disabled by default; no policy change |
| Connection and auth | Rail bottom and standalone view | Status dot with visible label and popover; controller-access form with `field`, `form`, and `callout` | Local-request trust model unchanged |

## Mewa component mapping

The redesign uses documented or explicitly extended Mewa contracts. MU tasks repair bounded reusable gaps; Pixie retains domain composition and never invents unsupported vendor classes or domain-specific Mewa settings/provider components. Preserve packaged enhancer and Svelte `@attach` leases, no `auto.js`, and one lifecycle/DOM owner per region.

| Surface | Current Pixie shape | Target Mewa contract | Task |
| --- | --- | --- | --- |
| Slot chrome | Ad-hoc panel headers and `eyebrow` labels | App Shell section primitives plus one consumer panel header | UI-11 |
| Primary rail | Custom rail with text collapse and restore controls | Consumer composition from `nav` and icon-size buttons with token roles | UI-10, UI-12 |
| Project, session, archive, and schedule lists | Boxed `u-*` rows and two segmented toggles | `tree-view` grouped by project; `.app-dense-list` only for flat inspector lists | UI-20, UI-25, UI-26 |
| Three resize tracks | Left/right pixel widths and central fraction with reversed right direction | Controlled `resizable` separator/track extension (MU-03); Pixie grid and persistence | UI-13 |
| Composer | Bespoke composer shell and header selects | `composer` with `select` or `dropdown-menu` controls | UI-23 |
| Transcript | Bespoke renderer chrome | `message-scroller`, `message`, `agent-activity`, `tool-call`, `reasoning`, `todo-list`, `sources`, `code-block` | UI-22 |
| Files | Custom rows with heavy borders | `tree-view`, `code-block`, `scroll-area` | UI-30 |
| Changes and diff | Raw `.btn` and `.toggle` with custom rows | `toolbar`, `dropdown-menu`, `file-diff`, `callout` | UI-31 |
| Details inspector | Real status/usage, empty state and idle-release lifecycle | `.app-status-list`, `statistic`, `badge`, preserving existing behavior | UI-32 |
| Settings sections | Numbered bullets and boxed rows | `nav` vertical list, App Shell sections, `.app-dense-list`, `field` and `form` | UI-27 |
| Empty, loading, and error states | Bespoke cards and copy | `.app-empty`, `skeleton`, `spinner`, `callout` | UI-16, UI-20 through UI-33 |
| Icons | Lucide names and mask CSS | Vendored `mewa-icons` through one `icon` component | UI-06 |
| Overlays | Ad-hoc popovers and dialogs | `popover`, `dropdown-menu`, `dialog`, `alert-dialog`, `toast` | UI-11, UI-25, UI-31 |
| Theme | OS-only class in the document shell | Mewa dark class with a persisted override and the documented theme toggle | UI-07 |

## Workstreams

A task is complete only when its applicable verification passes, with G1 readiness and pending G2 acceptance reported separately. Structure and copy implementation tasks run with `tests/webui`; every phase updates affected tests and stays green. This planning revision runs documentation checks only.

### M0 — Decisions and guardrails

#### UI-00 — Record the decisions

Outcome: UD-01 through UD-10 are answered before implementation. Status: complete (2026-09-20); see the decision record. Touch points: this plan. Verify: design decisions match the table without implying execution or publication authority. Gate: G1. Depends: none.

#### UI-01 — UI conformance check

Outcome: the single-visual-system rules are executable without breaking intermediate migrations. Delta: check raw `.btn`/`.toggle` outside wrappers, styling utilities, raw colors, vendor CSS imports and icon ownership. Separate the permanent, narrowly scoped UD-02 syntax/selection exception from temporary migration entries. Inventory existing foundation/color literals (UI-02/UI-03), font imports (UI-07), shell/native-control consumers (UI-10 through UI-16), area consumers (UI-20 through UI-33) and controller access (UI-34), each with exact scope and removal owner. Do not exempt whole directories or silently accept new violations. UI-41 removes temporary entries only; it retains the documented permanent exception unless MU-05 replaces it. Touch points: `tests/webui/tailwind-removal.test.ts`, `webui/src/styles/utilities.css`, `webui/src/mewa.css`. Verify: the current inventoried tree passes; an unlisted new literal/utility fails; the permanent exception passes; expired migration entries fail. Gate: G1. Depends: none.

#### UI-08 — Reference and contract inventory

Outcome: implementation starts from an explicit reference interpretation, not fabricated screenshots. Delta: record the corrected reference table, provisional dimensions, responsive regimes, token inventory by value type and component/state inventory; preserve original inputs if obtainable and mark missing originals. Record approved generated baselines separately. Verify: all eight references and six slots have owners; geometry is labelled provisional where unmeasured; original-file availability is explicit. Gate: G1, with measured geometry deferred to G2. Depends: UI-00.

### M1 — Foundations

The MU tasks land in mewa_ui and produce the revision that UI-02 vendors. They are planned here because the reference language needs upstream changes and mewa_ui is in scope for this project. UI-03 through UI-07 then consolidate the Pixie foundation on the vendored revision.

#### MU-01 — Decorative chrome (mewa_ui)

Outcome: clean packaged Sidebar matches stripe-free authored source. Delta: verify `library/components/sidebar/sidebar.css`, which already has no stripe; stale dist/vendor still contain it. Do not add a removed stripe feature. Keep optional upstream `app-shell-edge`; Pixie omits it. Touch points: sidebar documentation/preview only if drift remains and generated packaging through MU-07. Verify: source/package parity G1; default preview has no rendered hatch G2. Depends: UI-08.

#### MU-02 — Density contract (mewa_ui)

Outcome: 36px default single-line controls in implementation and docs. Delta: align select, field, text-field, time-field, file-input, date-range and tree branches with button/tabs/toggle; retain compact/multiline exceptions. Do not redefine the numeric scale (`--size-1000` remains 40px). Touch points: relevant component CSS/docs, `library/DESIGN.md`, system density guidance. Verify: catalog/palette and source contracts G1; computed actual control sizes with real button/input/select markup in both themes G2, not token-only assertions. Depends: UI-08.

#### MU-03 — Resizable for the six-slot shell (mewa_ui)

Outcome: Mewa owns all three separator controls while Pixie owns grid geometry and persistence. Current `resizable.js` accepts two direct flex panels, percentage bounds, imperative `flexBasis` writes and a private `setValue`; it lacks controlled updates. Delta: add a documented controlled separator/track integration with pixel/fraction units, bounds, direction (including reversed right width), external value/reset updates and orientation, without competing layout writes. Preserve existing uncontrolled two-panel API. Keep pointer/keyboard semantics, ARIA value text, cancellation/cleanup and a non-drag pointer alternative in Mewa. Touch points: `library/components/resizable/`, adapter and contract docs. Verify locally with generic controlled pixel/fraction/reverse fixtures, external reset, resize, bounds and legacy uncontrolled tests G1; pointer/keyboard/zoom smoke G2 before MU-07 readiness. UI-13 later owns Pixie integration checks, not MU completion. Depends: UI-08.

#### MU-04 — Icon coverage (mewa_ui)

Outcome: finite shell icon coverage without needless aliases. Delta: prefer existing Remix glyphs `inbox-unarchive-line`, `terminal-line`, `layout-left-line`, `layout-right-line`, `filter-line`, `send-plane-line` and `sparkling-line`; map remaining diff/panel needs and add aliases only for genuine gaps. An icon does not authorize a terminal capability. Touch points: icon registry, component guidance and build selection. Verify: local generic icon fixture and registry lookup for the intended name inventory G1; UI-06 later verifies actual consumer calls. Depends: UI-08.

#### MU-05 — Token gaps (mewa_ui)

Outcome: typed token gaps and focus semantics are resolved upstream. Delta: retain the minimal syntax/selection exception unless a genuinely shared role belongs upstream; verify overlay roles. `--focus-ring-width` is a length, whereas `--ring-default` is a complete box-shadow, not a rename. Resolve the accessibility solid-outline intent against `base.css` removing outlines in favor of shadow: proposed bounded contract fix is a solid perimeter outline by default with forced-colors support. Removing decorative shadows must not remove focus. Treat Tabs' blurred selected shadow separately. Touch points: `library/src/base.css`, tokens, Tabs, DESIGN and system accessibility/foundation docs. Verify: local generic focus/overlay/selection contract fixture and palette check G1; keyboard, contrast and forced-colors browser checks G2. No dependence on UI-03 completion; consumer mapping is checked there later. Depends: UI-08.

#### MU-06 — Component contract gaps (mewa_ui)

Outcome: bounded reusable contract repairs, not domain components. Delta: normalize resting background/border/radius for supported native element/class combinations, especially `button.tree-leaf`, without blanket `appearance: none`; consumers replace unsupported `.input` with `text-field-input`. Repair tree selected/disabled branch selectors whose direct-child combinator cannot reach the nested trigger; add metadata/age affordance only if composition needs it and preserve native semantics. Composer must use the documented submit action as `requestSubmit` submitter, respect disabled state and retain validation, IME and `defaultPrevented` handling. Time-field destroy must safely restore its owned canonical hidden-input fallback rather than leave an enabled stale submission field. Native reset-button event timing is a risk, not a reproduced bug: add browser-driven click/reset and canceled-reset regression; change scheduling only if reproduced, since programmatic reset tests are insufficient. Touch points: tree-view, composer, time-field, relevant native control CSS/docs/tests; keep enhancer and Svelte `@attach` leases, no `auto.js` or duplicate DOM ownership. Verify: local semantic/cleanup regressions G1 and real button/input/select computed resting/hover/focus/disabled styles in both themes plus native reset browser checks G2. Provider/settings/run-ledger structures stay Pixie composition. Depends: UI-08.

#### MU-07 — Release the approved revision (mewa_ui)

Outcome: a clean, distinct versioned package containing MU-01 through MU-06, ready for coherent pinning. Source package still reports 0.1.2; current dist manifest reports dirty `b809401` while authored HEAD is `e66d8af`, so neither identifies a clean new release. Delta: plan a new version and clean rebuild, local contract/test/package/catalog/palette/docs checks and browser smoke, record revision, archive names/sizes/SHA-256 and license set. Local archives support UI-02 `--from=<path>` without mandatory remote publication. Tags/releases require the assigned owner's authorization; none are performed or authorized by this documentation task. Verify: clean source/artifact parity, archive integrity and local readiness G1; browser evidence G2; tag/publication G4. Consumer end-to-end checks remain downstream, avoiding MU/UI completion deadlock. Depends: MU-01 through MU-06.

#### UI-02 — Upgrade the Mewa pin

Outcome: one coherent vendored foundation and working consumers at every landing point. Delta: update lock-upgrade/integrity and transactional sync, `mewa.lock.json`, foundation tokens, pin tests and icon map together. Inventory tokens by value type (color, length, family, composite shadow): map `--text-muted` to `--text-secondary`, replace removed `--font-sans`/Geist with Google Sans Code and Lucide with Remix; never substitute composite `--ring-default` for length `--focus-ring-width`. Migrate affected consumers atomically or retain explicitly temporary same-authority aliases with owners/removal gates; no broken intermediate styles. `sync-mewa.ts` hardcodes `LUCIDE-LICENSE.txt`: migrate to `REMIX-ICON-LICENSE.txt`, new font licenses, notices, Docker NOTICE paths and checksum set together, preserving archive safety/rollback and lock integrity. Support existing local `--from=<path>` archive input. Font imports must resolve immediately; UI-07 consolidates ownership. Verify: pin/integrity/rollback/license tests and `mewa:check`, frontend tests/build G1; changed native styles G2. Depends: MU-07.

#### UI-03 — Retire the second visual system

Outcome: Mewa roles are the only color and type authority. Delta: map every remaining `--primary*`, `--container-*`, `--control-*`, `--feedback-*`, `--shadow-*`, `--overlay*`, and `tr-*` use to Mewa roles or Mewa components; move overlay uses to the Mewa `--overlay-*` roles and shadow uses to Mewa surface and border roles; keep the minimal syntax and selection palette from UD-02 as the recorded exception, or map to Mewa roles if MU-05 added them; delete `webui/src/styles/palette.css`, `webui/src/styles/generated/`, the color and typography generators, and their package scripts and tests. Touch points: `webui/src/index.css`, `webui/src/styles/`, `webui/src/lib/shiki-theme.ts`, `webui/package.json`, `webui/scripts/`, `tests/webui/colors.test.ts`. Verify: every referenced custom property resolves; no raw color or type-family literal in `webui/src` outside the recorded syntax and selection exception; `bun run --cwd webui build`. Gate: G1. Depends: UI-02.

#### UI-04 — Geometry and type scale

Outcome: square geometry and one type scale. Delta: remove consumer radius declarations, `tr-*` classes, and the brand hero; use Mewa size, weight, and case tokens for slot labels and body text. Touch points: `webui/src/styles/tokens.css`, `webui/src/workspace/panel-header.svelte`, `webui/src/workspace/views/welcome-panel.svelte`, `webui/src/chat/`. Verify: grep finds no consumer radius token or `tr-` class; both themes render the same geometry. Gate: G1. Depends: UI-03.

#### UI-05 — Shrink the utility layer

Outcome: a frozen migration inventory and replacement contracts, not premature stylesheet deletion. Delta: inventory styling utilities and consumers, freeze new usage, retain live definitions and identify layout-only survivors (flex, spacing, sizing, truncation, screen-reader helpers). Each area task migrates all its consumers and tests atomically; an allowlist is scheduling evidence, not visual compatibility. Delete unused styling definitions only in UI-40 after zero consumers; UI-41 verifies zero temporary entries while retaining only the scoped permanent UD-02 exception where needed. Verify: every live style has an owner/replacement and no definition disappears before migration. Gate: G1. Depends: UI-01, UI-02.

#### UI-06 — Icon consolidation

Outcome: one icon component over the vendored set. Delta: consume the names and aliases added by MU-04 and map every Pixie call site; add no glyph in Pixie, and use the documented sizes with `aria-hidden` on decorative icons. Touch points: `webui/src/components/icon.svelte`, `webui/vendor/mewa-icons/`, `tests/webui/mewa/adapters.test.ts`. Verify: the icon set equals the lock exactly; every call site resolves. Gate: G1. Depends: UI-02.

#### UI-07 — Theme and font boot

Outcome: deterministic theme and font loading. Delta: move the vendored font stylesheet into the single foundation owner (`webui/src/mewa.css`), apply the dark class from the persisted override or the OS preference, and expose the toggle in settings. Touch points: `webui/index.html`, `webui/src/main.ts`, `webui/src/mewa.css`, `webui/src/workspace/shell.svelte`. Verify: both themes render without a flash of the wrong theme; the override survives reload. Gate: G1. Depends: UI-02.

### M2 — Shell

#### UI-10 — Shell skeleton and landmarks

Outcome: one continuous shell with no global product header and an accessible main in every layout. Delta: render the brand mark in the primary rail, move status/settings/account actions to its bottom, remove `app-shell-edge`, and retain the first skip link targeting a route-owned main wrapper around selectable content. Primary and secondary view slots become labelled sections; the main wrapper and its focusable skip target never inherit a hidden slot's inert state. Touch points: `webui/src/workspace/shell.svelte`, `webui/src/workspace/views/project-work-area.svelte`. Verify: shell/landmark assertions G1; keyboard skip reaches visible content in primary, secondary-only and persisted-secondary-focus layouts G2. Depends: UI-04.

#### UI-11 — One panel header

Outcome: every slot uses one header component. Delta: add a panel header that renders the uppercase slot label, optional count or metadata, and an actions slot; use it in all six slots and both settings surfaces; remove one-off header markup. Touch points: `webui/src/components/`, `webui/src/workspace/views/project-work-area.svelte`, `webui/src/settings/settings-area.svelte`. Verify: no screen composes its own slot header; the label appears once per slot. Gate: G1. Depends: UI-10.

#### UI-12 — Slot state machine and persistence

Outcome: one owner per slot transition. Delta: collapse, focus, secondary selection, and expansion move through one store path; persist layout and expansion state only, because UD-04 removes the catalog and changes view toggles; remove the duplicate restore controls and keep one reset-layout command. Touch points: `webui/src/workspace/store/`, `webui/src/workspace/views/project-work-area.svelte`, `webui/src/styles/shell.css`. Verify: `tests/webui/workspace/ui-closure-restore.test.ts`, `tests/webui/workspace/ui-closure-split.test.ts`. Gate: G1. Depends: UI-11.

#### UI-13 — Split handle

Outcome: all three handles use the extended Mewa Resizable contract. Delta: migrate left sidebar pixel width, central content fraction and right sidebar pixel width with reversed direction; Pixie owns grid/persistence, Mewa owns appearance, pointer/keyboard and non-drag pointer alternatives. Synchronize external values, bounds, reset and orientation without imperative flex writes competing with grid state. Remove the bespoke `shell-resizer.svelte` implementation once all callers migrate. Verify: shell-layout/state tests cover three tracks, reload/reset, clamping, reverse direction, collapse and drag cancellation G1; actual pointer/keyboard/non-drag interaction and responsive transitions G2. Touch points: work area, shell styles, store and resizer callers/tests. Depends: UI-12, UI-02 (MU-03 contract).

#### UI-14 — Responsive slot behavior

Outcome: the responsive contract table works as one state/layout system. Delta: implement its width regimes, drawers, labelled triggers and selected-surface switching in work-area/state as well as CSS; reconcile `mobilePane`, `mobileSecondarySurface`, hidden/inert and persisted secondary focus. Suspend hidden handles, cancel active drags on regime changes, restore focus and preserve drafts/desktop preferences. Keep two-dimensional scrolling confined to tables/code/diffs. Touch points: `project-work-area.svelte`, work-area state, selection store, shell CSS and responsive tests. Verify: state transitions G1; browser checks at boundaries, reload with secondary focus, 320px and 200% zoom G2. Depends: UI-08, UI-12, UI-13.

#### UI-15 — Remove the probe harness

Outcome: the probe harness is gone and the product shell is the only shell. Delta: delete the fixture markup and the five probe modes from the work area, delete the probe classes from `webui/src/foundation/layouts.css`, remove the query handling and the production overrides in `webui/src/mewa.css`, retain the Mewa `scroll-area` class as the single scroll owner and delete `pixie-panel-scroll` and `mewa-layout-probe__scroll`, rewrite the probe-heavy tests (`tests/webui/mewa/layout-integration.test.ts`, `tests/webui/mewa/layout-gates.test.ts`, `tests/webui/mewa/layouts.test.ts`) to cover the real six-slot shell, and add an artifact guard that the probe class never appears in the built output. Touch points: `webui/src/workspace/views/project-work-area.svelte`, `webui/src/foundation/layouts.css`, `webui/src/mewa.css`, `webui/scripts/check-artifacts.ts`, `tests/webui/mewa/`. Verify: no probe class, attribute, or query flag remains; the rewritten shell tests pass; the artifact guard fails on an injected probe string; one scroll class per region. Gate: G1. Depends: UI-12.

#### UI-16 — Viewport and decoration fixes

Outcome: no clipped chrome or decorative stripes. Delta: replace magic `4.5rem` heights with flex sizing, fix settings Sidebar composition, omit `app-shell-edge` and consume the clean stripe-free Sidebar (MU-01). Touch points: utilities, chat-view, settings-area and shell. Verify: composition assertions G1; settings/work area at 1440x900 and 390x844 have no cut-off, decorative repeating gradient or inaccessible composer G2. Depends: UI-10.

### M3 — Primary areas

#### UI-20 — Project and session tree

Outcome: grouped catalogs without losing sessions previously reachable through Flat. Delta: compose grouped project/session/archive/schedule trees with dense rows, age metadata, selected/hover roles and filtering; remove Grouped/Flat. Include explicit Ungrouped for nullable project associations, not a synthetic project or hidden all-files root. Preserve native ID/cwd, independent filesystem admission and session-keyed drafts when a project grouping is removed. Touch points: projects/views/store and tests; migrate all local utility/native class consumers atomically. Verify: filtering, selection, expansion, project removal into Ungrouped and draft retention G1; long titles/many rows and keyboard operation G2. Depends: UI-05, UI-11.

#### UI-21 — Project home and empty states

Outcome: the project home reads as a page, not a poster. Delta: use `page-overview` with one action, a dense recent-session list, and compact empty states; remove the hero heading and the centered card grid. Touch points: `webui/src/workspace/views/welcome-panel.svelte`, `webui/src/workspace/views/`. Verify: no oversized heading in the built CSS; the home is usable at 320px. Gate: G1. Depends: UI-04.

#### UI-22 — Chat transcript

Outcome: the transcript uses the AI component contracts. Delta: compose message, message scroller, agent activity, tool call, reasoning, todo list, sources, and code block; keep the AUX-16 and AUX-17 streaming internals unchanged. Touch points: `webui/src/chat/view/`, `webui/src/chat/render/`. Verify: `tests/webui/chat/`; a long transcript streams without regressions; announcements remain single-owner. Gate: G1. Depends: UI-05.

#### UI-23 — Composer

Outcome: the existing composer behavior uses the repaired Composer contract. Delta: compose attachment/mention/slash actions with existing provider/model/thinking controls and capability-aware send/steer/follow-up; no Mode or tool-policy control inferred from mockups. Preserve history, stop/queue, attachment errors, IME, disabled-send and native validation/submitter semantics, accessible names and drafts. Move model controls only with their behavior and tests. Touch points: composer, session-model-controls, chat-view and tests. Verify: composer tests cover capability states, errors, disabled action, validation and IME G1; keyboard submit, menu/dialog focus and actual submitter behavior G2. Depends: UI-05, UI-02 (MU-06 repair).

#### UI-24 — Session header

Outcome: one compact view header. Delta: show the session title, lineage, goal, plan, and stats in one row with icon-only controls and accessible names; remove the text Focus and Restore controls. Touch points: `webui/src/chat/session/`, `webui/src/chat/chat-view.svelte`. Verify: `tests/webui/workspace/shell-layout.test.ts` chrome assertions. Gate: G1. Depends: UI-12, UI-23.

#### UI-25 — Archive

Outcome: archive has one shared restore owner without changing restoration semantics. Delta: render UI-20 grouping including Ungrouped; consolidate the existing immediate metadata-only restore action without mandatory confirmation. Keep closed-chat handling distinct and preserve unavailable capability states rather than enabling unsupported archive operations. Touch points: archive components/views and tests. Verify: supported fixture archive/unarchive round-trip, unavailable state and closed-chat distinction G1; action focus/feedback G2. Depends: UI-20.

#### UI-26 — Schedules

Outcome: schedules read at a glance. Delta: sidebar rows show name, next occurrence, and enabled state; the detail view uses a page overview, a form, a run ledger, and a callout for the dispatch warning; create moves to the overview. Touch points: `webui/src/schedules/`. Verify: `tests/webui/schedules/`; create, edit, and ledger round-trip against the fixture host. Gate: G1. Depends: UI-20.

#### UI-27 — Settings surface

Outcome: one vertical settings implementation, mounted once per route, with the stronger embedded recovery behavior retained. Delta: extract shared loading/retry/visited-section ownership without reducing embedded upgrade recovery, bounded retry, draft protection, visited-section form state or capability filtering. Preserve schedule project context and standalone Back navigation; the route alone owns exactly one `main` landmark, not the shared settings component. Compose provider rows with one action hierarchy; normalize tree buttons and field classes through MU-06. Touch points: settings, work area, upgrade-recovery helpers and tests. Verify: both entry paths, section revisits, missing context, capability changes, asset-load failure/retry limit, chat drafts and Back G1; single main, keyboard/focus and no duplicate mounted lifecycle G2. Depends: UI-11, UI-05.

### M4 — Secondary surfaces

#### UI-30 — Files tree and preview

Outcome: Files inspection follows the reference tree. Delta: Tree View in the secondary sidebar with file icons, selected and hover roles, and keyboard navigation; `code-block` for text preview; restyled markdown preview; compact empty, loading, and error states. Touch points: `webui/src/files/`. Verify: `tests/webui/` file suites; containment behavior unchanged. Gate: G1. Depends: UI-20.

#### UI-31 — Changes and diff

Outcome: Git inspection uses the same panel language. Delta: one toolbar with branch and scope controls, `file-diff` rows, the diff in the secondary view, and a callout for the raw-bytes and LFS notice; remove the List/Tree toggle and its session-only preference per UD-04; remove the remaining raw `.btn` and `.toggle` call sites. Touch points: `webui/src/files/changes/`, the diff pane, `webui/src/workspace/store/content-state.ts`. Verify: `tests/webui/` changes suites; no List/Tree toggle remains; read-only behavior unchanged. Gate: G1. Depends: UI-12, UI-30.

#### UI-32 — Details inspector

Outcome: existing DetailsPanel content is restyled, not replaced with an invented implementation. Delta: preserve real model/thinking/status/usage, unknown versus reported zero, offline/stale presentation, separate summary/stats retries, selection/connection-generation and removed-project guards. The no-selection sentence is a legitimate empty state. Preserve Release idle runtime, busy/error/refusal feedback, refusal while active/queued and controller authority for pinned work; successful idle release deletes neither history nor drafts. Touch points: details-panel, details-model, session-stats and existing release tests. Verify: all preserved states, stale-response races, active/queued refusal and idle release retention G1; readable status/empty/error controls in both themes G2. Depends: UI-12, UI-05.

#### UI-33 — Module chrome

Outcome: Canvas and Design match the shell without behavior change. Delta: recompose module sidebars and views from the shared shell components; keep both streams disabled by default and add no module policy. Touch points: `webui/src/canvas/`, `webui/src/design/`. Verify: module tests; the disabled default is unchanged. Gate: G1. Depends: UI-10 through UI-16.

#### UI-34 — Controller access

Outcome: standalone controller access uses the same form and status contracts without changing authentication. Delta: migrate `webui/src/connection/controller-access.svelte` composition, utilities and native field classes to documented Mewa field/form/callout contracts; preserve initial authentication checking, status-fetch failure/retry, rejected credentials, pending submission and successful authentication. Preserve the existing request/authentication boundary and do not log or persist entered secrets through new UI state. Verify: existing authentication/state behavior and utility migration G1; keyboard submit, focus, errors and both-theme native-control styles G2. Depends: UI-05, UI-10.

### M5 — Cleanup, tests, and acceptance

#### UI-40 — Remove dead surface

Outcome: no dead CSS/classes/components remain after consumer migration. Delta: prove zero live consumers before deleting frozen styling utilities, arbitrary sizes, temporary aliases and orphaned generated/scroll/probe remnants; remove layouts.css if empty. Fix dev watch path and stale embed comment. Touch points: styles/foundation, dev.ts and webui.go. Verify: zero-consumer inventory, filename check and production build; real shared watch path. Gate: G1. Depends: UI-03 through UI-07, UI-13 through UI-16, UI-20 through UI-34.

#### UI-41 — Update the web UI tests

Outcome: final coverage/conformance audit, not deferred repair of broken tests. Delta: every implementation task updates its affected tests in the same change; UI-15 replaces probe assertions with real shell behavior. UI-41 removes temporary migration entries, retains only the precisely scoped UD-02 exception where still needed, audits single ownership, grouping/tree contracts and typed tokens, and checks retained behavior coverage. Touch points: tests/webui. Verify: frontend suite green with no behavior test removed merely to hide regression; 475 tests/102 files is a historical baseline, not a fixed target. Gate: G1. Depends: UI-40.

#### UI-42 — Acceptance capture

Outcome: final comparison against UI-08 interpretation with reproducible approved generated baselines. Delta: run the production-built acceptance target, not development assets alone, in both themes at 1440x900, 390x844, 320px and 200% zoom, including responsive transitions and the preservation matrix. Record generated captures separately from unavailable originals, dimensions, computed control styles and explicit deviations (including hatch removal). Update browser assertions with their owning changes. Touch points: tests/ui; development docs only for command changes. Verify: documented Docker build/run and measurable acceptance below. Gate: G2; G1 script checks do not establish rendering acceptance. Depends: UI-08, UI-41.

#### UI-43 — Documentation and roadmap upkeep

Outcome: the docs point at the current plan. Delta: keep the six-slot pointer and the central roadmap link current as the shell grammar evolves, and change no operating doc ahead of shipped behavior. Touch points: `AGENTS.md`, `roadmap/roadmap.md`. Verify: `bun run check:docs`, `bun run check:coverage`. Gate: G1. Depends: none.

## Verification

### Behavior preservation matrix

| Boundary | Preserve | Owning regression checks |
| --- | --- | --- |
| Catalog/group removal | Native identity/cwd/admission, Ungrouped reachability, drafts, selection and capability gating | UI-20/UI-25 removal, filtering, unavailable archive and immediate restore |
| Chat/composer | Streaming/reconnect generations, history, stop/queue, send/steer/follow-up capabilities, slash/mention, attachments/errors, IME and drafts | UI-22/UI-23 success, disabled, validation, cancellation and reconnect cases |
| Settings | Upgrade recovery, bounded retry without mutation replay, visited forms, draft protection, project context, Back, one lifecycle/main | UI-27 both route entries, failed asset load and section revisits |
| Controller access | Authentication checking, failure/retry, rejected credentials, pending submit and success without changing request authority | UI-34 state/keyboard/focus regressions and secret-handling review |
| Details/release | Real usage, unknown/zero distinction, stale/offline, retries, generation guards, active/queued refusal and idle history/draft retention | UI-32 selection/reconnect races, retry and release failure/success |
| Layout/resizing | Three persisted tracks, focus/collapse/reset distinction, secondary focus, hidden/inert state and drafts | UI-12 through UI-14 reload, reverse resize, breakpoint crossing and drawer focus |
| Files/Git/schedules/modules | Read-only containment, scope/refresh and errors; schedule project context, CRUD/ledger/dispatch warning; disabled module defaults | UI-26/UI-30/UI-31/UI-33 existing behavior suites retained |
| Mewa lifecycle/forms | One enhancer/attach lease owner, cleanup, submitter/disabled/validation, native reset and no-JS fallback | MU-03/MU-06 local fixtures before packaging; consumer integration after pin |

### Migration exit gates

1. Inventory: UI-08 precedes MU changes; original-reference availability, typed tokens, native class contracts and responsive table are explicit.
2. Upstream readiness: MU local generic fixtures and clean package/contract checks pass without downstream dependencies; browser-dependent readiness remains G2. No stale dirty dist is vendored.
3. Pin cutover: one revision/version/checksum/license set and atomic consumer migration or bounded same-authority aliases; integrity/rollback and affected tests change with the pin.
4. Area cutover: migrate all live consumers and tests for that area together; retain needed utilities until the last consumer moves; no allowlist substitutes for visual compatibility.
5. Cleanup: UI-40 proves zero consumers for removed definitions/aliases; UI-41 removes temporary exceptions and retains only the scoped permanent UD-02 exception, not postponed repairs.
6. Acceptance: UI-42 checks production-built assets and behavior/visual states at G2; static G1 checks cannot establish browser acceptance. Later contract bugs permit a coherent versioned re-pin with the same gates.

### Commands

The commands below are planned implementation checks, not commands executed for this documentation-only revision. Its allowed checks are `bun run check:docs`, `bun run check:filenames` and documentation diff/path review; no runtime tests, builds or browser runs.

- `bun run --cwd webui test` for the frontend suite (102 files, 475 tests at the baseline).
- `bun run --cwd webui build` for the Mewa check, typecheck, bundle, and artifact checks; the typography and color checks run while the generated systems still exist and drop with UI-03.
- `bun run mewa:check` for vendor integrity.
- `bun run check:docs` and `bun run check:coverage` for documentation consistency.
- `docker build -f Dockerfile --target ui-acceptance -t pixie-ui-acceptance .` then the documented run for browser acceptance.
- `bun run lint` and `bun run typecheck` at the root before handoff.
- In mewa_ui for every MU task: the test suite, `bun run package:check`, `bun run catalog:check`, `bun run palette:check`, `bun run docs:check`, and the browser smoke suite.

### Acceptance criteria

Each item is checked on production-built assets in both themes at 1440x900, 390x844 and 320px, at 200% zoom, and with keyboard only. Include empty/loading/error/disabled/stale/disconnected states, long unbroken titles and enough rows/transcript content to overflow each intended scroller. Record viewport, theme, state and measured result with the generated baseline.

1. Document scroll width does not exceed viewport client width (allow at most 1px rounding) at 320px or 200% zoom; no clipped focused control, composer or drawer trigger. Code/table/diff local scrolling is allowed.
2. One brand mark, exactly one accessible route-owned `main`, skip link first with a usable focus target, and one heading per visible view. Verify secondary-only selection, persisted-secondary-focus reload and transitions between width regimes; a hidden primary slot must never hide the main or skip target.
3. Every control has an accessible name (icon-only actions included), a visible solid perimeter focus indicator and the documented size. Computed default single-line control height is 36px at default zoom (at most 1px rounding); compact/multiline exceptions are enumerated. Measure actual button/input/select resting background, border, radius and focus in both themes, not token strings alone.
4. No card or outline box around a list row, input, or section that the reference shows as a row.
5. No decorative hatch stripe, no consumer radius, no `tr-*` class, and no raw color in Pixie source outside the recorded syntax and selection exception.
6. One button authoring path, one scroll owner per region, one restore owner, and no per-area view toggle.
7. Normal text contrast is at least 4.5:1, large text and essential control/focus boundaries at least 3:1; forced colors retains a visible outline. Reduced motion suppresses nonessential motion, increased contrast retains boundaries, and keyboard traversal reaches only visible/non-inert surfaces with no trap outside modal drawers/dialogs.
8. Empty/loading/error/disabled/stale/disconnected states are readable and actionable where appropriate; many rows/long titles preserve alignment, truncation access and one scroll owner without changing slot geometry.
9. Functional coverage is retained for chat, schedules, settings, files, connection and selection; markup assertions change atomically with implementation, not behavior expectations to conceal regressions.
10. The shipped bundle stays inside the existing JavaScript budget.
11. Compare the eight reference compositions against UI-08's documented geometry and approved deviations; record measured rail/header/sidebar/control dimensions and reject unexplained differences over the agreed baseline tolerance (provisionally 2px for fixed chrome, excluding font rasterization). Missing original pixels cannot support a pixel-perfect claim.

## Sequencing and dependencies

The critical path is UI-00 (complete) → UI-08 → MU-01 through MU-06 local readiness → MU-07 clean versioned artifacts → UI-02 coherent pin → UI-03 → UI-04 → UI-10 → UI-11 → area migrations → UI-40 → UI-41 → UI-42. MU verification never waits on downstream UI completion; publication is not a prerequisite for local `--from` vendoring. UI-01 runs in parallel; UI-05 inventories/freezes live utilities after the pin, screen tasks migrate consumers atomically, and UI-40 alone deletes unused definitions. UI-06/UI-07 consolidate icon/font/theme after UI-02; shell state UI-12 feeds all three resizers UI-13, responsive UI-14 and probe removal UI-15. Area tasks start after UI-04 and their listed dependencies, update tests with each change and share no file owner. UI-40 also waits for UI-06/UI-07 and all shell/area consumers, not only the last Changes task. A later upstream contract fix may trigger a new coherent versioned re-pin; revisions never mix.

`webui/src/workspace/views/project-work-area.svelte` is the hot file. One owner edits it per phase; area work moves markup out of it into area components rather than editing it in parallel.

## Risks and open questions

- The mewa_ui workstream is in scope; local generic readiness precedes coherent versioned pinning, with re-pins permitted for discovered contract bugs. The vendor tree never mixes revisions.
- The utility removal touches hundreds of call sites. Temporary migration entries must reach zero at UI-41; the separately recorded, narrowly scoped UD-02 exception is not a temporary migration entry.
- Existing tests lock probe classes, restore testids and the vendor pin. Every owning change updates affected assertions immediately; UI-15 replaces probe tests with real shell coverage and UI-41 audits the result. A failing test is never deleted to hide a behavior change.
- Removing the probe harness is the one destructive step. UI-15 confirms the rewritten tests cover the same shell behavior before the fixtures are deleted.
- Original captures are not repository assets. UI-08 records interpretation before implementation; UI-42 records separately labelled generated baselines, never fabricated originals.
- Focus, restore, and settings lifecycle are behavior, not decoration. Removing an affordance means re-homing it, never dropping it.
- The 36px versus 40px control height and the `--shadow-selected` Tabs exception are mewa_ui contract questions resolved inside this project (MU-02 and MU-05) rather than patched in Pixie CSS.
- The syntax and selection palette is the one recorded exception to Mewa color ownership. It stays minimal and checked; every other Pixie color definition is deleted.
- MU-03 and MU-06 have source-confirmed controlled-resizer/native-contract gaps; only optional metadata affordances and the unproven native reset timing repair are conditional. Their scope remains bounded and reusable.

## Not in scope

- No new product capability, route, or module.
- No change to the trust model, authentication, host protocol, or capability negotiation.
- No write path for Files or Git.
- No Tailwind, no utility framework replacement, and no second generated visual system.
- No IDE, Monaco, LSP, terminal, or worktree manager.
- No canvas or openfig behavior change; those streams stay disabled by default.
- No source implementation, build, tag, commit, publication or deployment in this documentation assignment. Future execution follows the assigned owner's authorization; UI-42 needs a runnable acceptance environment.

## Evidence limits

The initial audit and the 2026-09-20 follow-up are source evidence, not browser verification. Follow-up sources include Pixie's details-panel, shell-resizer, work-area and sync-mewa callers and Mewa's authored Sidebar, Tree View, Resizable, Composer, Time Field, base tokens/accessibility and dist manifest at the HEADs recorded above. The supplementary Mewa browser audit failed executable discovery (Chrome required / `PUPPETEER_EXECUTABLE_PATH`); no browser rendering executed. No runtime tests, builds, live host, credentials, Docker or acceptance run were exercised for this documentation revision. Current dist identifies dirty `b809401`, not clean authored `e66d8af`; future vendoring requires rebuilt artifacts and a distinct version. Historical counts and original capture interpretations are not new test results or measured pixels.
