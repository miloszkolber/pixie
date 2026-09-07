# MCP draft canvas

An optional `canvas` MCP module lets Pi agents stage visual drafts — HTML the user sees live in Pixie while the agent iterates with screenshot feedback. Network-denied by default. Session-scoped only. Previews use a dedicated agent-browser session owned by the canvas module.

## Decisions

- External network in previews: denied. Served canvas HTML carries no network capability (CSP + no fetch). No per-canvas flag in this plan.
- Scope: one canvas per chat session. No shared gallery.
- Screenshots: the canvas module owns its own agent-browser session. It never drives the Browser module's sessions.

## Module surface

Registry ID `canvas`, extension name `pixie-canvas`, route `/mcp/canvas`, transport `streamable_http`. Enablement lives in `mcp-modules.json` beside Browser and toggles from Tools UI. Default: disabled. Disabled stops jobs and reads but retains stored documents. Missing canvas dependencies degrade canvas only.

| Tool | Contract |
|---|---|
| `canvas_create` | Optional template id → canvas id, version 1. One canvas per session; a second create returns the existing id. |
| `canvas_write` | Canvas id + expected version + full HTML body → new version. Stale versions are rejected, never merged. No partial patches. |
| `canvas_read` | Canvas id + bounded selector → text/DOM snapshot with explicit truncation. |
| `canvas_screenshot` | Canvas id → artifact URL plus bounded image bytes (max 2 MiB, downscaled otherwise). |
| `canvas_list` | Canvas id, version, updated time for the calling session. |
| `canvas_remove` | Canvas id → tombstones the document, cancels matching jobs, removes files. Late completions never republish. |

All tools return structured content plus a compact text summary. HTML body limit 512 KiB per write; total canvas storage 64 MiB. Breaking changes ship as new tool versions. A `pixie://canvas/guide` resource documents the call order, the Mewa component subset with exact tokens, and the network-denied sandbox. Raw HTML outside the subset stays allowed but labeled exceptional.

## Storage

Under `<PixieDataDir>/mcp-canvas/<session-id>/`: `v<N>.html` immutable revisions, `meta.json` (current version, authoring session/tool call, byte counts), `shots/` cached screenshots keyed by version. Writes go to temp files with atomic rename; the version pointer commits last. Removal tombstones first, then deletes. Restart restores the pointer and cleans staging.

## Serve and screenshot paths

- `GET /v1/canvas/<session>/<version>` serves stored HTML to the authenticated Web UI only: `sandbox="allow-scripts"` without `allow-same-origin`, `default-src 'none'`, no `connect-src`, `Cross-Origin-Resource-Policy: same-origin`, `nosniff`, `no-store`, no credentials. Never served to MCP clients as a URL; agents receive bytes or artifact references.
- Screenshots run in the module-owned agent-browser session against the served URL over loopback, stored as artifacts under the canvas session. Reuse the browser artifact conventions (MIME allowlist png/jpeg/webp, per-file and total caps); do not reuse Browser sessions or state directories.
- The Web UI canvas view renders the served URL in a sandboxed iframe and refreshes on version events over the existing channel. Stale events never resurrect removed canvases.

## Security boundaries

Canvas HTML is untrusted content with the browser module's posture: sanitized child environment, no controller secrets, read-only project mounts untouched, single shared UID documented in `docs/security.md`. The module credential authorizes agent calls only; upload-equivalent writes (`canvas_write`) are model tools by design here, so every write carries full HTML in the call body where Pi tool logging applies. No tokens in image or serve URLs.

## Implementation sequence

1. Registry: second module slot, catalog revision over both modules, independent enablement and status. Keep Browser routes and behavior unchanged.
2. Service: store lifecycle, quotas, single-flight render, versioned writes, tombstone removal.
3. Serve + screenshot: sandboxed route, owned agent-browser session, artifact output with byte-bounded image content.
4. MCP handler: six tools plus guide resource with input/output schemas, following `internal/browser/mcp.go` patterns.
5. Web UI: canvas panel (version-aware refresh, thumbnail, fullscreen), chat-side tool card reusing browser-card patterns.
6. Component subset + fixtures: Mewa tokens, two example canvases as test fixtures.
7. Hardening: oversized/flooded writes, stale-version races, reconnect mid-render, removal while viewing, disabled-module access, restart recovery.

## Acceptance

Agent creates, writes twice (second with stale version rejected), screenshots, and iterates to a user-visible draft; human watches updates live; removal stops all access immediately. Full bun suite, Go suite, vet, gofmt, typechecks green. Docs updated in `mcp.md`, `architecture.md`, `security.md`.
