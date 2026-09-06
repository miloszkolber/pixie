# Architecture

| Process | Location | Owns |
| --- | --- | --- |
| Pi SDK service, `:3284` | Host user | Native sessions, providers, credentials, models and extensions |
| Pixie, `:7312` | Application container | Web UI, projects, files, Git, goals, questions, queues, schedules, Browser MCP publisher, Chromium and artifacts |

Host networking lets the container reach host services over loopback. Only the application receives project mounts, read-only and at the same absolute paths used by Pi. Browser state mounts into the same container at `/var/lib/pixie-browser` and has no project, application-state or Pi-configuration mounts.

## Source

Paths below are relative to `pixie/`.

| Directory | Responsibility |
| --- | --- |
| `cmd/pixie`, `internal/controller` | Application HTTP/WebSocket/MCP, MCP publisher, native Pi projection and lifecycle |
| `internal/mcpserver`, `internal/browser` | In-process Browser module publication and browser runtime |
| `internal/workspace`, `internal/persist` | Bounded project access and durable state |
| `webui`, `contracts` | Svelte 5 interface and shared wire contracts |
| `tests` | Unit, integration, deployment and browser checks |

Pi sources live separately in top-level `pi/`: `host/` contains the SDK service and upstream extension profiles, `mcp/` preserves the old MCP package/file entry as a small compatibility shim, and `extensions/local-patches/` contains the pinned upstreamable MCP host API patch. The repository root holds the shared Bun workspace and lockfile.

Bun builds the frontend with verified Mewa UI assets. The single application image includes static UI assets, Git and the Browser runtime. It runs non-root with a read-only root filesystem.

## Configuration ownership

Each setting has one owner. Pixie never reads or writes another owner's state.

| Owner | Settings | Examples |
| --- | --- | --- |
| Pi (host service) | Providers, models, thinking, credentials, extensions, subagents, agents, transcripts, run settlement | `~/.pi/agent`, Pi settings APIs |
| Extension (guest capabilities) | Dialog answers, status/widget/title/working hints, background work signals | `ctx.ui` bridge, per-session liveness |
| Pixie (application state) | Projects, sessions, MCP enablement, Browser engine, schedules, goals, settings, dialog and event projection | Controller data directory (`config.json`, `mcp-modules.json`, `browser.json`) |
| External (operator-owned) | Memory daemon, search backend and credentials | `SIGNET_DAEMON_URL`, `~/.config/rpiv-web-tools/config.json`, provider `*_API_KEY` |

See [Pi integration](pi.md) for Pi-owned settings, [MCP publisher](mcp.md) for Pixie-owned MCP and Browser state, and [deployment](deployment.md) for external services.

## State and lifecycle

Pi stores native JSONL transcripts. Pixie stores project/session associations, goals, settings, durable queues, schedules and browser-panel ownership. Application JSON uses atomic replacement; execution and ownership ledgers reject stale-backup recovery. The host and MCP extension use locked atomic host-side JSON writes.

Schedule occurrences are recorded before dispatch. Runs create native sessions in an admitted project and retain status and session IDs. An ambiguous running entry after restart is marked interrupted and paused. Failed writes retain the execution claim. Missed cron occurrences coalesce into one run; schedules do not overlap. Pixie must remain running for dispatch. There is no Automation settings screen; schedules are available through project-scoped API methods and the `schedule_manage` tool.

Schedule mutations and their retry results commit in one atomic store. The latest 512 successful mutation identities survive restart; MCP callers can supply `mutationId` for retries. The runner allows eight concurrent jobs, retries persistence failures with backoff and exposes failures in application health. Cron expressions are cached until the schedule changes.

The browser receives the newest transcript page first. Older pages carry projection identities. Snapshots also carry pending tools and pending extension dialogs, so a reload mid-run, mid-tool, or mid-dialog reconciles against server state. Late message events from older runs never resurrect completed streaming state, and one prompt's several `agent_end` events never settle it early: only prompt settlement does. Inactive projections have count and memory budgets; active work, pending dialogs, registered liveness, and durable queues prevent eviction. Reconnect generations, session ownership and deletion markers reject stale work.

Browser panels have persisted ownership and renewable leases. Startup retries cleanup of recorded panels. Interactive MCP Apps use a separate origin, short-lived tickets and same-session tool/resource authorization.
