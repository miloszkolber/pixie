# Pi extensions

The host starts with normal Pi resources and no bundled factories enabled. Add `--extensions mcp,agents,plans,web` to select optional factories. These are ordinary Pi extensions; Pixie checks their service contracts in the selected project or session before exposing related controls.

| Extension | Added capability |
| --- | --- |
| `mcp` | Configurable MCP tools, resources and App metadata; [standalone package](../pixie/pi-mcp/README.md) |
| `agents` | Markdown agent definitions, `list_agents` and `delegate` |
| `plans` | Persistent `update_plan` tool |
| `web` | Bounded HTTP(S) `web_fetch` tool |

Agent definitions live in `<agentDir>/agents/*.md` and `<project>/.pi/agents/*.md`. Frontmatter contains `name`, `description` and optional `model` (`provider/model` or a model ID within the inherited provider). The body supplies task instructions. Edits preserve unspecified frontmatter, including the model. Names use letters, numbers, spaces, underscores or hyphens (up to 80 UTF-8 bytes); each complete file is limited to 64 KiB. Invalid files produce diagnostics while valid definitions remain available. Delegation creates a separate native Pi session using the selected host extension profile.

Pixie capabilities register through `pixie:capability:v1`; their operations are defined in `pi-host/src/capabilities.ts`. The independent MCP package emits `pi-mcp:service:v1`; the host adapts it to Pixie's protocol. It has no dependency on Pixie's addresses, credentials, Browser service or Docker deployment.

Global MCP changes apply on subsequent session initialization. Session membership is stored separately. Removing a connection removes only its tools. Unavailable connections are reported without replacing Pi's core tools.
