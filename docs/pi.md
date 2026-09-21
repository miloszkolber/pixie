# Pi integration

`pixie_assistant` is the archive-internal host bundle `libexec/pixie_assistant.js`, run by the pinned Bun `1.4.2` runtime `runtime/bin/bun`, used by the `pixie` host product. It runs Pi sessions in-process through the bundled `@earendil-works/pi-coding-agent` SDK at version `0.86.1`. There is no `pi --mode rpc` child model, bridge sidecar, external Pi discovery, or public `pixie_assistant` command. Pi owns provider credentials, models, settings, native JSONL sessions, tools, extensions, and trust under the selected agent directory, normally `~/.pi/agent`.

`pixie` bundles Bun `1.4.2`, the Pi SDK and host; no Node runtime is bundled. Bare `pixie` is the regular native Pi command and TUI, excludes Pi RPC, and blocks Pi self-update. The root `pixie` command opens Pi's native TUI through `runtime/bin/bun runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js`. `pixie_web` remains controller-only, never contains or starts Pi, and uses the host's authenticated loopback protocol rather than a Pi execution fallback.

## Feature ownership

The table records ownership, not universal API availability. The host returns a complete operation set with `runtime.hello`; the controller gates a negotiated false or missing operation before calling it. A handled route can still fail with a capability error when the selected public Pi API lacks the required member. See the [SDK coverage inventory](sdk-coverage.md).

| Feature | Implementation |
| --- | --- |
| Chat, streaming, cancellation, images, compaction, forks | Native Pi execution, projected by Pixie when the negotiated operation and selected public API support it |
| Steering | Not exposed by the current host: Pi `0.86.1` has no public run identifier that safely binds a steering request. Both `session.steer` and `pi.session.steer` are catalogued `unavailable` with that reason and fail closed |
| Run settlement, retry, compaction and lifecycle annotations | Native Pi events; the host separates acceptance from settlement and returns the terminal reason, pending real-provider event-order evidence |
| Extension dialogs (`select`, `confirm`, `input`, `editor`) | Generic host UI bridge, projected by Pixie; pending dialogs replay on reload |
| Extension status, widget, title, working-message hints | Generic host projections, fanned out by Pixie; terminal-only interfaces stay unavailable |
| Providers, API keys, OAuth, models, defaults, thinking | Native Pi model/auth/settings APIs where available; secrets stay on the host. A typed provider configuration provenance projection (`pi.providers.config.read`) reports field presence and a fixed source enum only, so settings can explain defaults or unknown before mutation, but provider configuration is not universally available |
| Project grouping, file attachments, history search, durable follow-ups | Pixie records and transcript projection |
| Defined agents and delegation | Pixie authoring API for native Markdown definitions, optional native subagent extension for execution |
| Plans | Upstream `todo` tool via the `rpiv-todo` extension |
| MCP tools, Browser MCP | Pixie manages its own configured connection records and registers the chosen browser endpoint; Pi dials the endpoint directly and no universal public Pi MCP/tool API is assumed |
| Signet | Operator-owned external service with a Pi-native file extension; no Pixie MCP connection |
| Goals, tasks and questions | Pixie session-scoped MCP |
| Schedules | Pixie storage and runner; ordinary Pi sessions, no Pi scheduling extension |

## Assistant protocol

The wire contract between the controller and the Pi host service. The host translates supported frames onto public Pi APIs; it is not Pi RPC. The generated catalog (`schema/protocol-catalog.json`) is the single source of host operation names and their `available`/`unavailable`/`absent` status; the host advertises only `available` routes and every other catalogued route fails closed with its stated reason.

**Transport.** The service listens on loopback `/pi` over WebSocket. Requests carry `Authorization: Bearer <PIXIE_PI_SECRET_KEY>`; connections without a valid bearer are rejected, and browser `Origin` headers are refused. Frames are JSON text. The host bounds frames and pending requests, and unsupported operations are absent or false and fail closed. Stable authority feeds deletion binding; durable pairing remains roadmap work.

**Welcome.** The first request is `runtime.hello`:

```json
{ "id": 1, "method": "runtime.hello", "params": { "protocolVersion": 1 } }
```

The result carries `protocolVersion` (`1`), a stable `runtimeId`, a fresh `bootId` for the current host process, the host's reported `version`, `sessions` and `agents` capability groups, and an exhaustive `operationSet` derived from the catalog: an operation is true when the catalog marks it `available`, except `runtime.restart`, which also requires the deployment to enable it. When `PIXIE_PI_PROTOCOL` enables negotiation and version 2 is selected, the result instead carries `protocolVersion` (`2`), `supportedProtocolVersions`, `hostIdentity`, `bootId`, `nativeVersion`, and the same capability groups and exhaustive operation set. `runtime.hello` therefore cannot claim an unimplemented route, and the host never advertises the `tools` group. Clients must check the operation set before using optional methods.

**Frames.** Client requests are `{ "id": <positive safe integer>, "method": string, "params": object }`; the first request must be `runtime.hello` with `params.protocolVersion` set to `1`. The host does not coerce IDs or params. Replies are `{ "id", "result" }` or `{ "id", "error": { "code", "message" } }`. Events carry a `method` and `params` without an id; `session.event` frames carry `sessionId` and a monotonically increasing `sequence` used by snapshot checkpoints.

- Malformed JSON closes the connection with `1007`.
- A non-object envelope, non-number id, empty/non-string method, non-object params or invalid/missing first hello closes the connection with `1008`; an id that is not a positive safe integer also closes with `1008`.
- A reused in-flight id answers with an error frame (code `-32000`) and keeps the connection open.
- Frames above 32 MiB close the connection with `1009`; a consumer slower than a 32 MiB send or session-attachment buffer closes with `1013`.
- More than 128 pending requests per connection answer with a `-32000` error frame.
- `runtime.hello` has a 10-second deadline; provider/extension/default/preference administration has a 30-second deadline; interactive provider authentication retains a 10-minute deadline. A service drain shares a 25-second deadline across construction, administration, extensions, in-flight dispatches and teardown.
- Operation failures answer with a `-32000` error frame unless the failure carries a specific code (for example `-32002` for an unknown or ambiguous session).
- An event payload that cannot be serialized degrades to a `host.unserializable` stub with `sessionId` and `sequence` preserved; unserializable replies degrade to an error frame.
- **Native error boundary (partial).** Request failures are normalized at both boundaries: the host redacts and bounds the native cause before replying (`src/assistant/host.ts`), and the controller maps a host error code to a fixed browser message (`piprotocol/host_errors.go`, `internal/controller/websocket.go`) while logging only the redacted cause. Native update text carried inside session events is normalized at the controller boundary before persistence or projection: the known error-bearing fields and native status messages are redacted and bounded, and the native `agent_end` message array is replaced by a bounded stop/retry summary and dropped at that boundary (`internal/controller/session_events.go`, `internal/controller/pi_events.go`, `tests/go/controller/session_native_error_boundary_test.go`). The normalization is field-allowlisted, so native event fields outside those handled cases still pass through unchanged.

**Methods.**

| Group | Shape |
| --- | --- |
| `runtime.hello` | Service identity, protocol version, capability groups, and negotiated operation set |
| `runtime.restart` | End the process for the service manager; enabled per deployment with `PIXIE_ALLOW_SELF_RESTART=1` and rejected otherwise. The accepted request blocks new work, replies `ok`, and the production entrypoint drains for up to 25 seconds before exiting with status 75 ([deployment](deployment.md)) |
| `pi.extensions.list` / `configure`, `pi.sources.*` | Native resource inventory, deferred configuration and Markdown agent definitions |
| `pi.providers.*`, `pi.defaults.*`, `pi.preferences.*`, `provider.login*` | Provider catalog, credentials, OAuth flows and a typed, secret-free configuration provenance projection (`pi.providers.config.read`); secrets never leave the host |
| `session.create` / `fork` / `load` / `list` / `prompt` / `cancel` / `configure` / `rename` and related | Session lifecycle, runs, and configuration where an operation is negotiated and its public Pi member exists |
| `session.goal*`, `session.plan*`, `session.stats`, `session.commands`, `session.agentMentions` | Application projections on top of native sessions |
| Capability methods (`mcp.*`, `llama` feature surface) | Versioned groups advertised in the welcome capabilities |

Unknown methods return an error frame. `runtime.capabilities` is intentionally unavailable, not missing: `runtime.hello` already returns the negotiated capability groups and the exhaustive operation set, and controller `pi.capabilities` is a derived view of `runtime.hello`; neither is a Pi runtime call. Features are gated by the negotiated operation set, not assumed.

**Versioning.** `protocolVersion` changes only for breaking wire changes. Within a version the protocol is additive: new methods, fields and capability groups join without a bump, and optional features stay behind capability versions. The controller negotiates at `runtime.hello` and refuses incompatible hosts. Protocol version 2 negotiation is opt-in through `PIXIE_PI_PROTOCOL` (`v1` is the default, `auto` negotiates and accepts a v1 host, `v2` requires version 2); the assistant host and the controller read the same variable, and an incompatible or inconsistent peer is rejected rather than downgraded. The opt-in surface is source-level only; live and credentialed version-2 behavior is not verified here.

**Projection and lifecycle.** The host projects transcript, tool, usage, run, lifecycle, UI, history, dialog, and attachment state where implemented. It verifies exact identity before every operation, blocks prompt until settlement, and advertises only negotiated capabilities. Its transcript projection remains narrower than the full retained feature set; do not infer complete parity from source repairs.

The host keeps a durable session registry, reloads an exact session file on demand, and degrades readiness while a lost session awaits reload. Production integration remains subject to the roadmap audit.

| Native event or entry | Pixie presentation |
| --- | --- |
| Message start/delta/end, tool execution updates | Transcript, tool activity and final usage |
| Compaction, automatic retry, summarization retry | Existing progress, retry and completion states |
| Compaction/branch summary, visible custom message | Persistent summary or notice row |
| Model, thinking and session metadata changes | Session controls and title |
| Saved plan entries and plan tool results | Session plan |
| Hidden custom messages and internal entries | Kept by Pi; omitted from the displayed transcript |
| Agent/turn bookkeeping | Pi internal; Pixie uses host run boundaries for completion |

Use separate sessions for simultaneous Pi CLI and host work. Pi does not coordinate concurrent writes to the same session across processes. The host takes an exclusive lock for its own agent-directory service, but host-local mutation cannot coordinate arbitrary external writers.

## Extensions

The host loads ordinary Pi resources through the native resource loader. Install and configure extensions through Pi. File, package and project resources do not need a Pixie-specific marker to execute. Optional web administration requires a supported API; native tools and supported `ctx.ui` calls do not require package-specific wrappers.

Native project trust controls project resources. User/global resources load under the native configuration. Saved trust or resource changes apply on a subsequent native load; do not assume changing configuration changes an already resident session.

Extension registration adds services and tools; it does not replace prompts, intercept tools or add execution policies. TUI-specific extension interfaces are not rendered in the Web UI. Optional controls require supported versions and an exhaustive negotiated operation set.

The host is not a Pi extension. An extension lives only inside a Pi process and runs under the TUI runtime; it cannot own a durable multi-session registry, provider/model/settings or MCP mutation, or deletion authority, and it would expose the controller secret to every loaded extension and the model's bash tool. A dedicated owner-locked host process remains required. An in-TUI extension can only ever be an optional additive surface that never owns sessions or the loopback endpoint. See the [bundled native interface](sdk-coverage.md#bundled-native-interface).

**Inventory and configuration.** Settings → Extensions distinguishes configured resources from extensions loaded in a resident session. Missing sources stay visible; inspection does not install packages, import extension code or create/reload a session. Non-resident sessions have no live loaded inventory. Loaded versions/interface support remain unknown when native metadata does not supply them.

Browser `pi.nativeExtensions` maps to host `pi.extensions.list`; `pi.nativeExtensionConfigure` maps to `pi.extensions.configure`. The host uses native resolver/settings APIs, a scoped resource key and expected revision. Confirmed changes preserve unrelated settings and resource filters. Unsupported changes, malformed targets and stale revisions are rejected. Static CLI resources such as `--llama` are not editable here. Native extension inventory is separate from MCP connection administration; the similarly named `pi.config.extensions.*` and `pi.session.extensions.*` methods concern MCP connections, not this resource inventory.

A successful save reports `saved=true`, `loaded=false` and `reload=deferred`. Refresh inventory before retrying an uncertain save. A subsequent load failure is reported separately and does not silently roll back the saved configuration. Reopen the session or restart the configured host service to apply changes. Per-session hot reload is not exposed. The [roadmap](../roadmap/roadmap.md) covers the replacement runtime and compatibility rules.

**Web UI bridge.** The host maps `select`, `confirm`, `input`, `editor` and `notify`, plus text status/widgets, transient title and working-message hints. Blocking requests are scoped to session and request ID, settle once, and expire within 30 minutes. Select returns an offered string, confirm a boolean and text dialogs preserve text; dismissal returns the native cancellation value.

Pending requests replay on session load. Browser replies use `session.uiReply`/`session.uiCancel`; the controller calls host `session.uiResponse`/`session.uiCancel`. All clients dismiss a settled request. History questionnaire cards are read-only tool-result recaps. Without a reliable tool-call association, active requests stay session-level rather than being attached by guessed timing or tool name.

Each session permits 16 pending dialogs. Passive status/widget collections each permit 16 keys; widgets accept string arrays, not component factories. Text is escaped, ordinary passive updates are bounded, and clears remain deliverable. Passive state currently clears on connection/context loss and is not replayed. A transient extension title does not rename the saved conversation. Terminal input, custom TUI factories, footers, headers, autocomplete, and composer APIs remain native-TUI-only.

The multiline editor dialog has its own draft. Stop and session teardown cancel pending interactions. Generic extension liveness can keep background work resident without redefining native run settlement.

The host implements the blocking dialogs through native `extension_ui_request`/`extension_ui_response` frames; the [roadmap](../roadmap/roadmap.md) records required behavior and the remaining fidelity work. Do not assume every bridge method is available.

**Agent definitions.** Definitions live in `<agentDir>/agents/*.md` and `<project>/.pi/agents/*.md`. Pixie's `pi.sources.*` API provides Markdown CRUD and `@agent` discovery without registering model tools or implementing delegation.

Frontmatter includes `name`, `description` and optional `model`; unspecified fields are preserved. Names accept letters/numbers/spaces/underscores/hyphens up to 80 UTF-8 bytes. Complete files are limited to 64 KiB. Invalid definitions produce diagnostics. Editability does not establish execution eligibility: discovery, trust, children and execution belong to the installed native extension.

## MCP

**Host MCP configuration.** The host keeps bounded configured and per-session connection records for supported HTTP/streamable-HTTP definitions. Global records and session membership are distinct. This is Pixie host policy, not evidence of a universal public Pi MCP runtime, attachment API, or tool inventory; baseline sessions remain usable when an optional connection capability is unavailable.

**Pixie MCP publisher.** The main Pixie process publishes its workspace modules to trusted MCP clients on the application listener. It is separate from the Pi MCP client and from the external browser endpoint.

| Endpoint | Purpose |
| --- | --- |
| `/mcp/canvas`, `/mcp/design` | Workspace-module Streamable HTTP MCP surfaces |
| `/api/mcp/modules` | Authenticated module catalog, schema version `1`, engine `in-process` |
| `/api/mcp/status` | Authenticated build and catalog status |
| `/health`, `/livez` | Process liveness (application listener) |
| `/readyz` | Application readiness, `200` or `503` |

Requests use `Authorization: Bearer <PIXIE_MCP_TOKEN>`. The catalog contains module IDs, names, paths, transport, state and an opaque revision. Canvas and Design are the registered modules and both default to disabled. `PIXIE_MCP_MODULES` and `PIXIE_MCP_DISABLED_MODULES` are retired and ignored; setting either logs a startup warning pointing at the Tools UI. Publication enablement lives in the Pixie persist store (`mcp-modules.json`) and in the Tools UI in-process section (Enabled, Status, Endpoint), exposed as `mcpRegistry.catalog` / `mcpRegistry.moduleSetEnabled`.

**Browser MCP endpoint.** Browser MCP is an external pointer, not a Pixie module or browser. Pi is the MCP client and connects directly to an operator-chosen endpoint. Pixie stores one setting in `config.json`, registers it in Pi's effective `mcpServers` configuration and reports a bounded probe; the Settings → Browser section exposes name, URL, an enabled toggle, register/update and remove. The endpoint may be unauthenticated, and hardening, network isolation and egress belong to the deployment; Pixie never proxies MCP traffic and fails closed when the setting is disabled or Pi administration is unavailable. See [security](security.md) for the trust boundary.

## Local models and memory

Local llama.cpp is an optional native Pi feature. When the bundled Pi runtime supports it, the operator supplies `LLAMA_BASE_URL` and optional native credentials/`LLAMA_API_KEY`; the native Pi session receives those native names. Model selection, refresh and inference run through Pi; `/llama` management remains native-TUI-only. A requested profile fails explicitly when its required provider is unavailable.

Signet is operator-owned and loads through Pi's normal file-extension discovery. Its daemon, configuration and enablement are not managed by Pixie and are not a Pixie MCP connection. Other unfamiliar native extensions follow the same native loading and supported UI boundaries.

## Observability and recovery

A supervised installation records a boot identity and a per-run identity. `PIXIE_BOOT_ID` is stable across restarts on the same host boot (or archive session) and `PIXIE_RUN_ID` is unique per launcher invocation; a directly launched process generates both. The entrypoint sanitizes the values against fixed identifier formats, stores them on the process, and the supervisor exports them to its managed children. Only opaque identifiers cross that boundary, never a path, token or endpoint (`internal/diagnostics/identity.go:37-176`).

Host diagnostics redact secret-shaped and path-shaped values at their emission boundary: bearer tokens, credential-shaped values, endpoint URLs, absolute paths and long opaque tokens are replaced before text is printed. Child stderr passes through one supervisor redaction boundary: `pixie serve` writes each managed child's stderr only into a bounded, redacted ring whose console mirror receives the same already-redacted lines, so raw child text never reaches the supervisor console or a retained ring. The ring keeps line, count, byte and age caps, emits that tail with the child-exit log, and points at it from the doctor remediation. The controller owns no child process, so no child-stderr summary crosses the controller boundary (`src/assistant/log.ts:47-70`, `src/assistant/serve.ts:73-76`, `internal/diagnostics/stderr.go:11-20`, `internal/diagnostics/stderr.go:160-163`, `cmd/pixie/main.go`).

The controller exposes a bounded, secret-free health history. It records at most 32 transitions across allowlisted components (`agent`, `application`, `schedule`) and stable state tokens only, and the support-export boundary re-sanitizes and bounds the same shape (`internal/controller/runtime.go:38-122`, `internal/diagnostics/transitions.go:8-10`).

`pixie_web doctor [--config ABS]` prints a read-only recovery report: a `summary=` value plus one line per check with a stable ID, status, code and fixed remediation text. The report carries no path, endpoint, credential or raw error (`internal/diagnostics/recovery.go`, `cmd/pixie-web/runtime.go`, `cmd/pixie-web/config.go`).

The support snapshot stays behind controller authentication and includes only bounded, already-sanitized fields: run identity, health transitions, runtime facts and controller request outcomes. It never collects the assistant or system logs wholesale, and it does not contain the supervisor-scoped child-stderr tail (`internal/diagnostics/support_snapshot.go:71-95`, `internal/controller/runtime.go:347-350`).

## Resilience and persistence

A Browser WebSocket connection is bounded. The controller tracks at most 64 identities — an active socket and a disconnected client still inside its replay-reap grace period both count — and a reconnect beyond a short burst is delayed by a per-identity attempt/backoff gate (six attempts per ten seconds, then a 1–30 second exponential penalty). Slow-client buffering is bounded to 256 queued items and 32 MiB per socket on top of a 64 MiB aggregate admission budget, and a socket that exceeds its lane is closed with a reconnect request. Disconnecting a socket releases its private replay namespace and aggregate reservations, so retained-response budget does not leak across reloads (`internal/controller/websocket.go:68-84`, `internal/controller/websocket.go:700-770`, `internal/controller/socket_output.go:33-34`, `internal/controller/socket_output.go:140-155`, `internal/controller/replay.go:177-190`).

Schedule deadlines use a process-local start reading that carries its monotonic clock component, so a wall-clock jump cannot shorten or extend a live run's deadline. A run recovered from disk has no monotonic reading and falls back to its persisted wall timestamp (`internal/controller/schedules.go:804-810`).

Application JSON publication classifies every result as `known-uncommitted` (the declared commit point was not reached, so the prior primary is intact or no primary existed) or `durability-uncertain` (the new primary is visible but the directory sync or reply step did not confirm). Injected-failure tests cover disk-full (`ENOSPC`), permission denial, short write, rename, file and directory fsync, and staging/publication crash outcomes, and a partial multi-file result names the primaries it made visible. This is injected-failure evidence, not measured power-loss atomicity (`internal/persist/store_outcomes.go:45-190`, `internal/persist/migration_apply.go:111-140`, `internal/persist/crash_consistency_test.go:21-70`).

See [deployment](deployment.md) for host installation and [security](security.md) for the trust boundary.
