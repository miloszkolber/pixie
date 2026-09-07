# Pixie MCP service

`pixie-mcp` hosts the Browser module for trusted MCP clients. It is separate from the universal [Pi MCP client extension](../pixie/pi-mcp/README.md).

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

A ready service has not necessarily launched Chromium. Verify browsing by opening a disposable panel, navigating, taking a screenshot and closing it. For failures check the configured host/port, token, state ownership, container logs and authenticated readiness. Keep tokens out of command-line arguments and never forward them through redirects.

Modules share the Browser service's credentials and storage boundary. Additions require a compiled factory in `internal/mcphost/host.go` and tests for publication, routing, readiness, authorization and shutdown.
