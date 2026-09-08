# Extension foundation

Browser is the first real contribution; fixture modules prove generality before Canvas and Openfig. Read [contracts.md](contracts.md), [feature-coverage.md](feature-coverage.md) and [acceptance.md](acceptance.md). Execution tracks MODULE, ROUTE, EXT and related fixes. Do not implement three registries or another agent framework.

## Three systems

| System | Owner |
| --- | --- |
| Native Pi extension | Selected Pi process: hooks, tools, commands and native UI |
| Native MCP integration | Operator's native client inside Pi: connections and tool execution |
| Pixie workspace module | Application service plus trusted UI contribution: panels, settings and resource actions |

A native tool can work without a Pixie panel and a human panel without agent tools. A generic MCP response is not a rich UI renderer. Distinguish configured, supported, enabled, connected and ready. Missing optional capabilities remain local; vanilla chat does not require the bridge, adapter or any module.

Use supported native APIs and the explicitly enabled generic bridge for administration gaps, following the FC matrix. Do not install packages, rewrite native settings, start another Pi MCP client or deep-import private session objects to make a control appear functional. Terminal-only limitations and degraded fallbacks are explicit, not a waiver of retained Web UI coverage.

## Native UI translation

B owns native I/O; E owns primitive semantics and the single final response mapping. Translate select, confirm, input, multiline editor, notifications, status, string-array widgets, title and supported editor-text requests. Preserve native ID plus sessionKey, child generation, primitive and original deadline. Confirmation uses native confirmed rather than generic value.

Validate types, options and bounds before forwarding. The first valid answer wins; competing/stale replies get settled/invalid outcomes. Retain pending interactions while their callbacks live, without resetting deadlines on reconnect. Child replacement, timeout, Stop and deletion invalidate appropriate callbacks. Historical recaps never become active dialogs.

A sent response is not proof the native tool accepted it. Plain RPC's exact cancellation/working-message limitations remain FC13–FC16/BRIDGE-03 gates. Test public hooks with real native identities, never timing/title guesses or fabricated cancellation frames. Existing terminal factories/raw input/custom chrome stay explicitly unsupported; do not execute factories as browser HTML.

Bound passive keys/text/rate and preserve latest supported state for reconnect. Clears and blocking settlement cannot be silently throttled. Widgets/status/title are presentation, not agent-running state or durable rename authority. Supported detached-work pins have separate ownership. The Stop/idle-release state machines in contracts.md apply; an unobservable background task is not idle.

Each browser owns its local session draft/revision. Native editor text applies automatically only to the unchanged empty originating draft; otherwise offer explicit insert/replace. It never auto-sends or overwrites another client's input. Native editor() dialogs have separate draft/expiry ownership.

## Backend definition and HTTP routing

One compile-time trusted module definition declares stable ID/version, route ownership, metadata, context scope, supported operations, state/lifecycle and bounded handlers. One frontend descriptor contributes registered icon/label, rail, sidebar, viewer and settings. Do not execute remote module code or inject SVG/HTML chrome from a server response.

ROUTE-01 integrates definitions with the actual top-level HTTP handler. Generalizing Registry.ServeHTTP alone is insufficient because the current application router names Browser explicitly. Register MCP, human management and artifact paths through route ownership; reserve core namespaces and reject collisions/overlap. Unknown /api/* or /mcp/* returns a typed non-success response, not index.html. Keep static frontend navigation fallback separate.

Apply the common configured Host/Origin/role policy before dispatch. MCP and browser management have different valid authentication flows; neither route prefix nor caller-supplied session ID grants authority. Register routes independent of readiness so disabled/missing-dependency status and authorized retained-data removal work. An enabled flag must not bypass handler availability or auth.

Test a fixture module end to end through the assembled HTTP server, including its management/artifact route, wrong method, wrong role, unknown route and conflict. Assert Content-Type/result, not merely HTTP 200. No feature-specific branch should be required in HTTPHandler or the UI shell.

## Frontend contributions

The rail contribution targets slot 6, sidebar 5 and selected viewer 4. Sidebar-only, viewer-only and combined modules are valid. Use stable namespaced opaque resource IDs with explicit session/project/instance context. The shell owns layout/routes; contributions receive validated context and a small controller API, not mutable global state or unrestricted file/service credentials.

Opening a secondary viewer leaves the primary selected. Context change, disable and deletion invalidate stale requests; Hide and Close follow their distinct module policy. Lazy-load optional viewers and retain content/error/loading/unavailable states. Module failure cannot replace the workspace or stop native chat.

Safe generic text, structured-data, image and resource views can support simple MCP integrations. Rich Browser/Canvas/Design use trusted local adapters. Do not execute returned JavaScript/HTML or revive a marketplace/MCP Apps framework as an unrequested prerequisite. Old lazy assets after a release have the common draft-preserving refresh/recovery path, not a blank panel.

## Persistence and lifecycle

Store the complete candidate module map with revision/mutation identity; preserve unrelated and unknown saved entries without routing/executing unregistered code. Catalog order is deterministic and revision reflects committed state. Avoid holding a global request lock through slow startup/teardown.

A known pre-publication save failure leaves prior committed state and runtime untouched. A post-rename/durability-uncertain failure must not be treated as a rollback: retain mutation identity, reconcile validated primary state and report uncertainty. Do not start a newly enabled privileged worker or reauthorize a disabled/deleted resource on an unconfirmed outcome. X04 covers every publication/durability boundary.

After confirmed save, reconcile runtime separately. Startup failure can leave desired enablement true with readiness failed; do not erase user intent. An unchanged enable request is a no-op, not an implicit Restart or recovery retry. Restart is explicit and bounded. Concurrent edits with stale revision fail rather than overwriting a sibling setting.

Disable stops admission, revokes relevant authority, cancels matching jobs and invalidates late completions. Browser leases are transient; Canvas/Design documents remain until explicit removal. Human status/removal remains available while disabled or worker-unavailable. Tombstone publication precedes cleanup; physical cleanup can retry without restoring logical access.

Invalid restrictive configuration makes the owning module unavailable; do not replace a failed parse with permissive defaults. Missing dependencies/delegation and crashed workers affect only that module. Opening unrelated settings does not launch Chromium or parse a document.

## Scope and native MCP authority

| Module | Scope |
| --- | --- |
| Browser | Existing explicit panel/session ownership and leases |
| Canvas | One canvas per authenticated native session; version/generation-bound operations |
| Design | One explicitly instance-wide source slot; model read authority separate from human upload/remove/shared focus |

Neither MCP transport-session ID, native session argument, project label nor document ID authorizes access. Use a server-issued principal or equivalent trusted connection binding. Scope credentials to module/session/lifetime as required; revoke on session deletion and generation changes. Do not include them in prompts, URLs, frontend state, artifacts or model instructions.

Canvas uses EXT-04's generic native session connection support, never a Canvas-specific Pi extension. If that native adapter cannot carry trustworthy scope, tools remain unavailable until the generic supported path works. A global token must not widen access to all canvases. Core chat remains usable.

Design readers deliberately see the instance-wide source. Label that scope before upload and beside shared focus. Reader credentials cannot mutate upload/delete/focus routes. Session/project switching does not make Design private. Serve only bounded catalog/status through normal authenticated UI APIs; do not expose service-wide MCP tokens to the browser.

Reuse the locked maintained MCP SDK and actual supported protocol/client matrix. Test initialization, tools/list/call, guide access, cancellation/disconnect, structured/text/image output and wrong-role calls. Do not infer compatibility from the Streamable HTTP label or require a latest-spec migration unrelated to retained behavior.

## Browser first

Retain existing service, rendering transport, artifact mediation, leases, quotas and cancellation. Controls/status move to slot 5 and view to 4; components do not own backend lifecycle. Do not substitute a universal website iframe for the existing transport.

Ordinary hiding retains the active handle according to explicit lease policy; close/expiry releases it. Disabled/replaced contexts cannot be restored by late results. Repeated enable preserves live sessions; a confirmed explicit Restart warns about affected Browser work. Human UI availability and native tool availability remain separate.

The current shared-UID, host-networked, sandbox-disabled Docker posture is not containment. It may remain during migration with that limitation, but is not accepted as contained execution and must not silently move into the Pi owner's direct-host account. Name/test the supported Browser execution profile for each release topology. A trusted-only exception requires explicit recorded approval; none is implied by this roadmap. Stronger routing/auth/artifact/resource/cleanup guarantees require actual integration evidence.

Canvas/Design always use their enforced worker profile, not an inherited Browser assumption. Namespace, cgroup, scratch, inode, egress and inherited-descriptor checks are defined in contracts.md and X12/X13. Missing worker prerequisites cannot break vanilla core or prevent removal of retained user data.

## Acceptance

MODULE-01 fixture shapes register without shell edits; ROUTE-01 reaches them through the assembled HTTP boundary without module-name branches. X01/X04/X05/X12/X13 test authority, uncertainty and real worker scope. All FC native UI/MCP/Browser rows still apply.

Browser runs beside a streaming conversation, survives Hide according to lease policy and cleans up on Close/expiry. Independent toggles/start failures preserve siblings and saved intent. Strict failures remain local; a forged context or stale generation cannot restore access. Preserve no-loss coverage and both binary topologies before Canvas, then Openfig, consume this same foundation.
