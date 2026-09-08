# Extension foundation

First implement Browser plus fixture contributions. Canvas and Openfig later reuse this foundation; do not implement three separate registries. Track EXT and MODULE tasks in [execution.md](execution.md).

## Three separate systems

| System | Runs where | Owns |
| --- | --- | --- |
| Native Pi extension | Selected Pi process | Agent hooks, tools, commands and native UI requests |
| Native MCP integration | Selected Pi process | MCP connections and tools available to the agent |
| Pixie workspace module | Application service and UI contribution | Optional sidebar, viewer, settings and resource actions |

A tool can work without a panel. A panel can work without an agent tool. An MCP server does not automatically provide a rich viewer. Distinguish those states in settings and diagnostics.

The current registry is Browser-specific and the current host has a generic UI bridge. Generalize the former's contribution boundary; preserve the latter's ownership guarantees. Do not build another agent framework. [Source baseline](sources.md#extensions-and-deployment).

## Native UI translation

Translate choice, confirmation, single-line input, multiline editor, notification, status, string-array widget, title and supported editor-text requests. Preserve native IDs and add session/process-generation context in Pixie's envelope.

Blocking interactions settle once. Validate session, epoch, request and primitive. Confirmation maps to native `confirmed`, not a generic string. Test expiry, abort, cancellation, reconnect, multiple clients, replay and foreign/stale responses.

Retain unresolved requests while the child exists and preserve their original deadline across replay. A restarted child has no old callbacks: invalidate them. Keep latest bounded status/widget state for reconnect, not just transient broadcasts. Bound keys, text, event rate and pending requests without dropping a blocking prompt silently.

Editor-text requests target their original session. Never overwrite newer or unrelated user input invisibly; offer explicit insert/replace on draft conflict. Test empty/current/inactive drafts, reconnect and competing browser clients.

TUI component factories, raw terminal input and terminal-only chrome are not general HTML components. Show a concise limitation and available text fallback, not fake success or a required extension for basic chat.

## Workspace contribution contract

Descriptors contain identity/version, label and registered icon, description, context scope, required capabilities, availability, and contributed sidebar/view/settings. Use a resolved context and namespaced resource identity. No arbitrary injected SVG/HTML for chrome.

The sidebar targets slot 5, selected viewer slot 4, rail entry slot 6. Support sidebar-only, viewer-only and combined contributions. The shell resolves descriptors rather than branching on Browser/Canvas/Openfig names.

Adapters receive bounded controller APIs and resolved context, never provider credentials, host bearer, unrestricted filesystem paths or global mutable shell state. Context changes invalidate requests. Disable, resource removal, Hide and Close have explicit distinct lifecycle semantics.

Simple external MCP integrations may use safe generic text, structured data, images and resource views. Rich Browser/Canvas/Design viewers need trusted local adapters. Do not execute frontend code delivered by an MCP response. Remote UI code and MCP Apps are not required by this registry.

Lazy-load optional viewer code. Distinguish configured, installed, enabled, supported, connected and ready. Missing optional functionality is local, not global incompatibility.

## Backend registry

Use a small compile-time module definition/instance contract for ID, metadata, route, status, handler and shutdown. Keep shared authorization, size/error handling and catalog mediation in one place. Register Browser now; reserve no visible placeholder features for later modules.

Persist the entire known module-state map on every mutation. Preserve unrelated module values, sort catalog records deterministically, compute a revision over relevant catalog state and reject unknown mutations. Coordinate schema handling for temporarily unavailable modules so toggling one cannot erase another's stored data.

Compute candidate enablement, persist it, publish committed desired state, then reconcile runtime. Persistence failure must preserve effective prior state. Runtime startup failure reports enabled-but-unavailable rather than pretending a ready tool. Unchanged enablement is a no-op; Restart is explicit. Avoid expensive shutdown/start while holding a global request lock.

A healthy module with no document is not a failed application. Missing binaries, disabled modules and parser/render failures degrade only the owning module. Preserve authenticated management/removal for retained Canvas/Design data even while execution/read access is disabled.

## Scope and authority

| Module/resource | Scope | Authority |
| --- | --- | --- |
| Browser | Existing explicit panel/session ownership | Preserve tested panel/lease authorization |
| Canvas | One canvas per native chat session | Session-scoped MCP principal plus canvas/version identity |
| Design | One document per single-user Pixie instance | Explicit instance-wide MCP read principal; separate human management authority |

A caller-provided `sessionId`/`projectId`, an MCP transport session identifier, an opaque resource ID or module enablement alone is not authorization. Resolve scope from authenticated application state or a server-issued scoped credential, and compare every resource request against that scope.

For Canvas, adapt the existing session-scoped service/credential pattern through the supported native MCP integration. Scope credentials to module, native session and lifetime/generation as appropriate; revoke them on session deletion. Do not put tokens in prompts, URLs, tool descriptions or frontend state. Reuse a generic session-connection mechanism, not a canvas-specific Pi extension or a second MCP client in Go. When the native adapter cannot provide trustworthy per-session scope, Canvas tools are unavailable until that integration is implemented; a static shared token must not silently widen them to all sessions.

Design deliberately has broader read scope. Label it in upload/settings; any authorized Design reader can inspect the active document. Its read credential cannot upload, delete or change shared focus through application routes. Do not infer project privacy from the session that happened to open the panel.

Keep the bounded module catalog available to authenticated UI through the controller rather than handing the browser the service-wide MCP token.

## Browser as the first module

Reuse service, rendering transport, artifact mediation, ownership, leases, quotas and cancellation. Controls/status belong in the right sidebar and view in secondary content. Do not recreate backend lifecycle inside components.

Define disabled, starting, ready, busy, stopping, unavailable/failed states where useful. Do not launch Chromium for unrelated settings navigation. Preserve the user's enablement choice; lazy startup can reduce overhead without changing it.

Browser UI can be useful without native MCP; Pi tools require a supported native client. Show both independently. Never install packages or rewrite native configuration silently to enable a panel.

The current shared-UID, host-networked, sandbox-disabled deployment is not containment. A stronger optional worker deployment needs routing, auth, file/network isolation, artifact mediation and cleanup tested end to end. Canvas and Design must not inherit an unproven boundary simply by reusing Browser conventions.

## MCP compatibility

Reuse the existing maintained SDK and test the protocol revisions supported by locked dependencies and the native client. Do not write a bespoke transport for catalog functionality. Streamable HTTP behavior differs across revisions, including the inspected 2026-07-28 specification; the transport label alone is not proof of compatibility. [Official references](sources.md#external-contracts).

Test relevant Origin/Host/authentication, resource authority, bounded inputs/outputs, cancellation, real image content and guide access through the actual adapter. These checks protect Pixie's own services/resources; they are not interception of Pi's normal tools.

## Acceptance

Sidebar-only, viewer-only and unavailable fixture modules register without shell edits. Cross-context resource access fails. Disable or removal prevents late completion from restoring a viewer. Unknown capability versions fail visibly and locally.

Browser sits beside streaming chat, survives ordinary hiding according to lease policy, and cleans up on explicit close/expiry. Duplicate enable preserves the active handle; failed persistence cannot change effective enablement. Toggle fixtures preserve every other module's stored state. Vanilla chat works with all optional modules and native MCP absent.
