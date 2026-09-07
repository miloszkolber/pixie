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

Native session events become transcript, tool, usage and run updates. Attachments use Pi custom entries for presentation metadata. Snapshot attachment uses sequence checkpoints and buffers concurrent events; large histories arrive in bounded chunks. A small host catalog retains empty sessions as well as ordinary Pi sessions.

The host loads Pi's normal resources and extensions. Optional controls require supported versions and complete operation sets. Extension registration adds services and tools; it does not replace prompts, intercept tools or add execution policies. TUI-specific extension interfaces are not rendered in the Web UI.

Use separate sessions for simultaneous Pi CLI and host work. Pi does not coordinate concurrent writes to the same session across processes. The host takes an exclusive lock for its own agent-directory service.

See [deployment](deployment.md) and the [extension contract](pi-extensions.md).
