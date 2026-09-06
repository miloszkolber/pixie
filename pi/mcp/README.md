# Pi MCP compatibility entry

`@pixie/pi-mcp` is a small compatibility entry for persisted package and file-extension imports. It loads the pinned `pi-mcp-adapter` profile from this workspace. There is no custom MCP transport, protocol engine or per-tool registration implementation here.

After installing workspace dependencies, the existing vanilla Pi entry remains valid:

```sh
pi -e /absolute/path/to/pixie/pi/mcp/src/index.ts
```

The model uses upstream's `mcp` proxy:

```js
mcp({ connect: "remote" })
mcp({ describe: "remote_echo" })
mcp({ server: "remote", tool: "echo", args: { text: "hello" } })
```

Upstream's optional `mcpScript` follows native settings. Text, images, structured results, resources, OAuth/bearer mechanics, discovery and reconnect remain upstream-owned. The extension assumes no Pixie address or Browser service.

## Existing configuration

Existing `<agentDir>/mcp.json` bare connection maps and `mcp-sessions.json` memberships remain readable. Pixie's connection administration continues to maintain this legacy shape with locked atomic writes:

```json
{
  "local": { "type": "stdio", "command": "/absolute/path/to/server", "args": [], "env": { "SERVICE_KEY": "${SERVICE_KEY}" }, "cwd": "/absolute/path/to/work" },
  "remote": { "type": "http", "url": "https://your-server.example/mcp", "headers": { "Authorization": "Bearer ${SERVICE_TOKEN}" } }
}
```

Names use letters, digits, hyphens and single underscores. Legacy `enabled` defaults to true. `stdio`, `http`, `streamable_http` and `sse` are accepted. Relative legacy `cwd` resolves from the selected agent directory. Keep configuration private. Invalid legacy entries remain visible and removable. Runtime registration is lazy, identical attachments are idempotent, and conflicting replacements fail closed. Remove and add a connection explicitly to replace its definition.

Native `{mcpServers: ...}` configuration and discovery remain upstream-owned and are not rewritten by the legacy administration methods. Native servers support the same host App APIs without a second registration. The host CLI aligns `PI_CODING_AGENT_DIR` with `--agent-dir` and defaults `MCP_UI_VIEWER` to `none` so the Web UI service does not launch a desktop browser.

The host bridge advertises `mcp`, `mcp-apps`, `mcp-app-tools` and `pi-mcp-adapter`. Retained `<server>__<tool>` names are decoded only at the host protocol boundary, not registered as model tools. The [local upstreamable patch](../extensions/local-patches/README.md) supplies raw resource and App-origin tool APIs while preserving model visibility. See [verification](../../docs/mcp-client-verification.md).
