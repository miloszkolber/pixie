# Pi integration

The release assistant is the Go `pixie-assistant` binary. It starts the selected public `pi` executable in RPC mode and does not embed an SDK or require Bun at runtime. Pi owns provider credentials, models, settings and native JSONL sessions under the selected agent directory, normally `~/.pi/agent`.

The opt-in administration bridge in `assistant/bridge/` runs under Bun when an operator selects it, resolves exactly one installed Pi SDK, and serves a bounded sidecar protocol to the Go host. The Go assistant does not embed an SDK or require Bun at runtime.

## Feature ownership

The table records the intended owner, not proof that the Go adapter currently exposes every operation. Unimplemented operations stay absent or false and fail closed; the Go gaps in the [roadmap](../roadmap/README.md#confirmed-defects-and-integration-risks) remain open.

| Feature | Implementation |
| --- | --- |
| Chat, streaming, cancellation, steering, images, compaction, forks | Native Pi execution, projected by Pixie |
| Run settlement, retry, compaction and lifecycle annotations | Native Pi events; Go separates acceptance from settlement and returns the terminal reason, pending real-provider event-order evidence |
| Extension dialogs (`select`, `confirm`, `input`, `editor`) | Generic host UI bridge, projected by Pixie; pending dialogs replay on reload |
| Extension status, widget, title, working-message hints | Generic host projections, fanned out by Pixie; terminal-only interfaces stay unavailable |
| Providers, API keys, OAuth, models, defaults, thinking | Native Pi model/auth/settings APIs; secrets stay on the host |
| Project grouping, file attachments, history search, durable follow-ups | Pixie records and transcript projection |
| Defined agents and delegation | Pixie authoring API for native Markdown definitions, optional native subagent extension for execution |
| Plans | Upstream `todo` tool via the `rpiv-todo` extension |
| MCP tools, Browser MCP | Operator-installed upstream MCP adapter; Pixie registers the chosen browser endpoint and Pi dials it directly |
| Signet | Operator-owned external service with a Pi-native file extension; no Pixie MCP connection |
| Goals, tasks and questions | Pixie session-scoped MCP |
| Schedules | Pixie storage and runner; ordinary Pi sessions, no Pi scheduling extension |

## Assistant protocol

The wire contract between the controller and the Pi host service. The host translates these frames onto native Pi APIs; the protocol is the stable surface that keeps the host service swappable.

**Transport.** The service listens on loopback `/pi` over WebSocket. Requests carry `Authorization: Bearer <PIXIE_PI_SECRET_KEY>`; connections without a valid bearer are rejected, and browser `Origin` headers are refused. Frames are JSON text. The host bounds frames and pending requests, and unsupported operations are absent or false and fail closed. Stable authority feeds deletion binding; production host v2 and durable pairing remain roadmap work.

**Welcome.** The first request is `runtime.hello`:

```json
{ "id": 1, "method": "runtime.hello", "params": { "protocolVersion": 1 } }
```

The result carries `protocolVersion` (`1`), a stable `runtimeId`, a fresh `bootId` for the current host process, the host's reported `version`, and a capability map. `sessions`, `providers` and `agents` are always `1`; optional feature groups (for example `mcp`, `llama`) appear only when a supported native runtime is loaded. Clients must check capability versions before using their methods.

**Frames.** Client requests are `{ "id": <positive safe integer>, "method": string, "params": object }`; the first request must be `runtime.hello` with `params.protocolVersion` set to `1`. The host does not coerce IDs or params. Replies are `{ "id", "result" }` or `{ "id", "error": { "code", "message" } }`. Events carry a `method` and `params` without an id; `session.event` frames carry `sessionId` and a monotonically increasing `sequence` used by snapshot checkpoints.

- Malformed JSON closes the connection with `1007`.
- A non-object envelope, non-number id, empty/non-string method, non-object params or invalid/missing first hello closes the connection with `1008`; an id that is not a positive safe integer also closes with `1008`.
- A reused in-flight id answers with an error frame (code `-32000`) and keeps the connection open.
- Frames above 32 MiB close the connection with `1009`; a consumer slower than a 32 MiB send or session-attachment buffer closes with `1013`.
- More than 128 pending requests per connection answer with a `-32000` error frame.
- `runtime.hello` has a 10-second deadline; provider/extension/default/preference administration has a 30-second deadline; interactive provider authentication retains a 10-minute deadline. A service drain shares a 25-second deadline across construction, administration, extensions, in-flight dispatches and teardown.
- Operation failures answer with a `-32000` error frame unless the failure carries a specific code (for example `-32002` for an unknown or ambiguous session).
- An event payload that cannot be serialized degrades to a `host.unserializable` stub with `sessionId` and `sequence` preserved; unserializable replies degrade to an error frame.

**Methods.**

| Group | Shape |
| --- | --- |
| `runtime.hello`, `runtime.capabilities` | Service identity, protocol version and capability versions |
| `runtime.restart` | End the process for the service manager; enabled per deployment with `PIXIE_ALLOW_SELF_RESTART=1` and rejected otherwise. The accepted request blocks new work, replies `ok`, and the production entrypoint drains for up to 25 seconds before exiting with status 75 ([deployment](deployment.md)) |
| `pi.extensions.list` / `configure`, `pi.sources.*` | Native resource inventory, deferred configuration and Markdown agent definitions |
| `pi.providers.*`, `pi.defaults.*`, `pi.preferences.*`, `provider.login*` | Provider catalog, credentials and OAuth flows; secrets never leave the host |
| `session.create` / `fork` / `load` / `list` / `prompt` / `steer` / `abort` / `queue*` / `delete` / `rename` / `archive` / `setModel` / `setThinkingLevel` and related | Session lifecycle, runs and configuration |
| `session.goal*`, `session.plan*`, `session.stats`, `session.commands`, `session.agentMentions` | Application projections on top of native sessions |
| Capability methods (`mcp.*`, `llama` feature surface) | Versioned groups advertised in the welcome capabilities |

Unknown methods return an error frame. Features are gated by capability versions, not assumed.

**Versioning.** `protocolVersion` changes only for breaking wire changes. Within a version the protocol is additive: new methods, fields and capability groups join without a bump, and optional features stay behind capability versions. The controller negotiates at `runtime.hello` and refuses incompatible hosts.

**Projection and lifecycle.** The Go host projects transcript, tool, usage, run, lifecycle, UI, history, dialog, and attachment state where implemented. It owns one immutable child per logical session in the admitted cwd, verifies exact identity before every operation, blocks prompt until settlement, and advertises only negotiated capabilities. Its transcript projection remains narrower than the full retained feature set; do not infer complete parity from source repairs.

The Go host keeps a durable session registry, reloads an exact session file on demand, and degrades readiness while a lost session awaits reload. Production integration remains subject to the roadmap audit.

| Native event or entry | Pixie presentation |
| --- | --- |
| Message start/delta/end, tool execution updates | Transcript, tool activity and final usage |
| Compaction, automatic retry, summarization retry | Existing progress, retry and completion states |
| Compaction/branch summary, visible custom message | Persistent summary or notice row |
| Model, thinking and session metadata changes | Session controls and title |
| Saved plan entries and plan tool results | Session plan |
| Hidden custom messages and internal entries | Kept by Pi; omitted from the displayed transcript |
| Agent/turn bookkeeping | Pi internal; Pixie uses host run boundaries for completion |

Use separate sessions for simultaneous Pi CLI and host work. Pi does not coordinate concurrent writes to the same session across processes. The host takes an exclusive lock for its own agent-directory service.

## Extensions

The host loads ordinary Pi resources through the native resource loader. Install and configure extensions through Pi. File/package/project resources do not need a Pixie-specific marker to execute. Optional web administration requires a supported API; native tools and supported `ctx.ui` calls do not require package-specific wrappers.

Native project trust controls project resources. User/global resources load under the native configuration. Saved trust or resource changes apply on a subsequent native load; do not assume changing configuration changes an already resident session.

Extension registration adds services and tools; it does not replace prompts, intercept tools or add execution policies. TUI-specific extension interfaces are not rendered in the Web UI. Optional controls require supported versions and an exhaustive negotiated operation set.

**Inventory and configuration.** Settings → Extensions distinguishes configured resources from extensions loaded in a resident session. Missing sources stay visible; inspection does not install packages, import extension code or create/reload a session. Non-resident sessions have no live loaded inventory. Loaded versions/interface support remain unknown when native metadata does not supply them.

Browser `pi.nativeExtensions` maps to host `pi.extensions.list`; `pi.nativeExtensionConfigure` maps to `pi.extensions.configure`. The administration bridge uses native resolver/settings APIs, a scoped resource key and expected revision. Confirmed changes preserve unrelated settings and resource filters. Unsupported changes, malformed targets and stale revisions are rejected. Static CLI resources such as `--llama` are not editable here. Native extension inventory is separate from MCP connection administration; the similarly named `pi.config.extensions.*` and `pi.session.extensions.*` methods concern MCP connections, not this resource inventory.

A successful save reports `saved=true`, `loaded=false` and `reload=deferred`. Refresh inventory before retrying an uncertain save. A subsequent load failure is reported separately and does not silently roll back the saved configuration. Reopen the session or restart the configured host service to apply changes. Per-session hot reload is not exposed. The [roadmap](../roadmap/README.md) covers the replacement runtime and compatibility rules.

**Web UI bridge.** The host maps `select`, `confirm`, `input`, `editor` and `notify`, plus text status/widgets, transient title and working-message hints. Blocking requests are scoped to session and request ID, settle once, and expire within 30 minutes. Select returns an offered string, confirm a boolean and text dialogs preserve text; dismissal returns the native cancellation value.

Pending requests replay on session load. Browser replies use `session.uiReply`/`session.uiCancel`; the controller calls host `session.uiResponse`/`session.uiCancel`. All clients dismiss a settled request. History questionnaire cards are read-only tool-result recaps. Without a reliable tool-call association, active requests stay session-level rather than being attached by guessed timing or tool name.

Each session permits 16 pending dialogs. Passive status/widget collections each permit 16 keys; widgets accept string arrays, not component factories. Text is escaped, ordinary passive updates are bounded, and clears remain deliverable. Passive state currently clears on connection/context loss and is not replayed. A transient extension title does not rename the saved conversation.

Terminal input, custom TUI factories, footers/headers, autocomplete and composer get/set/paste APIs are unsupported. The multiline editor dialog has its own draft. Stop and session teardown cancel pending interactions. Generic extension liveness can keep background work resident without redefining native run settlement.

The Go host implements the blocking dialogs through raw native `extension_ui_request`/`extension_ui_response` frames; the [retained feature index](../roadmap/README.md#retained-feature-index) records required behavior and the [integration findings](../roadmap/README.md#confirmed-defects-and-integration-risks) record unresolved fidelity work. Do not assume every bridge method is available.

**Agent definitions.** Definitions live in `<agentDir>/agents/*.md` and `<project>/.pi/agents/*.md`. Pixie's `pi.sources.*` API provides Markdown CRUD and `@agent` discovery without registering model tools or implementing delegation.

Frontmatter includes `name`, `description` and optional `model`; unspecified fields are preserved. Names accept letters/numbers/spaces/underscores/hyphens up to 80 UTF-8 bytes. Complete files are limited to 64 KiB. Invalid definitions produce diagnostics. Editability does not establish execution eligibility: discovery, trust, children and execution belong to the installed native extension.

## MCP

**Native Pi MCP client.** The operator-installed native adapter is the only Pi MCP runtime. The opt-in administration bridge discovers the public runtime-snapshot interface and registers session connections through runtime-register APIs. Native tools remain the model-facing interface. The Pi MCP client uses the pinned upstream `pi-mcp-adapter` runtime unchanged, with no custom transport.

Identical attachments are idempotent; conflicting definitions fail. Global saved connections and session membership are distinct. Native `{mcpServers: ...}` configuration is not rewritten by the bridge's connection administration. Without a compatible adapter, MCP administration stays unavailable and baseline sessions remain usable.

**Pixie MCP publisher.** The main Pixie process publishes its workspace modules to trusted MCP clients on the application listener. It is separate from the Pi MCP client and from the external browser endpoint.

| Endpoint | Purpose |
| --- | --- |
| `/mcp/canvas`, `/mcp/design` | Workspace-module Streamable HTTP MCP surfaces |
| `/api/mcp/modules` | Authenticated module catalog, schema version `1`, engine `in-process` |
| `/api/mcp/status` | Authenticated build and catalog status |
| `/health`, `/livez` | Process liveness (application listener) |
| `/readyz` | Application readiness, `200` or `503` |

Requests use `Authorization: Bearer <PIXIE_MCP_TOKEN>`. The catalog contains module IDs, names, paths, transport, state and an opaque revision. Canvas and Design are the registered modules and both default to disabled. `PIXIE_MCP_MODULES` and `PIXIE_MCP_DISABLED_MODULES` are retired and ignored; setting either logs a startup warning pointing at the Tools UI. Publication enablement lives in the Pixie persist store (`mcp-modules.json`) and in the Tools UI in-process section (Enabled, Status, Endpoint), exposed as `mcpRegistry.catalog` / `mcpRegistry.moduleSetEnabled`. The `mcpAdapter.status` projection surfaces the Pi-side adapter state (connected, cached, failed, needs-auth, not-connected, disabled) and stays fail-open when the adapter is not loaded.

**Browser MCP endpoint.** Pixie hosts no browser. Pi is the MCP client and connects directly to an operator-chosen external browser MCP endpoint. Pixie stores one setting in `config.json`, registers it in Pi's effective `mcpServers` configuration and reports a bounded probe; the Settings → Browser section exposes name, URL, an enabled toggle, register/update and remove. The endpoint may be unauthenticated, and hardening, network isolation and egress belong to the deployment; Pixie never proxies MCP traffic and fails closed when the setting is disabled or Pi administration is unavailable. See [security](security.md) for the trust boundary.

## Local models and memory

Local llama.cpp is an optional native Pi feature. The operator selects it through the installed Pi distribution and supplies `LLAMA_BASE_URL` and optional native credentials/`LLAMA_API_KEY`; the supervised child receives those native names. Model selection, refresh and inference run through Pi; `/llama` management requires terminal UI. A requested profile fails explicitly when its required provider is unavailable.

Signet is operator-owned and loads through Pi's normal file-extension discovery. Its daemon, configuration and enablement are not managed by Pixie and are not a Pixie MCP connection. Other unfamiliar native extensions follow the same native loading and supported UI boundaries.

See [deployment](deployment.md) for host installation and [security](security.md) for the trust boundary.
