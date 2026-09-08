# 08 — Canvas

Owner: H with E/F. Implement after core gate K5, before Design. This plan supersedes docs/mcp-draft-canvas.md and retains its session-scoped visual-drafting workflow. [Draft and security sources](sources.md#canvas-and-openfig).

## Scope and decisions

One Canvas per native chat session. Agents stage full HTML revisions, inspect the rendered result and request screenshots; the user sees committed versions in Pixie's secondary viewer. No shared gallery, project-wide document or per-canvas network override.

Module ID canvas, connection name pixie-canvas, MCP route /mcp/canvas, transport matched to the tested SDK/native-client matrix. Default disabled. Missing dependencies degrade Canvas only. Disable cancels jobs and denies tool/content access but retains documents. Authenticated human metadata/removal remains available even when disabled or worker-unavailable.

Canvas owns its renderer session and state. It never drives Browser module sessions or borrows their leases. Reusing the vetted agent-browser executable or bounded artifact helpers does not mean sharing runtime authority.

## Changes from the draft

CSP plus an allow-scripts iframe is not a complete network-denial boundary. The draft also does not define how an iframe or renderer authenticates a stored-HTML URL without exposing a credential. Its session ID parameter alone would not authenticate the calling session.

Adopt an isolated-renderer-backed live view for the initial implementation. Raw generated HTML/JavaScript is not executed in Pixie's application DOM or the user's browser. The trusted viewer displays versioned rendered images and state. A committed HTML update automatically refreshes the view; this is live draft iteration, not a promise of a remote desktop. A future interactive viewer may forward bounded explicit input into the same isolated renderer, but must not weaken the boundary or add arbitrary eval.

Do not implement the draft's raw-HTML iframe route as a supposedly network-denied equivalent. A direct script-capable browser preview requires a separate approved security design and is not necessary for this milestone.

## Authority

Derive session identity from a server-issued scoped MCP credential or a trusted authenticated session binding. A shared global module token plus a caller-supplied session ID is insufficient. Validate binding before create/list/read/write/screenshot/remove. Resource IDs do not authorize access by themselves.

The ordinary native MCP integration consumes that scoped connection; no Canvas-specific Pi extension or assistant command is added. Test that a token from session A cannot read or mutate B even with B's IDs. If the native adapter cannot carry the required scope, Canvas tools remain unavailable with an explicit capability explanation until a supported binding is implemented.

Human access uses normal Pixie authentication and session ownership. Credentials never appear in URLs, HTML, screenshots, artifacts, tool instructions or logs. The rendering worker receives job input, not a reusable controller/assistant/MCP credential. Removing a session invokes Canvas cleanup; hiding or archiving it does not silently delete documents.

## Tools

All tools return structuredContent and a compact text summary. Model-visible tool schemas are bounded and closed where practical. Bind retry identity to credential, session, operation and payload fingerprint; reusing a mutation ID with different content is a conflict.

| Tool | Contract |
| --- | --- |
| canvas_create | Optional registered template ID; returns canvas ID, generation and version 1. A repeated create returns the existing document without replacing it. |
| canvas_write | Canvas ID/generation, expected version, full HTML and mutation ID; returns committed new version. Stale writes fail, never merge. |
| canvas_read | Canvas ID/generation and requested version, optionally a bounded CSS selector; returns bounded rendered text/DOM summary with explicit truncation. No JavaScript evaluation input. |
| canvas_screenshot | Exact canvas generation/version and fixed viewport variant; returns that version's artifact reference and bounded image content, not a different newer version. |
| canvas_list | Metadata for the authenticated calling session only: identity, current version, update/render state. |
| canvas_remove | Canvas ID/generation and mutation identity; durably tombstones first, cancels work, revokes reads and schedules physical cleanup. Idempotent retries. |

Full HTML body limit: 512 KiB in UTF-8 bytes. Aggregate Canvas storage budget: initially 64 MiB across committed revisions, staging and screenshots. Image content: at most 2 MiB decoded per response; also bound base64/JSON envelope bytes. Downscale with checked pixel/dimension limits before returning or produce a clear limit error. These are initial validation budgets, not measured capacity guarantees.

Use full-document CAS writes only; no patch language. Expose stable conflict, missing, disabled, unauthorized, quota and render-failed errors. A render failure does not pretend a successfully committed HTML revision disappeared or replace its last good screenshot with the wrong version.

## Storage and jobs

Use package/internal/canvas and registered UI code under package/webui/src/canvas. Keep Go integration tests in package/tests/go/canvas and feature fixtures in existing test areas. Storage belongs under <PixieDataDir>/mcp-canvas/<opaque-session-key>/<canvas-id>/, not Browser's disposable session cache.

Store immutable v<N>.html revisions, meta.json with current pointer/generation/ownership/byte counts, and shots keyed by revision, viewport, renderer and local asset/font-set version. Native IDs and display names must not be interpolated as unchecked filesystem paths. Persist native session association separately from the opaque storage key.

Reserve quota before staging. Atomically publish a completed revision and then its pointer; fsync where required by the persistence contract. Crash recovery exposes the last committed pointer and cleans incomplete staging. Concurrent same-version writes cannot both commit. Never evict source revisions silently to conceal quota exhaustion; remove regenerable screenshots first, otherwise reject the write with recovery guidance.

A tombstone is committed before cleanup. Include generation in every job/event/artifact key. Late render completion after remove/disable cannot publish a result or restore the document. A new create after removal receives a fresh identity. Restart retries physical cleanup without treating a stale backup as a live pointer.

Limit rendering to one active job per Canvas with an explicit global bound. Coalesce superseded background refresh jobs while keeping explicit screenshot/read requests tied to their requested version. Use wall/CPU/memory/PID/output limits and terminate the job enclosure on cancellation or overflow. Rapid writes cannot grow an unbounded queue.

## Rendering and network denial

Before loading untrusted HTML, implement the enforced worker boundary in [05](05-reliability-security.md). No host networking, host-loopback services, Pi credentials, controller database, project mounts or sibling Canvas state is available to the renderer. Use an operation-local immutable input and isolated output. A same-UID subprocess with changed HOME is insufficient.

Provision the worker enclosure explicitly; do not mount a Docker socket into Pixie or silently create privileged infrastructure. It can use a private local document server inside its isolated network namespace or a validated document injection mechanism. The controller supplies bytes/approved local assets over the bounded trusted job transport, not an authenticated application URL. The worker must not fetch arbitrary files from the controller.

Apply a complete document CSP and browser sandbox as defense in depth. Define actual inline script/style and local/data asset allowances so supported drafts render; default-src 'none' alone is not the complete policy. Forbid network connections, forms, nested remote frames, popups, downloads, unsafe navigation and unapproved schemes. Bound the rendered DOM and selector result. Raw HTML outside the Mewa subset is allowed only inside this same enforced boundary.

Prove network denial with an external test receiver and host-service probes, not only a disabled fetch example. Cover navigation/meta refresh, links, forms, CSS/image/font URLs, WebSocket/fetch, redirects and browser background traffic. No external request reaches the receiver and no privileged host path is readable. If the deployment cannot enforce the boundary, do not enable arbitrary HTML processing.

## Serving and UI

The controller serves authenticated image-only artifacts and bounded metadata. Use opaque IDs, correct MIME, no-follow validation, no-store/nosniff and ownership/generation rechecks. No raw controller filesystem paths or token-bearing URLs. Cancellation/removal prevents new responses; bytes already delivered or recorded in Pi tool history cannot be recalled.

Register the Canvas rail entry in slot 6, version/status/actions in slot 5, and trusted render viewer in slot 4. Reuse shell Focus/Restore rather than inventing another fullscreen layout. Never replace the primary conversation. Show current committed version, rendering state, displayed-image version and any error separately. Preserve a clearly labeled older successful image while a newer revision renders.

Subscribe through the existing event channel, reconcile metadata on reconnect and ignore stale generation/version events. A notification does not carry raw HTML. Read/screenshot calls work with the Web UI closed. Hidden views do not terminate an agent's active render job; explicit disable/remove/cancellation do.

Provide pixie://canvas/guide with call order, limits, exact supported Mewa subset/tokens and the real isolation behavior. Provide tool-accessible guidance when the native adapter does not expose resources. Templates and approved assets are local and license-reviewed; no CDN/font downloads. Keep ordinary HTML possible without advertising exceptions as product direction.

## Implementation sequence

1. Validate scoped native MCP binding and an isolated render job end to end; record the chosen enforceable deployment.
2. Add transactional revision storage, quotas, tombstones, restart recovery and single-flight rendering.
3. Implement bounded read/image artifact output and six MCP tools using the shared service/auth conventions.
4. Add registry contributions and version-aware live viewer/tool cards; include two project-owned Mewa draft fixtures.
5. Run failure/network/authorization tests and package optional dependencies without increasing vanilla-core requirements.

## Acceptance

An agent creates a Canvas, writes two revisions, receives a stale-version conflict, reads the rendered content, requests an actual screenshot and iterates while the user sees the correct version. Reconnect and a closed browser tab do not break headless tools. A session cannot access another's document. Duplicate mutations are safe.

Test oversized/flooded writes, storage exhaustion, invalid selectors, render crash/timeouts, cancellation, disable, cross-session requests, removal mid-render/view, restart and late completions. Verify pixel/output bounds, no external network/host-file access and no credential leakage. Disabled retains data; explicit removal revokes access before cleanup and still works with missing worker dependencies.

K6 requires actual renderer, native MCP and UI evidence plus core regressions. A mocked image, CSP declaration or screenshot from the unrelated Browser module does not satisfy it. Then proceed to [09 — Openfig](09-openfig.md).
