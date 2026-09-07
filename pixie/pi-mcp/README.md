# Pi MCP extension

An ordinary Pi extension for stdio, Streamable HTTP and SSE MCP servers. It requires no Pixie service, Browser module, container layout or fixed address.

After installing this workspace's dependencies, load the extension in vanilla Pi:

```sh
pi -e /absolute/path/to/pixie/pixie/pi-mcp/src/index.ts
```

Create `<Pi agent directory>/mcp.json`, normally `~/.pi/agent/mcp.json`. Connection names use letters, digits, hyphens and single underscores. The file maps names to settings:

```json
{
  "local": {
    "type": "stdio",
    "command": "/absolute/path/to/mcp-server",
    "args": [],
    "env": { "SERVICE_KEY": "${SERVICE_KEY}" },
    "cwd": "/absolute/path/to/work"
  },
  "remote": {
    "type": "http",
    "url": "https://your-server.example/mcp",
    "headers": { "Authorization": "Bearer ${SERVICE_TOKEN}" }
  }
}
```

`type` accepts `stdio`, `http` (or `streamable_http`) and `sse`. `enabled` defaults to true. Stdio inherits the host environment, then applies `env`; it starts the executable directly, without a shell. Relative `cwd` resolves from the agent directory. Commands, arguments, environment values and headers support `${ENV_NAME}`. Missing variables reject that connection. Keep this file private.

Tools are named `<connection>__<tool>`. Text, images, structured results and MCP App metadata are retained. Tool errors remain errors. Removing a connection affects only its tools. Connections load on session initialization; unavailable servers leave Pi's native tools usable. Optional host-managed membership is stored in `mcp-sessions.json` beside the configuration.

The extension emits `pi-mcp:service:v1` for compatible host integrations. Vanilla Pi needs no listener. Pixie's separate SDK host supplies its own adapter and service connections.
