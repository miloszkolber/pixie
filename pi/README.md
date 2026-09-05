# Pi

Pi-specific source and configuration examples live here.

- `host/`: Bun service using the unmodified Pi SDK, with optional agents, plans and web extensions under `src/extensions/`.
- `mcp/`: standalone [MCP extension](mcp/README.md), including connection configuration examples.

From the repository root:

```sh
bun install --frozen-lockfile --production --filter @pixie/pi-host --filter @pixie/pi-mcp
bun pi/host/src/main.ts --extensions mcp,agents,plans,web
```

Set `PIXIE_PI_SECRET_KEY` before starting the host. Omit `--extensions` for baseline Pi. Runtime configuration and transcripts use Pi's agent directory, normally `~/.pi/agent`; `--agent-dir` selects another location. See [deployment](../docs/deployment.md) for host and container setup.
