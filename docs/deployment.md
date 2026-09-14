# Deployment

The deployment target is Linux x86-64 or arm64 with `pixie_assistant` on the host and `pixie_web` as the Docker controller container or a local process. Pi runs as the host user. The target `pixie_assistant` is a Bun host that runs Pi sessions in-process through the operator's installed Pi SDK, resolved at runtime and never bundled. The interim Go host supervises the selected `pi` executable over native RPC and is being replaced. See [assistant](assistant.md) and the [roadmap](../roadmap/roadmap.md).

## Host service

Install `pixie_assistant` from the matching commit-named binary archive, or build it from source as described in [assistant](assistant.md). Install and configure optional extensions through Pi's native mechanisms.

Generate separate random values for `PIXIE_PI_SECRET_KEY`, `PIXIE_MCP_TOKEN`, and controller `PIXIE_TOKEN`. Store them in a private environment file with mode `0600`, load it into the host service environment, and use the same values in Compose's `.pixie` file.

```sh
PIXIE_PI_SECRET_KEY=<at-least-32-characters> \
PI_CODING_AGENT_DIR="$HOME/.pi/agent" \
pixie_assistant serve --config "$HOME/.config/pixie/assistant.json"
```

Point `--config` at an absolute private JSON file that selects the literal loopback host, port, agent directory and optional Pi executable; `web/systemd/assistant.json` is the example. Environment values override the secret and selected Pi paths. Provider setup and optional extensions remain native Pi configuration. The interim Go host is not ready for production cutover until the critical lifecycle, parity and recovery gaps in the [roadmap](../roadmap/roadmap.md) close, so do not replace a working service merely because the binary builds.

## Optional local models

Configure local providers through the selected Pi installation. `LLAMA_BASE_URL` is passed to the native Pi session when present; Pixie does not maintain a separate provider database.

## Containers

From the repository root:

```sh
cp .pixie.example .pixie
chmod 600 .pixie
```

Set `PIXIE_DATA_PATH`, `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. The [example](../.pixie.example) lists optional addresses, authentication and resource limits. The data directory must be writable by container UID/GID `1000:1000` and mounts into the `pixie_web` container at `/var/lib/pixie`.

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

Open <http://127.0.0.1:7312>. Containers use host networking; bridged-container loopback cannot reach host Pi. Pi stays on loopback; set only `PIXIE_PI_PORT`. See [security](security.md#remote-access) for authentication, public-origin and TLS-proxy requirements.

## MCP and Signet

The Pixie MCP publisher lives inside the `pixie_web` process on `PIXIE_CONTROLLER_PORT` (default `7312`) and serves the Canvas and Design workspace modules; enable them in Settings → Tools with a compatible MCP extension loaded. See [Pi integration](pi.md#mcp) for endpoints and tokens. The universal MCP adapter also accepts unrelated stdio, HTTP and SSE servers.

Browser is an external MCP endpoint, not a Pixie module. The optional `pixie-browser` Compose service runs Obscura's MCP HTTP transport on its own network, published only on host loopback (`127.0.0.1:3000`), with a non-root user, a read-only root filesystem and resource limits; it is not a `pixie` dependency. Point Pi at it, or any other endpoint, from Settings → Browser. The endpoint may be unauthenticated, and its hardening, isolation and egress are the deployment's responsibility.

Optional Signet memory is an operator-owned external service, not a Pixie-managed MCP connection. Pi loads a managed file extension through native `<agentDir>/extensions` discovery with fail-open lifecycle hooks and hidden auto-recall. Pixie never reads or writes `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`), `signet.json` or per-session `SIGNET_ENABLED`.

## Without Docker

Run `pixie_assistant` and `pixie_web` as separate local processes. `pixie_assistant` owns Pi sessions and `pixie_web` serves the UI and controller, with `pixie_web` listening on the controller port and reaching the host over loopback. Do not run a second assistant for the same sessions.

This topology is not yet an approved deployment recipe. Startup must fail closed when the agent directory or Pi executable is missing, and readiness must degrade while a session awaits reload. arm64 lifecycle and upgrade/rollback evidence under real systemd is still missing. See the [roadmap](../roadmap/roadmap.md) for the current evidence status.

For development inspection only, `bun run build:host` builds the embedded UI and a full-host binary. Do not replace a working service until final archive installation, Pi selection, readiness, failure propagation and lifecycle checks pass.

Advanced override only: `PIXIE_STATIC_DIR` serves the web UI from a disk directory instead of the embedded bundle (development use). A Go binary built before `bun run build:web` embeds only a placeholder and falls back to `PIXIE_STATIC_DIR`, then to the container asset path.

## Operations

Application `/health` and `/livez` check liveness; `/readyz` checks state, UI and Pi connectivity. The in-process MCP publisher shares the application listener. See [Pi integration](pi.md#mcp) for diagnostics.

Schedules run while Pixie is up. Ask a chat with MCP support to create, list, pause, resume, run or stop a schedule, or use the workspace Schedules list/detail views and the Settings schedules section. Each scheduled run is a separate chat in that project. For example, `schedule_manage` with `action: "create", prompt: "Review open tasks", cron: "0 9 * * 1-5", timezone: "Europe/Warsaw", mutationId: "weekday-review-1"` runs at 09:00 on weekdays. It uses the current project; repeat the mutation ID only for a retry of the same request. Cron has five fields and an IANA timezone (UTC by default). Pause prevents future dispatch; stop cancels the current run; run-now starts one immediately. See [state and restart behavior](architecture.md#state-and-lifecycle).

Back up Pi state, Pixie data and private environment files after active work settles. A publishing workflow produces digest references; set `PIXIE_IMAGE` to that reference and use Compose `pull` followed by `up -d --no-build` for a prebuilt deployment.
