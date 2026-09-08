# Canvas module

Penultimate feature, after core Gate 5. Read [contracts.md](contracts.md), [extensions.md](extensions.md), [security-and-validation.md](security-and-validation.md), and the [consolidated draft review](repository-review.md#canvas-and-openfig-draft-review). Track CAN-01 through CAN-05 in [execution.md](execution.md). Shared contracts govern limits and publication/durability outcomes.

## Outcome and scope

An agent stages an HTML draft, a human sees version-aware updates in Pixie, and the agent iterates with bounded DOM/text and screenshot feedback. One canvas belongs to one native chat session. No shared gallery, online assets, editing framework or independent agent orchestration.

Registry ID `canvas`, connection `pixie-canvas`, endpoint `/mcp/canvas`, supported Streamable HTTP binding. Default disabled. Missing dependencies or mandatory containment degrade Canvas only. Disable stops jobs and agent/preview reads but retains stored documents. Authenticated owner management can report and remove retained data while disabled or unavailable.

Use `package/internal/canvas/` for service/storage/MCP/HTTP/job supervision and `package/webui/src/canvas/` for its contribution. Reuse existing integration test areas and colocated Go unit tests. Do not add a canvas-specific Pi extension or change assistant architecture. Support both deployment variants from [builds-and-releases.md](builds-and-releases.md).

## Scope and authorization

Resolve native session ownership from an authenticated session-scoped MCP principal or equivalent tested server-side connection. A static module bearer plus a model-supplied sessionId is insufficient. Canvas IDs are references, not credentials. Bind every operation to the resolved session and document generation.

Reuse EXT-04's generic credential/context mechanism. No tokens in prompts, URLs, artifacts or layout state. Test expiry/revocation and reconnect with the actual native MCP adapter. If it cannot carry trustworthy session scope, implement that generic integration before enabling Canvas tools; do not broaden access.

The authenticated user UI selects a native session and uses controller-mediated operations. A session need not belong to a named project. Canvas gains no project/filesystem access from the session's cwd.

## Tools

All results include structured content and compact text, exact version/generation and explicit truncation/limit information. Use shared schemas and the locked MCP SDK.

| Tool | Contract |
| --- | --- |
| canvas_create | Optional known template ID; return canvas ID/version 1. Repeated create for the session returns the existing live canvas without overwriting it. |
| canvas_write | Canvas ID, expected version, full UTF-8 HTML and mutation ID; publish one immutable new revision. Reject stale versions; no patches/merges. |
| canvas_read | Canvas ID, requested version or version captured at admission, bounded selector/output options; return bounded text/DOM with exact version. No arbitrary evaluation tool. |
| canvas_screenshot | Canvas ID and requested/captured version; return actual bounded image content, dimensions, exact version and authenticated UI artifact reference. |
| canvas_list | Only the calling session's canvas ID, version, update time and availability; no global enumeration. |
| canvas_remove | Canvas ID and expected generation/revision or equivalent precondition; tombstone, revoke, cancel and clean. Retried removal cannot affect a later recreated canvas. |

A repeated write mutation ID/fingerprint returns the original result; the same ID with different input fails. Persist retry results with the committed version. A lost acknowledgment is not permission to append another revision.

Initial budgets: 512 KiB HTML/write, 64 MiB Canvas storage across sessions, 2 MiB encoded image bytes before MCP base64. Account separately for wire expansion and decoded pixels. Use and test the dimensions/pixel, selector result, execution deadline and concurrency limits in contracts.md before enabling the module. These are design budgets, not measured capacity.

Provide `pixie://canvas/guide` with call order, exact pinned Mewa subset/tokens and offline limitations. Make essential guidance tool-accessible if the native adapter cannot read resources. No MCP Apps framework is required. HTML outside the recommended subset remains allowed and labelled exceptional, but cannot bypass execution limits.

## Transactional storage

Store under `<PixieDataDir>/mcp-canvas/<opaque-session-key>/<canvas-id>/`:

```text
meta.json
v1.html
v2.html
shots/<opaque-render-key>.png
staging/<operation-id>/...
```

Validate/map identities rather than interpolating model-supplied paths. Metadata includes current version, generation, authoring session/tool/mutation identity, hashes, timestamps and accounting. A recreated canvas has a new identity/generation.

Serialize canvas commits and reserve global quota before staging. Write, sync and rename complete immutable revisions; commit the metadata pointer/mutation result last. A crash exposes old complete state or new complete state, never a partial write. Handle post-publication errors as the explicit persistence-uncertain outcome in contracts.md; a returned error does not prove the old pointer remains installed.

Startup restores committed state, cleans abandoned staging, reconciles accounting and reports corruption. Never recover a pre-deletion backup over a tombstone. Retain tombstones until stale jobs/retries cannot reauthorize old generations.

Evict only regenerable caches. Initially reject quota-exceeding writes after cache cleanup rather than silently deleting authored revisions. Any future retention policy is a separate documented choice. Logical removal is durable before physical cleanup, which can retry with visible status.

Archive retains Canvas. Native-session deletion must explicitly disclose and clean owned Canvas state or offer a separately defined retention choice; never leave accessible orphan documents. Browser caches are not durable Canvas storage.

## Renderer and offline boundary

Canvas owns separate agent-browser execution sessions/private state, never Browser's live sessions, cookies or directories. Reuse bounded job/artifact conventions, not Browser's shared security context.

The draft's iframe/CSP-only path does not establish egress denial. The default executes authored HTML only in a tested isolated renderer and displays validated raster previews in the UI. This retains HTML authoring, live updates and screenshot iteration without running arbitrary draft scripts on the controller or user's browser origin. Direct interactive HTML is conditional on equivalent proven credential/network guarantees, not an implicit MVP requirement.

Authorize and capture the immutable revision in the controller, then pass it through private input/IPC. Do not send the renderer application cookies or broad bearer headers to navigate an authenticated application URL. A job-local asset server, if required, serves only immutable job assets inside the enclosure and holds no controller credentials. The renderer cannot call its parent service.

Require actual egress-denied networking and filesystem/memory/process limits: no controller databases, Pi credentials, project mounts, Docker socket or unrelated files. Use an operator-configured isolated worker/container or tested sandbox launcher with scoped input/output. Do not add privileged daemon access to the controller. If a deployment cannot enforce this, rendering remains unavailable with a diagnostic rather than falling back to the merged same-UID no-sandbox Browser process.

Apply restrictive CSP and Chromium sandboxing where supported as additional layers. Explicitly permit necessary local/inline styles/scripts/assets; `default-src 'none'` alone also blocks required content. Bundle the approved small Mewa/font subset offline; no CDN or runtime dependency installation. [Policy references](sources.md#external-contracts).

Test scripts, CSS/fonts/images, frames, redirects, navigation, forms, DNS, WebSockets and host/loopback access against the actual enclosure. A failed fetch inside one page is not a network-isolation test. Verify the worker cannot read a canary file outside its input/output or exfiltrate through permitted diagnostics/artifacts. Page text remains untrusted data.

## Jobs and version identity

Single-flight jobs are keyed by canvas generation, immutable revision, viewport, renderer version and local asset/font set. Bound global and per-session work. A new write may cancel/coalesce obsolete previews but never change the identity of a completed requested screenshot.

Timeout, cancellation, disable, session deletion and document removal stop matching jobs and descendants. Commit artifacts only if identity/authorization still matches. Late job completion cannot recreate a tombstoned document or make an old preview appear current.

Bound pixel allocation before rendering and validate image MIME, dimensions, bytes and output path without following symlinks. Downscale an oversized image through the bounded pipeline or return an explicit limit error. Do not call an artifact URL alone a model-visible screenshot.

## UI contribution

Canvas's right-rail entry opens revision/status controls in slot 5; selecting its document opens slot 4. Display the version being viewed, current version, rendering state, warnings and update time. Focus/Restore uses the shared shell. No generic content-tab or shell-name branches.

Version events refresh the raster preview through authenticated image delivery with no tokens in URLs. While a newer render is pending, label the prior preview as stale. Provide explicit screenshot/refresh/removal where authorized. Changing session changes scope; stale events cannot restore the previous session's document.

Tool cards show compact operation/version/result and bounded image output, not an executable HTML replay. Two owned test templates demonstrate Mewa-based drafting. A Browser session can remain open alongside the Canvas lifecycle without being driven or reset by it.

## Implementation and acceptance

CAN-01 establishes session authority, quota policy and the isolated offline renderer first. CAN-02 implements atomic revisions, retries, tombstones and restart. CAN-03 adds six tools/guide and actual image responses. CAN-04 registers the view/sidebar/cards. CAN-05 validates failures and packaged runtime on both deployment variants and architectures.

Acceptance: create, write twice, reject a stale write, read/screenshot exact versions, observe human-visible updates, iterate and remove. Retry identities do not duplicate writes. Cross-session and revoked credentials fail. Remove/disable during rendering denies access immediately and cannot be undone by restart or a late completion.

Include flooded/oversized writes, quota concurrency, disk failure, malformed selectors, hung scripts, huge pixel requests, child death, forbidden network/filesystem access, image limits, expired credentials, reconnect and disabled-management cleanup. Basic chat remains usable with Canvas absent. No module is marked complete from a screenshot without authority/containment evidence.
