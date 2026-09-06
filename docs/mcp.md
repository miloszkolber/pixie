# Pixie MCP service

`pixie-mcp` hosts the Browser module for trusted MCP clients. It is separate from the universal [Pi MCP client extension](../pi/mcp/README.md).

| Endpoint | Purpose |
| --- | --- |
| `/browser` | Browser Streamable HTTP MCP |
| `/v1/mcp/modules` | Authenticated Pixie module catalog, schema version `1` |
| `/v1/mcp/status` | Authenticated build and catalog status |
| `/health`, `/livez` | Process liveness |
| `/readyz` | Authenticated module readiness, `200` or `503` |

Requests use `Authorization: Bearer <PIXIE_MCP_TOKEN>`. The catalog contains module IDs, names, paths, transport, state and an opaque revision. Browser uses ID `browser`, connection name `pixie-browser` and path `/browser`. The host also serves Browser HTTP, artifact and `/mcp` compatibility routes.

`PIXIE_MCP_MODULES` defaults to `browser`; `PIXIE_MCP_DISABLED_MODULES` subtracts modules. Unknown or duplicate IDs reject startup. Pixie's Tools toggle changes Pi connection configuration; publication remains controlled by the MCP service environment.

Browser provides `browser_command`, `browser_guidance` and `pixie://browser/guide`. It limits sessions to 16, artifacts to 64 MiB per session and 256 MiB total, and commands to 120 seconds. Controller-owned panels have five-minute leases, renewed every minute; abandoned panels are cleaned up. Ordinary MCP client sessions remain the caller's responsibility.

Keep browser session IDs short: at most 28 characters with the default storage roots. Chromium's singleton socket inherits the state path length, and longer IDs exceed its limit at launch.

A ready service has not necessarily launched Chromium. Verify browsing by opening a disposable panel, navigating, taking a screenshot and closing it. For failures check the configured host/port, token, state ownership, container logs and authenticated readiness. Keep tokens out of command-line arguments and never forward them through redirects.

Modules share the Browser service's credentials and storage boundary. Additions require a compiled factory in `internal/mcphost/host.go` and tests for publication, routing, readiness, authorization and shutdown.

## In-process publisher (dual-run)

The controller additionally publishes Browser itself on its own listener, alongside (not instead of) the separate host:

| Endpoint | Purpose |
| --- | --- |
| `/mcp/browser` | Browser Streamable HTTP MCP, same tools and resources as the separate host |
| `/api/mcp/modules` | Authenticated in-process module catalog, schema version `1`, engine `in-process` |
| `/api/mcp/status` | Authenticated build and catalog status |

Enablement lives in the Pixie persist store (`mcp-modules.json`) and the Tools UI in-process section (Enabled, Status, Endpoint), or `mcpRegistry.catalog` / `mcpRegistry.moduleSetEnabled`. `PIXIE_MCP_MODULES` and `PIXIE_MCP_DISABLED_MODULES` remain only the fallback default and are deprecated pending parity: persisted enablement wins once the operator toggles a module. Browser is the only module; Signet, Web, Todo, Questions, and Subagents are never published through Pixie MCP. Browser storage stays isolated under the controller data directory, and switching publisher engines does not alter the model-facing API (`pixie-browser`, same tools and resource surface). The `mcpAdapter.status` projection surfaces the Pi-side adapter state (connected, cached, failed, needs-auth, not-connected, disabled) and stays fail-open when the adapter profile is not enabled.

## Browser engine

The Browser engine preference lives in Pixie app state (`browser.json`, default `chromium`). The in-process publisher honors it; the separate host follows only its environment. Setting `obscura` requires `PIXIE_BROWSER_CDP` (a port such as `9222` or `host:port`, matching `obscura serve --port 9222`); without it the module degrades instead of silently using Chromium. While `chromium` is selected, a `PIXIE_BROWSER_CDP` override is ignored. Switching engines does not alter the model-facing API. There is no Tools UI toggle yet; see the [roadmap](roadmap.md).

Chromium is the default and preferred engine. Verification runs agent-browser `0.34.0` against headless Chromium through the same executor the MCP tools share (`tests/go/browser/chromium_parity_test.go` with `PIXIE_BROWSER_LIVE=1`): connect, navigate, snapshot with refs, click, fill, type, press, scroll, wait, form login, large DOM, infinite scroll, iframe, JS-heavy pages, screenshots with artifacts, close with storage removal, and policy rejection of `eval`/`pdf`/`download`. Typical latencies are ~600 ms for a cold open (new Chromium), 10–30 ms for snapshot and actions, ~60 ms for screenshots, and ~110 ms for close. Reopening within seconds of close can fail while the previous daemon winds down; retry once. The `0.36.0` release exists but its per-architecture hashes are unverified, so the image stays pinned to `0.34.0`.

Obscura was evaluated from its published interface and rejected as the preferred backend: upstream `h4ckf0r0day/obscura` documents its CDP API as Target, Page, Runtime, DOM, Network, Fetch, IO, Storage, Input, and LP (`getMarkdown`) domains, with no Accessibility domain. Agent-browser snapshots and stable element references (`[ref=eN]`) require CDP Accessibility, so Obscura cannot satisfy the snapshot/ref path that every Browser tool call depends on. Its independents strengths (rendering, screenshots, PDF, markdown extraction) do not cover this gap. Chromium therefore stays both default and preferred; the `obscura` engine value and `PIXIE_BROWSER_CDP` override remain for experimentation only, and the engine switch still never alters the model-facing API.
