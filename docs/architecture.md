# Architecture

## Products

| Product | Role |
| --- | --- |
| `pixie_web` | Controller-only Go web workspace. It connects to a separately managed loopback `pixie_cli` host and never contains or starts Bun, Node or Pi. |
| `pixie_cli` | Bundled Bun `1.4.0`, regular Pi TUI, Pi SDK, and standalone assistant host. `pixie_cli serve --config ABS` starts the host. |
| `pixie` | Bundled Bun `1.4.0`, regular Pi TUI, Pi SDK, assistant host, and controller. Its archive-internal full service is `libexec/pixie_full serve --assistant-config ABS --web-config ABS`. |

`pixie_assistant` is the archive-internal JavaScript host `libexec/pixie_assistant.js`, run by the pinned Bun `1.4.0` runtime `runtime/bin/bun`, rather than a compiled executable or a public product, command, unit, or archive. No Node runtime is bundled. The source build does not prove a standalone distribution, a published release, or an approved Docker deployment.

| Process | Location | Owns |
| --- | --- | --- |
| `pixie_cli`, `:3284` | Host user | Pi sessions, providers, credentials, models and extensions through the bundled Pi SDK at version `0.85.1` |
| `pixie_web`, `:7312` | Application container or local process | Web UI, projects, files, Git, goals, questions, queues, schedules and browser-MCP registration |
| External browser MCP, operator-chosen | Deployment | Browser runtime and MCP transport; Pi is the client and Pixie stores only the registration setting |

The two Compose products are alternatives. The controller-only `pixie_web` image never starts Pi and reaches a separately managed host over loopback; the full `pixie` image starts its bundled host and controller and persists Pi state in `pixie-pi-state`. They share controller state and the global `pixie` command, so do not run them against the same `PI_CODING_AGENT_DIR`. Docker definitions do not establish a completed deployment. Pixie hosts no browser and never proxies MCP traffic: Pi dials the operator-chosen browser MCP endpoint directly, while Pixie writes the registration into Pi's MCP configuration and reports a bounded probe.

## Source

Paths below are relative to the repository root, which holds the shared Bun workspace and lockfile.

| Directory | Responsibility |
| --- | --- |
| `assistant/` | The archive-internal host bundle `libexec/pixie_assistant.js`, built from `assistant/src`: Pi sessions in-process through the bundled Pi SDK, with no RPC child model or bridge sidecar |
| `web/cmd`, `web/internal/controller` | Application HTTP/WebSocket/MCP, native Pi projection, lifecycle and browser-MCP registration (`mcp_browser.go`) |
| `web/internal/canvas`, `web/internal/design` | Optional Canvas and Openfig workspace modules |
| `web/internal/mcpserver` | Module catalog and in-process MCP publication |
| `web/internal/workspace`, `web/internal/persist` | Bounded project access and durable state |
| `web/webui`, `shared/` | Svelte 5 interface and the shared wire-contract schema plus generated Go and TypeScript catalogs |
| `web/tests`, `shared/tests`, `assistant/tests` | Unit, integration and deployment checks, including the Bun host and Pi SDK probe suites |

The root `go.work` links the `shared` and `web` Go modules, each with its own `go.mod` and local `replace` so `GOWORK=off` still works. Bun builds the frontend with verified Mewa UI assets. The `pixie_web` image build includes static UI assets and Git. It runs as UID 1000 and uses a read-only root filesystem only when launched with the documented Compose flags.

## Configuration ownership

Each setting has one owner. Pixie never reads or writes another owner's state.

| Owner | Settings | Examples |
| --- | --- | --- |
| Pi (host service) | Providers, models, thinking, credentials, extensions, subagents, agents, transcripts, run settlement, effective MCP servers | `~/.pi/agent`, Pi settings APIs |
| Extension (guest capabilities) | Dialog answers, status/widget/title/working hints, background work signals | `ctx.ui` bridge, per-session liveness |
| Pixie (application state) | Projects, sessions, MCP enablement, browser-MCP registration, schedules, goals, settings, dialog and event projection | Controller data directory (`config.json`, `mcp-modules.json`) |
| External (operator-owned) | Browser MCP endpoint, memory daemon, search backend and credentials | Operator-selected endpoint, `SIGNET_DAEMON_URL`, `~/.config/rpiv-web-tools/config.json`, provider `*_API_KEY` |

Pi owns the effective MCP configuration. Pixie writes only its own browser entry through Pi's `pi.mcp.servers.*` operations. See [Pi integration](pi.md) for Pi-owned settings, the extension bridge and MCP publication, and [deployment](deployment.md) for external services.

## State and lifecycle

Pi stores native JSONL transcripts. Pixie stores project/session associations, goals, settings, durable queues, schedules and the browser-MCP registration. Application JSON publishes through validate-first staging with typed outcomes: only installed commits update dispatch state, durability-uncertain results retain the mutation identity and reconcile the validated primary without backup restore or replay, and deletions stay fail-closed. The host and MCP extension use locked atomic host-side JSON writes.

Schedule occurrences are recorded before dispatch. Runs create native sessions in an admitted project and retain status and session IDs. An ambiguous running entry after restart is marked interrupted and paused. Failed writes retain the execution claim. Missed cron occurrences coalesce into one run; schedules do not overlap. Pixie must remain running for dispatch. Schedules are available through the workspace list/detail/run views and the Settings schedules section, which reuse the ledger semantics and render recovery for a missing selection, as well as through project-scoped API methods and the `schedule_manage` tool.

Schedule mutations and their retry results commit in one atomic store. The latest 512 successful mutation identities survive restart; MCP callers can supply `mutationId` for retries. The runner allows eight concurrent jobs, retries persistence failures with backoff and exposes failures in application health. Cron expressions are cached until the schedule changes.

Workspace navigation uses v2 hash routes (`#/v2/...`) with v1 compatibility. The session catalog offers grouped and flat views ordered recent-first with selected/running pinning and native titles; Archive restore is metadata-only via `session.unarchive`. Pixie adds no second session index: listing reads the host catalog, and an index is added only after a benchmark under `web/tests/performance/` measures listing as a bottleneck. An empty project id means ungrouped (filesystem admission only, no hidden all-files project); removing a project grouping keeps conversations and session-keyed drafts.

Git inspection is read-only: staged index entries are compared against the base tree, raw worktree bytes are hashed within a 4 MiB per-file and 64 MiB aggregate budget with conservative reporting, previews note that clean/process and LFS conversion are not applied, and limits surface in per-repository warnings. The assembled HTTP handler reserves `/api/*` and `/mcp/*` for JSON errors and never falls back to the SPA document.

The Web UI receives the newest transcript page first. Older pages carry projection identities. Snapshots also carry pending tools and pending extension dialogs, so a reload mid-run, mid-tool, or mid-dialog reconciles against server state. Late message events from older runs never resurrect completed streaming state, and one prompt's several `agent_end` events never settle it early: only prompt settlement does. Inactive projections have count and memory budgets; active work, pending dialogs, registered liveness, and durable queues prevent eviction. Reconnect generations, session ownership and deletion markers reject stale work.
