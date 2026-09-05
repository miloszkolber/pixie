# Pi extensions

The host starts with normal Pi resources and no bundled factories enabled. Add `--extensions mcp,agents,plans,web` to select optional factories. These are ordinary Pi extensions; Pixie checks their service contracts in the selected project or session before exposing related controls.

| Extension | Added capability |
| --- | --- |
| `mcp` | Configurable MCP tools, resources and App metadata; [standalone package](../pi/mcp/README.md) |
| `agents` | Markdown agent definitions, `list_agents` and `delegate` |
| `plans` | Persistent `update_plan` tool |
| `web` | Bounded HTTP(S) `web_fetch` tool |
| `rpiv-todo` | Upstream `todo` tool and `/todos` command; Pi-native `plans` replacement candidate |
| `rpiv-web` | Upstream `web_search` and `web_fetch`; Pi-native `web` replacement candidate |

Use either `plans` with `web`, or `rpiv-todo` with `rpiv-web`. Both web profiles register `web_fetch`, and the first listed factory wins; the pair is mutually exclusive. The custom `plans` and `web` extensions remain the defaults pending the [parity gate](roadmap.md).

Agent definitions live in `<agentDir>/agents/*.md` and `<project>/.pi/agents/*.md`. Frontmatter contains `name`, `description` and optional `model` (`provider/model` or a model ID within the inherited provider). The body supplies task instructions. Edits preserve unspecified frontmatter, including the model. Names use letters, numbers, spaces, underscores or hyphens (up to 80 UTF-8 bytes); each complete file is limited to 64 KiB. Invalid files produce diagnostics while valid definitions remain available. Delegation creates a separate native Pi session using the selected host extension profile.

Pixie capabilities register through `pixie:capability:v1`; their operations are defined in `pi/host/src/capabilities.ts`. The independent MCP package emits `pi-mcp:service:v1`; the host adapts it to Pixie's protocol. It has no dependency on Pixie's addresses, credentials, Browser service or Docker deployment.

The `rpiv-web` search backend is operator-owned external configuration. Provider selection, API keys, and the SearXNG endpoint live in `~/.config/rpiv-web-tools/config.json` and provider environment variables (`WEB_SEARCH_PROVIDER`, `SEARXNG_URL`, per-provider `*_API_KEY`); the default is self-hosted SearXNG at `http://localhost:8080`. Pixie never reads or writes this config. The upstream `web_fetch` SSRF guard refuses loopback and private targets, so it cannot reach Docker-local loopback services; this protection is retained, not weakened.

Global MCP changes apply on subsequent session initialization. Session membership is stored separately. Removing a connection removes only its tools. Unavailable connections are reported without replacing Pi's core tools.
