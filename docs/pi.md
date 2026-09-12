# Pi integration

The release assistant is the Go `pixie-assistant` binary. It starts the selected public `pi` executable in RPC mode and does not embed an SDK or require Bun at runtime. Pi owns provider credentials, models, settings and native JSONL sessions under the selected agent directory, normally `~/.pi/agent`.

The legacy source service and parity fixtures still use `@earendil-works/pi-coding-agent` and `@earendil-works/pi-ai` pinned to `0.85.1`, including a small SDK export patch for the built-in extension barrel ([extensions](pi-extensions.md)). They remain a fallback and compatibility oracle until the Go adapter's audited gaps close; they are not an npm release path.

## Feature ownership and cutover status

The table records the intended owner, not proof that the Go adapter currently exposes every operation. The active legacy service retains the richer projections while the Go gaps in the [roadmap](../roadmap/README.md#confirmed-defects-and-integration-risks) remain open.

| Feature | Implementation |
| --- | --- |
| Chat, streaming, cancellation, steering, images, compaction, forks | Native Pi SDK, projected by Pixie |
| Run settlement, retry, compaction and lifecycle annotations | Native Pi events; Go separates acceptance from settlement and returns the terminal reason, pending real-provider event-order evidence |
| Extension dialogs (`select`, `confirm`, `input`, `editor`) | Generic host UI bridge, projected by Pixie; pending dialogs replay on reload |
| Extension status, widget, title, working-message hints | Generic host projections, fanned out by Pixie; terminal-only interfaces stay unavailable |
| Providers, API keys, OAuth, models, defaults, thinking | Native Pi model/auth/settings APIs; secrets stay on the host |
| Project grouping, file attachments, history search, durable follow-ups | Pixie records and transcript projection |
| Defined agents and delegation | Pixie authoring API for native Markdown definitions, optional native subagent extension for execution |
| Plans | Upstream `todo` tool via the `rpiv-todo` extension |
 | MCP tools, Browser | Operator-installed upstream MCP adapter; Pixie supplies service connections |
 | Signet | Operator-owned external service with a Pi-native file extension; no Pixie MCP connection |
| Goals, tasks and questions | Pixie session-scoped MCP |
| Schedules | Pixie storage and runner; ordinary Pi sessions, no Pi scheduling extension |

## Transport

Pixie connects to `/pi` over WebSocket with `Authorization: Bearer <PIXIE_PI_SECRET_KEY>`. The Go `runtime.hello` returns protocol version `1`, a stable persistent runtime identity, capabilities, and an exhaustive operation set. Unsupported operations are absent or false and fail closed. Stable authority now feeds deletion binding; production host v2 and durable pairing remain roadmap work. The host rejects browser Origin headers and bounds frames and pending requests.

The legacy host projects transcript, tool, usage, run, lifecycle, UI, history, dialog, and attachment state. The Go adapter now owns one immutable child per logical session in the admitted cwd, verifies exact identity before every operation, blocks prompt until settlement, and advertises only negotiated capabilities. Its transcript projection remains narrower than the legacy host; do not infer full parity from these repairs or from legacy snapshot tests.

The legacy host has residency, replay, and bounded-shutdown behavior that the Go replacement must retain. The Go host keeps a durable session registry, reloads an exact session file on demand, and degrades readiness while a lost session awaits reload. The wire target, bounds, and versioning rules are described by the [assistant protocol](pi-protocol.md), but production integration remains subject to the roadmap audit.


| Native event or entry | Pixie presentation |
| --- | --- |
| Message start/delta/end, tool execution updates | Transcript, tool activity and final usage |
| Compaction, automatic retry, summarization retry | Existing progress, retry and completion states |
| Compaction/branch summary, visible custom message | Persistent summary or notice row |
| Model, thinking and session metadata changes | Session controls and title |
| Saved plan entries and plan tool results | Session plan |
| Hidden custom messages and internal entries | Kept by Pi; omitted from the displayed transcript |
| Agent/turn bookkeeping | Pi internal; Pixie uses host run boundaries for completion |

The host must load Pi's normal resources and extensions. Optional controls require supported versions and an exhaustive negotiated operation set. Extension registration adds services and tools; it does not replace prompts, intercept tools or add execution policies. TUI-specific extension interfaces are not rendered in the Web UI.

Use separate sessions for simultaneous Pi CLI and host work. Pi does not coordinate concurrent writes to the same session across processes. The host takes an exclusive lock for its own agent-directory service.

See [deployment](deployment.md), the [extension contract](pi-extensions.md) and the [assistant protocol](pi-protocol.md).
