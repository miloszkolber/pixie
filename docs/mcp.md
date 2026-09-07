# Pixie MCP publisher

The Browser module for trusted MCP clients is published by the main Pixie process on the application listener. It is separate from the universal MCP client profile (`mcp`) above.

| Endpoint | Purpose |
| --- | --- |
| `/mcp/browser` | Browser Streamable HTTP MCP, same tools and resources as the former separate host |
| `/api/mcp/modules` | Authenticated module catalog, schema version `1`, engine `in-process` |
| `/api/mcp/status` | Authenticated build and catalog status |
| `/health`, `/livez` | Process liveness (application listener) |
| `/readyz` | Application readiness, `200` or `503` |

Requests use `Authorization: Bearer <PIXIE_MCP_TOKEN>`. The catalog contains module IDs, names, paths, transport, state and an opaque revision. Browser uses ID `browser`, connection name `pixie-browser` and path `/mcp/browser`.

`PIXIE_MCP_MODULES` and `PIXIE_MCP_DISABLED_MODULES` are retired and ignored; setting either logs a startup warning pointing at the Tools UI. Publication enablement lives in the Pixie persist store (`mcp-modules.json`) and in the Tools UI in-process section (Enabled, Status, Endpoint), exposed as `mcpRegistry.catalog` / `mcpRegistry.moduleSetEnabled`; the Browser module defaults to enabled.

Browser provides `browser_command`, `browser_guidance` and `pixie://browser/guide`. It limits sessions to 16, artifacts to 64 MiB per session and 256 MiB total, and commands to 120 seconds. Controller-owned panels have five-minute leases, renewed every minute; abandoned panels are cleaned up. Ordinary MCP client sessions remain the caller's responsibility.

Keep browser session IDs short: at most 28 characters with the default storage roots. Chromium's singleton socket inherits the state path length, and longer IDs exceed its limit at launch.

A ready publisher has not necessarily launched Chromium. Verify browsing by opening a disposable panel, navigating, taking a screenshot and closing it. For failures check the application address, token, state ownership, container logs and catalog status. Keep tokens out of command-line arguments and never forward them through redirects.

Browser is the only module. Signet, Web, Todo, Questions, and Subagents are never published through Pixie MCP. Browser storage stays isolated under the controller data directory (`browser/`). The model-facing Browser API (`pixie-browser`, same tools and resource surface) has no engine variants. The `mcpAdapter.status` projection surfaces the Pi-side adapter state (connected, cached, failed, needs-auth, not-connected, disabled) and stays fail-open when the adapter profile is not enabled.

The Pi MCP client uses the pinned upstream `pi-mcp-adapter` runtime unchanged, with no custom transport.

## Browser engine

Chromium is the only backend. Verification runs agent-browser `0.34.0` against headless Chromium through the same executor the MCP tools share (`tests/go/browser/chromium_parity_test.go` with `PIXIE_BROWSER_LIVE=1`): connect, navigate, snapshot with refs, click, fill, type, press, scroll, wait, form login, large DOM, infinite scroll, iframe, JS-heavy pages, screenshots with artifacts, close with storage removal, and policy rejection of `eval`/`pdf`/`download`. Typical latencies are ~600 ms for a cold open (new Chromium), 10–30 ms for snapshot and actions, ~60 ms for screenshots, and ~110 ms for close. Reopening within seconds of close can fail while the previous daemon winds down; retry once. The `0.36.0` release exists but its per-architecture hashes are unverified, so the image stays pinned to `0.34.0`.
