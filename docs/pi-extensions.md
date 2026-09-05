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
| `rpiv-ask` | Upstream `ask_user_question` tool; Pi-native question replacement candidate |

Use either `plans` with `web`, or `rpiv-todo` with `rpiv-web`. Both web profiles register `web_fetch`, and the first listed factory wins; the pair is mutually exclusive. The custom `plans` and `web` extensions remain the defaults pending the [parity gate](roadmap.md).

Agent definitions live in `<agentDir>/agents/*.md` and `<project>/.pi/agents/*.md`. Frontmatter contains `name`, `description` and optional `model` (`provider/model` or a model ID within the inherited provider). The body supplies task instructions. Edits preserve unspecified frontmatter, including the model. Names use letters, numbers, spaces, underscores or hyphens (up to 80 UTF-8 bytes); each complete file is limited to 64 KiB. Invalid files produce diagnostics while valid definitions remain available. Delegation creates a separate native Pi session using the selected host extension profile.

## Extension UI bridge

The host binds every Pi session with a generic UI bridge, so extensions that ask the user through `ctx.ui` work from the Web UI without a terminal. The bridge supports `select`, `confirm`, `input`, `editor` and `notify`; terminal-only interfaces stay unavailable. Each call carries a request ID, the Pi session ID, the primitive type and the payload, and settles exactly once with the user's answer, a cancellation, or a timeout (30 minutes).

Dialog state stays scoped to the originating Pi session: answers for another session or an unknown request are rejected, and aborting or closing the session dismisses its pending dialogs. The mapping is `select` to a choice modal, `confirm` to a confirm dialog, `input` to a single-line field, `editor` to a multiline field, and `notify` to a toast. The wire methods are `session.uiReply` and `session.uiCancel`; richer browser questionnaires later use these same methods or the public event contracts below.

The `rpiv-ask` profile answers through this bridge: in RPC mode the upstream tool walks its questionnaire with sequential `select`/`input` dialogs (previews fold into the prompt title, multi-select accepts comma-separated numbers) and returns the same answer envelope as its terminal UI. The upstream questionnaire allows 1–4 questions with 2–4 options each and headers up to 16 characters. Pixie's application-level `ask_user_question` remains the writer until the [parity gate](roadmap.md) passes; both tools must not reach the model together once parity passes.

Public upstream events, transported by the host for future subscribers:

| Event | Payload |
| --- | --- |
| `rpiv:ask-user:prompt` | `questions` with `question`, `header`, `multiSelect` and `options` (`label`, `description`, `hasPreview`) |
| `rpiv:ask-user:blocked` | `active` while the questionnaire awaits input |

Pixie capabilities register through `pixie:capability:v1`; their operations are defined in `pi/host/src/capabilities.ts`. The independent MCP package emits `pi-mcp:service:v1`; the host adapts it to Pixie's protocol. It has no dependency on Pixie's addresses, credentials, Browser service or Docker deployment.

The `rpiv-web` search backend is operator-owned external configuration. Provider selection, API keys, and the SearXNG endpoint live in `~/.config/rpiv-web-tools/config.json` and provider environment variables (`WEB_SEARCH_PROVIDER`, `SEARXNG_URL`, per-provider `*_API_KEY`); the default is self-hosted SearXNG at `http://localhost:8080`. Pixie never reads or writes this config. The upstream `web_fetch` SSRF guard refuses loopback and private targets, so it cannot reach Docker-local loopback services; this protection is retained, not weakened.

Global MCP changes apply on subsequent session initialization. Session membership is stored separately. Removing a connection removes only its tools. Unavailable connections are reported without replacing Pi's core tools.
