# 04 — Native extensions and workspace modules

Owner: E; contracts with A, native transport with B, slots with C/D. Implement Browser through the foundation before adding Canvas or Design. [Sources](sources.md#modules-and-deployment).

## Three boundaries

Native Pi extensions own tools/hooks/commands and native UI requests. Pi's configured native MCP integration owns access to MCP tools. Pixie workspace modules own optional sidebars, viewers and module services. None implies the others are installed.

Do not implement another Pi MCP client in Go, silently install extensions or rewrite user settings to make a panel appear. Browser's human UI can be available while agent access is unavailable; explain those states independently. Vanilla chat remains usable with every optional package and module absent.

## Native UI translation

B owns reading/writing native RPC; E owns primitive-to-UI semantics. Use one final response translation path. Retain select, confirm, input, editor, notify, status, string widgets and title. Add supported editor-text handling. Convert native shapes explicitly, including confirmed for confirmation responses rather than forwarding the current bridge's value blindly.

Key pending work by session, child generation and native request ID. Replies settle once. Preserve deadlines across ordinary client reconnect rather than resetting them on every replay. Validate select options, boolean/text types and size. A second client gets already-settled/stale state instead of delivering a second answer.

Snapshot pending interactions for reconnect; cancel/invalidate them on native completion, timeout, Stop, removal or child-generation change according to the native contract. Do not resurrect history recaps as active dialogs. Bound request count, text/widgets and event rate; blocking work cannot disappear through silent throttling.

Status and widgets are presentation, not authoritative agent-running state. Title hints do not silently rename durable sessions. Track supported background liveness separately. Terminal-only factories/raw input/custom chrome get one useful limitation message and any available text fallback, not fabricated rendering.

Editor-text requests target the originating session. Do not overwrite a newer/unrelated draft. Apply safely to an unchanged empty draft or present an explicit replace/insert proposal. Test active/inactive sessions, user edits, multiple clients and reconnect.

## Registry and contributions

Backend definitions contain ID/version, display metadata, route, capability requirements, handler, status and lifecycle. Frontend descriptors contain registered icon ID, context scope and lazy rail/sidebar/view/settings contributions. Keep one small compile-time registration point for trusted adapters, not a remote-code plugin loader.

Namespace resources by module and context. Context is resolved by the controller and may be session, project or explicitly instance-wide. A contribution receives bounded service methods and validated context, not global mutable shell state, provider credentials, assistant bearer or filesystem authority.

Rail targets slot 6; sidebar targets 5; viewer targets 4. Support sidebar-only, viewer-only and combined contributions. The shell resolves registrations rather than branching on browser/canvas/design names. Test these shapes with development fixtures before module implementations depend on them.

A simple MCP connection can use generic safe text/structured-data/image/resource presentation. A rich viewer requires an explicit adapter. An endpoint is not a renderer; do not execute HTML/JavaScript returned by arbitrary MCP servers. Interactive MCP Apps and a marketplace are not required by this plan.

## Transactions and lifecycle

Persist candidate configuration before publishing it in memory. Preserve settings for all registered modules, including during migrations and rollback; an unknown saved module entry must not be destroyed by toggling a known one. Route only registered modules and report unsupported saved entries explicitly.

Use deterministic catalog ordering/revision. Separate configured/enabled from starting/ready/busy/stopping/unavailable/failed. Persisted enablement can remain true after a failed start while readiness accurately reports the error. An unchanged enable request is a no-op, not a retry/restart. Restart is explicit and bounded.

Changes carry a revision or mutation identity so retries/concurrent settings edits cannot lose another module's state. Disabling revokes new tool/view access, cancels module jobs and invalidates stale completions. Retention is module-specific: Browser leases are transient; Canvas/Design documents survive disable until explicit removal. Authenticated human status/removal must remain possible when a persistent module is disabled or its dependency is missing.

Missing dependencies and runtime failure degrade one module, not the controller, chat or sibling modules. Opening unrelated Settings must not launch Chromium. Preserve existing user enablement choices; use lazy expensive worker startup behind explicit availability.

## Browser first

Reuse existing Browser service, artifact transport, leases, quotas and cancellation. Move controls/status to slot 5 and its view to slot 4. Preserve tested navigation limitations rather than replacing the transport with an assumed universal iframe.

Ordinary hiding does not close an active Browser handle; renew/release its lease according to explicit ownership. Closing or expiry cleans it. Context changes and disable prevent late results from restoring stale views. A duplicate enable leaves active sessions untouched. A failed save cannot change effective route authorization behind an error reply.

Do not describe the current shared-UID, host-networked Browser as isolated. An optional stronger worker/container profile needs end-to-end auth, routing, artifact, filesystem, egress and cleanup evidence before support is claimed. [Security plan](05-reliability-security.md).

## MCP authority and compatibility

Use the existing maintained SDK and record the protocol revisions actually supported by the lockfile, server and configured native adapter. Test initialization, tools/list, tools/call, structured/text/image content, authentication, cancellation and disconnect. Do not turn a latest-spec migration into a prerequisite of the shell rebuild or infer compatibility from the label Streamable HTTP.

Authorize service resources before expensive work. Check Host/Origin, credential role, request bytes and context. Canvas requires a server-issued session-scoped credential or trusted binding from an authenticated session; it cannot trust a session ID in tool arguments or a shared global token. Design read credentials deliberately authorize its instance-wide slot but never upload/delete/shared-focus mutations. No service token in URLs, model instructions or logs.

Native Pi retains its execution policy. These checks protect Pixie's own APIs/resources and do not introduce tool interception or a replacement permissions system into Pi.

## Acceptance

A sidebar-only fixture, viewer-only fixture and unavailable fixture register without editing the shell. Wrong-context resources and stale results after disable are rejected. Browser runs beside streaming chat, survives hiding, and cleans up on explicit close/expiry. Independent toggles/failures preserve siblings and settings. Generic UI answers stay single-use and session/generation-scoped. Native MCP absence remains non-fatal.

After K5, [Canvas](08-canvas.md) consumes this registry; [Design](09-openfig.md) follows last. Do not implement a second registry for either feature.
