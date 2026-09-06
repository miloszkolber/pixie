# Pi extensions

The host starts with normal Pi resources and no bundled factories enabled. Add `--extensions mcp,agents,rpiv-todo,web` to select optional factories. These are ordinary Pi extensions; Pixie checks their service contracts in the selected project or session before exposing related controls.

| Extension | Added capability |
| --- | --- |
| `mcp` | Configurable MCP tools, resources and App metadata; [standalone package](../pi/mcp/README.md) |
| `agents` | Markdown agent definition CRUD (`pi.sources.*`) and `@agent` mentions |
| `web` | Bounded HTTP(S) `web_fetch` tool |
| `rpiv-todo` | Upstream `todo` tool and `/todos` command |
| `rpiv-web` | Upstream `web_search` and `web_fetch`; Pi-native `web` replacement candidate |
| `rpiv-ask` | Upstream `ask_user_question` tool; Pi-native question replacement candidate |
| `signet` | Marker for the operator-installed Signet memory file extension; Pi-native memory candidate |
| `pi-subagent` | Upstream `subagent` tool; Pi-native delegation candidate |
| `pi-mcp-adapter` | Upstream `mcp` proxy tool; Pi-native MCP client candidate |
| `llama` | SDK built-in llama.cpp extension unchanged: local `llama.cpp` provider plus `/llama` command |

Use `rpiv-todo` with either `web` or `rpiv-web`. Both web profiles register `web_fetch`, and the first listed factory wins; the pair is mutually exclusive. The custom `web` extension remains available pending the [parity gate](roadmap.md).

Agent definitions live in `<agentDir>/agents/*.md` and `<project>/.pi/agents/*.md`. Frontmatter contains `name`, `description` and optional `model` (`provider/model` or a model ID within the inherited provider). The body supplies task instructions. Edits preserve unspecified frontmatter, including the model. Names use letters, numbers, spaces, underscores or hyphens (up to 80 UTF-8 bytes); each complete file is limited to 64 KiB. Invalid files produce diagnostics while valid definitions remain available. Delegation creates a separate native Pi session using the selected host extension profile.

## Extension UI bridge

The host binds every Pi session with a generic UI bridge, so extensions that ask the user through `ctx.ui` work from the Web UI without a terminal. The bridge supports `select`, `confirm`, `input`, `editor` and `notify`, plus ephemeral `setStatus`, `setWidget` (string arrays), `setTitle` and `setWorkingMessage` projections; terminal-only interfaces (raw terminal input, working visibility/indicator chrome, footers, headers, editor components, autocomplete, editor text, `custom` component factories) stay unavailable. Each blocking call carries a request ID, the Pi session ID, the primitive type and the payload, and settles exactly once with the user's answer, a cancellation, or a timeout (30 minutes).

Dialog state stays scoped to the originating Pi session: answers for another session or an unknown request are rejected, and aborting or closing the session dismisses its pending dialogs. Pending dialogs survive re-attachment: the host re-publishes unresolved requests on `session.load` (deduplicated by request ID) and snapshots carry the pending set, so reconnecting and late browsers replay the same modal instead of orphaning the extension call. A request vanishes only when answered, cancelled, timed out, stopped, or destroyed. The mapping is `select` to a choice modal, `confirm` to a confirm dialog, `input` to a single-line field, `editor` to a multiline field, and `notify` to a toast; status and working hints feed the activity line, titles rename the chat, and string widgets are stored per key for future surfaces. The browser-to-controller methods are `session.uiReply` and `session.uiCancel`; the controller-to-host methods are `session.uiResponse` and `session.uiCancel`. Dismissing the modal answers cancelled. Richer browser questionnaires later use these same methods or the public event contracts below.

Stop unwinds blocked UI on every path (prompt cancel, archive, delete, shutdown): the controller dismisses the modal and the host settles the awaiting call, so no run hangs and no orphan modal survives. Extension-owned background work after prompt idle registers generic per-session liveness, which only delays inactive-projection eviction; explicit lifecycle operations still win. Child subagent sessions stay headless: their dialogs never open nested top-level modals in the parent, and progress arrives through normal tool events.

The `rpiv-ask` profile answers through this bridge: in RPC mode the upstream tool walks its questionnaire with sequential `select`/`input` dialogs (previews fold into the prompt title, multi-select accepts comma-separated numbers) and returns the same answer envelope as its terminal UI. The upstream questionnaire allows 1–4 questions with 2–4 options each and headers up to 16 characters. Pixie's application-level `ask_user_question` remains the writer until the [parity gate](roadmap.md) passes; both tools must not reach the model together once parity passes.

Public upstream events, transported by the host for future subscribers:

| Event | Payload |
| --- | --- |
| `rpiv:ask-user:prompt` | `questions` with `question`, `header`, `multiSelect` and `options` (`label`, `description`, `hasPreview`) |
| `rpiv:ask-user:blocked` | `active` while the questionnaire awaits input |

The `pi-subagent` profile registers the upstream `subagent` tool unchanged and owns child execution. Discovery reads the same Markdown files with richer frontmatter (`model`, `thinking`, `tools`, `noTools`, `inactivityTimeout`, `sessionPreference`, `sessionHint`); project definitions apply only when the project is trusted and override user ones. Calls share one shape for single and parallel runs with per-call model override, `empty` (default) or exceptional `parent` initial context, and an optional `session` handle for named persistent sessions; depth and cycle guards bound delegation. Progress updates and the final details project through the generic tool path onto the shared child-run card, which also renders parallel calls. The custom `delegate`/`list_agents` execution was removed after the subagent parity gate passed (live child run verified on the local model); the `agents` profile keeps only Markdown CRUD.

Pixie capabilities register through `pixie:capability:v1`; their operations are defined in `pi/host/src/capabilities.ts`. The MCP compatibility entry delegates to the upstream adapter profile and assumes no Pixie addresses, Browser service or Docker layout.

The `rpiv-web` search backend is operator-owned external configuration. Provider selection, API keys, and the SearXNG endpoint live in `~/.config/rpiv-web-tools/config.json` and provider environment variables (`WEB_SEARCH_PROVIDER`, `SEARXNG_URL`, per-provider `*_API_KEY`); the default is self-hosted SearXNG at `http://localhost:8080`. Pixie never reads or writes this config. The upstream `web_fetch` SSRF guard refuses loopback and private targets, so it cannot reach Docker-local loopback services; this protection is retained, not weakened.

Global MCP changes apply on subsequent session initialization. Session membership is stored separately. Removing a connection removes only its tools. Unavailable connections are reported without replacing Pi's core tools.

## Pi-native MCP client

The MCP profiles use the pinned upstream model tools, transports, discovery, auth and reconnect behavior. The model calls `mcp`; upstream's optional `mcpScript` follows native settings. Pixie's bridge adds administration and presentation without tool interception or an MCP transport. Session attachment registers proxy-only servers through the public runtime event. Identical attachments are idempotent, conflicting definitions fail closed, and idle reload restores membership and controller attachments. Explicit removal/addition replaces a managed definition. Browser-specific registration remains first-wins. Legacy administration does not rewrite native `{mcpServers: ...}` configuration. The host CLI aligns `PI_CODING_AGENT_DIR` with its selected agent directory and suppresses desktop viewer launching by default.

`mcp` and `pi-mcp-adapter` select the same runtime, and selecting both names loads it once. The custom transport is removed. The local Bun patch adds `readMcpResourceV1` and the separate `callMcpAppToolV1` host API through upstream's existing manager. App-only tools stay absent from the model catalog and proxy, while App-origin calls enforce App visibility and upstream configured approvals. The controller selects the App method after AppView/project/session and same-server checks. Hidden native entries retain trusted App presentation metadata across reload without altering the model's tool result. The bridge advertises complete `mcp`/`mcp-apps` capabilities and `mcp-app-tools` for origin-aware calls, with old-host protocol fallback in the controller. [MCP client verification](mcp-client-verification.md) records the patch scope and reproducible checks.

## Local llama.cpp provider

The `llama` profile loads the Pi SDK's own built-in llama.cpp extension unchanged: it registers the local `llama.cpp` provider (OpenAI-completions against `LLAMA_BASE_URL`) plus the `/llama` model-management command, with an additive `llama` marker. Model selection keeps flowing through `pi.providers.*` and `session.configure`. The operator gives the host `LLAMA_BASE_URL` (service `Environment=` or equivalent); auth uses the stored `llama.cpp` credential when present and otherwise falls back to `LLAMA_API_KEY` or the dummy key `local`, which llama.cpp ignores. The `/llama` management command itself renders through Pi TUI components and needs an interactive terminal; provider registration, catalog refresh, and inference work headless.

## Signet memory

Signet loads through Pi's own `<agentDir>/extensions` discovery once the operator installs it (`signet setup` writes the managed `signet-pi.js` file). The `signet` profile only advertises an additive marker: importing the managed file as well would register its tools twice. The profile adds `signet_recall`, `signet_source_search`, `signet_session_search` and `signet_remember` with daemon lifecycle hooks (session start, prompt submit, session end, compaction) that stay fail-open while the daemon is unreachable, and auto-recall arrives as hidden context that never enters the transcript. The daemon endpoint (`SIGNET_DAEMON_URL`, default `http://127.0.0.1:3850`), `signet.json` (`{enabled}`) and per-session `SIGNET_ENABLED=false` are operator-owned external service state; Pixie never reads or writes them and connects no MCP for Signet. The MCP `signet` connection was removed after the parity gate passed (live recall through the managed extension verified against the running daemon); Pixie connects no MCP for Signet.
