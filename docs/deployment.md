# Deployment

The intended setup is Linux x86-64 or arm64 with the Go `pixie-assistant` binary on the host and either the Go full-host binary or the controller-only Docker image. Pi runs as the host user through its public executable in RPC mode. Bun is a source build/test tool, not an assistant runtime dependency.

## Host service

Install `pixie-assistant` from the matching commit-named binary archive. For a source build, run `CGO_ENABLED=0 go build -trimpath -o ../package/dist/pixie-assistant ./cmd/pixie-assistant` from `assistant/`. Install and configure optional extensions through Pi's native mechanisms.

Generate separate random values for `PIXIE_PI_SECRET_KEY`, `PIXIE_MCP_TOKEN`, and controller `PIXIE_TOKEN`. Store them in a private environment file with mode `0600`, load it into the host service environment, and use the same values in Compose's `.pixie` file. The optional Browser connection expands its token from the host environment.

```sh
PIXIE_PI_SECRET_KEY=<at-least-32-characters> \
PI_CODING_AGENT_DIR="$HOME/.pi/agent" \
pixie-assistant serve --config "$HOME/.config/pixie/assistant.json"
```

The JSON configuration selects the literal loopback host, port, agent directory, and optional Pi executable; see `package/systemd/assistant.json`. Environment values override the secret and selected Pi paths. Provider setup and optional extensions remain native Pi configuration. The Go adapter is not ready for production cutover until the critical session-routing, settlement, capability, recovery, and restart gaps in the [roadmap](../roadmap/README.md#confirmed-defects-and-integration-risks) close, so do not replace a working legacy service merely because the binary builds.

## Optional local models

Configure local providers through the selected Pi installation. `LLAMA_BASE_URL` is passed to the native Pi child when present; Pixie does not maintain a separate provider database.

## Containers

From the repository root:

```sh
cp .pixie.example .pixie
chmod 600 .pixie
```

Set `PIXIE_DATA_PATH`, `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. The [example](../.pixie.example) lists optional addresses, authentication and resource limits. Create `browser/artifacts` and `browser/state` inside the data directory. They must be writable by container UID/GID `1000:1000`. The data directory mounts into the Pixie container at `/var/lib/pixie`, so Browser state lives at `/var/lib/pixie/browser`.

Add project roots to the `pixie` service's mounts, preserving host absolute paths:

```yaml
- type: bind
  source: /absolute/path/to/project
  target: /absolute/path/to/project
  read_only: true
  bind:
    create_host_path: false
```

```sh
docker compose --env-file .pixie up -d --build
```

Open <http://127.0.0.1:7312>. Containers use host networking; bridged-container loopback cannot reach host Pi. Controller-owned service calls may use cleartext HTTP only from an actual loopback peer with a literal `localhost`, `127.0.0.1` or `::1` Host at the configured listener port; route-specific bearer and session checks still apply. For remote access either enable authentication with HTTPS and an exact public origin, or run on a trusted LAN with firewall-only protection and explicit `PIXIE_ALLOW_UNAUTHENTICATED_REMOTE=true` plus the exact `PIXIE_PUBLIC_ORIGIN`, leaving `PIXIE_TRUSTED_PROXY_CIDRS` unset. A TLS-terminating reverse proxy requires controller authentication, a peer CIDR explicitly listed in `PIXIE_TRUSTED_PROXY_CIDRS`, a `Host` rewrite to the exact public authority, and `X-Forwarded-Proto: https`; only that header from the listed peer is honored, and arbitrary forwarding headers are ignored. Pi stays on loopback; set only `PIXIE_PI_PORT`.

## MCP and Signet

The Browser MCP publisher lives inside the main Pixie process on `PIXIE_CONTROLLER_PORT` (default `7312`): Browser tools are served at `/mcp/browser`, the module catalog at `/api/mcp/modules` and status at `/api/mcp/status`, authenticated with `PIXIE_MCP_TOKEN`. Enable Browser in Settings → Tools with a compatible MCP extension loaded. The universal MCP adapter also accepts unrelated stdio, HTTP and SSE servers.

Optional Signet memory is an operator-owned external service, not a Pixie-managed MCP connection. Pi loads a managed file extension through native `<agentDir>/extensions` discovery with fail-open lifecycle hooks and hidden auto-recall. Pixie never reads or writes `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`), `signet.json` or per-session `SIGNET_ENABLED`.

## Without Docker

The intended non-Docker topology is one self-contained full-host `pixie` binary containing the assistant, controller, and embedded web UI. It must not run beside a separate `pixie-assistant` service for the same sessions. Neither final binary needs Bun at run time.

This topology is not yet an approved deployment recipe. Full-host startup now requires an absolute Pi agent directory and a resolvable Pi executable, joins assistant engine failure to the controller, degrades readiness while a lost session awaits reload, and supports an explicit `runtime.restart` that exits status 75. Fresh-archive install/start/stop/restart/upgrade/rollback/uninstall evidence under real systemd is still missing. See the [roadmap](../roadmap/README.md#confirmed-defects-and-integration-risks).

Architecture status: the current host campaign covers `linux/amd64` only. The arm64 archives and `linux/arm64` image are still required release artifacts, but arm64 installation, systemd lifecycle and image runs are deferred to a separate Mac/arm64 pass and remain an open evidence gap; do not treat amd64 results as arm64 acceptance.

For development inspection only, `bun run build:host` builds the embedded UI and full-host binary at `package/dist/pixie`. Do not replace a working service until final archive installation, native Pi selection, readiness, failure propagation, restart, upgrade, rollback, and uninstall checks pass.

Advanced override only: `PIXIE_STATIC_DIR` serves the web UI from a disk directory instead of the embedded bundle (development use). A Go binary built before `bun run build:web` embeds only a placeholder and falls back to `PIXIE_STATIC_DIR`, then to the container asset path.

## Operations

Application `/health` and `/livez` check liveness; `/readyz` checks state, UI and Pi connectivity. The in-process MCP publisher shares the application listener. See [MCP publisher](mcp.md) for diagnostics.

Schedules run while Pixie is up. Ask a chat with MCP support to create, list, pause, resume, run or stop a schedule, or use the workspace Schedules list/detail views and the Settings schedules section. Each scheduled run is a separate chat in that project. For example, `schedule_manage` with `action: "create", prompt: "Review open tasks", cron: "0 9 * * 1-5", timezone: "Europe/Warsaw", mutationId: "weekday-review-1"` runs at 09:00 on weekdays. It uses the current project; repeat the mutation ID only for a retry of the same request. Cron has five fields and an IANA timezone (UTC by default). Pause prevents future dispatch; stop cancels the current run; run-now starts one immediately. See [state and restart behavior](architecture.md#state-and-lifecycle).

Back up Pi state, Pixie data and private environment files after active work settles. A publishing workflow produces digest references; set `PIXIE_IMAGE` to that reference and use Compose `pull` followed by `up -d --no-build` for a prebuilt deployment.
