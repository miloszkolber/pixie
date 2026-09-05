# Pi integration

The source host service uses unmodified `@earendil-works/pi-coding-agent` and `@earendil-works/pi-ai`, pinned to `0.85.1`. Bun runs the service on the host. Pi owns provider credentials, models, settings and native JSONL sessions under the selected agent directory, normally `~/.pi/agent`.

## Feature ownership

| Feature | Implementation |
| --- | --- |
| Chat, streaming, cancellation, steering, images, compaction, forks | Native Pi SDK, projected by Pixie |
| Providers, API keys, OAuth, models, defaults, thinking | Native Pi model/auth/settings APIs; secrets stay on the host |
| Project grouping, file attachments, history search, durable follow-ups | Pixie records and transcript projection |
| Defined agents and delegation | Optional `agents` extension; each child has a native Pi session |
| Plans | Optional `plans` extension |
| MCP tools, Browser, Signet, interactive Apps | Compatible MCP extension; Pixie supplies service connections |
| Goals, tasks and questions | Pixie session-scoped MCP |
| Schedules | Pixie storage and runner; ordinary Pi sessions, no Pi scheduling extension |

## Transport

Pixie connects to `/pi` over WebSocket with `Authorization: Bearer <PIXIE_PI_SECRET_KEY>`. `runtime.hello` returns protocol version `1`, a stable runtime identity and versioned capabilities. The host rejects browser Origin headers and bounds frames and pending requests.

Native session events become transcript, tool, usage and run updates. Attachments use Pi custom entries for presentation metadata. Snapshot attachment uses sequence checkpoints and buffers concurrent events; large histories arrive in bounded chunks. A small host catalog retains empty sessions as well as ordinary Pi sessions. Native compaction summaries, branch summaries, visible custom messages and saved plans survive reopening; hidden custom messages stay hidden. Streaming sends incremental text rather than repeated partial transcripts.

Active calls and runs keep their Pi session loaded. Idle sessions are released after five minutes, with at most 32 idle sessions retained. Reopening restores the native transcript and session MCP membership. Shutdown stops new requests and closes extension clients.


| Native event or entry | Pixie presentation |
| --- | --- |
| Message start/delta/end, tool execution updates | Transcript, tool activity and final usage |
| Compaction, automatic retry, summarization retry | Existing progress, retry and completion states |
| Compaction/branch summary, visible custom message | Persistent summary or notice row |
| Model, thinking and session metadata changes | Session controls and title |
| Saved plan entries and plan tool results | Session plan |
| Hidden custom messages and internal entries | Kept by Pi; omitted from the displayed transcript |
| Agent/turn bookkeeping | Pi internal; Pixie uses host run boundaries for completion |

The host loads Pi's normal resources and extensions. Optional controls require supported versions and complete operation sets. Extension registration adds services and tools; it does not replace prompts, intercept tools or add execution policies. TUI-specific extension interfaces are not rendered in the Web UI.

Use separate sessions for simultaneous Pi CLI and host work. Pi does not coordinate concurrent writes to the same session across processes. The host takes an exclusive lock for its own agent-directory service.

See [deployment](deployment.md) and the [extension contract](pi-extensions.md).
