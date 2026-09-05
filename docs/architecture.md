# Architecture

| Process | Location | Owns |
| --- | --- | --- |
| Pi SDK service, `:3284` | Host user | Native sessions, providers, credentials, models and extensions |
| Pixie, `:7312` | Application container | Web UI, projects, files, Git, goals, questions, queues and schedules |
| Pixie MCP, `:8787` | Separate container | Browser, Chromium, artifacts and interactive App origin |

Linux host networking lets both containers reach host services over loopback. Only the application receives project mounts, read-only and at the same absolute paths used by Pi. Browser has its own state and no project, application-state or Pi-configuration mounts.

## Source

Paths below are relative to `pixie/`.

| Directory | Responsibility |
| --- | --- |
| `cmd/pixie`, `internal/controller` | Application HTTP/WebSocket/MCP, native Pi projection and lifecycle |
| `cmd/pixie-mcp`, `internal/mcphost`, `internal/browser` | MCP catalog, Browser routes and runtime |
| `internal/workspace`, `internal/persist` | Bounded project access and durable state |
| `pi-host` | Authenticated native SDK service and optional factories |
| `pi-mcp` | Independently usable Pi MCP extension |
| `webui`, `contracts` | Svelte 5 interface and shared wire contracts |
| `tests` | Unit, integration, deployment and browser checks |

Bun builds the frontend with verified Mewa UI assets. The Go application image includes static UI assets and Git. The MCP image includes the Browser runtime. Both run non-root with read-only root filesystems.

## State and lifecycle

Pi stores native JSONL transcripts. Pixie stores project/session associations, goals, settings, durable queues, schedules and browser-panel ownership. Application JSON uses atomic replacement; execution and ownership ledgers reject stale-backup recovery. The host and MCP extension use locked atomic host-side JSON writes.

Schedule occurrences are recorded before dispatch. Runs create native sessions in an admitted project and retain status and session IDs. An ambiguous running entry after restart is marked interrupted and paused. Failed writes retain the execution claim. Missed cron occurrences coalesce into one run; schedules do not overlap. Pixie must remain running for dispatch. There is no Automation settings screen; schedules are available through project-scoped API methods and the `schedule_manage` tool.

Schedule mutations and their retry results commit in one atomic store. The latest 512 successful mutation identities survive restart; MCP callers can supply `mutationId` for retries. The runner allows eight concurrent jobs, retries persistence failures with backoff and exposes failures in application health. Cron expressions are cached until the schedule changes.

The browser receives the newest transcript page first. Older pages carry projection identities. Inactive projections have count and memory budgets; active work and durable queues prevent eviction. Reconnect generations, session ownership and deletion markers reject stale work.

Browser panels have persisted ownership and renewable leases. Startup retries cleanup of recorded panels. Interactive MCP Apps use a separate origin, short-lived tickets and same-session tool/resource authorization.
