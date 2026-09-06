# Deployment

The primary setup is Linux x86-64 or arm64 with Docker Compose and Bun on the host. Pi runs as the host user through the source SDK service. No custom Pi executable is built.

## Host service

```sh
git clone https://github.com/miloszkolber/pixie.git
cd pixie
bun install --frozen-lockfile --production --filter @pixie/pi-host --filter @pixie/pi-mcp
```

This installs the host and MCP extension runtime dependencies without the web development toolchain.

Generate separate random values for `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. Store them in a private environment file with mode `0600`, load it into the host service environment, and use the same values in Compose's `.pixie` file. The optional Browser connection expands its token from the host environment.

```sh
bun pi/host/src/main.ts --extensions mcp,agents,rpiv-todo,rpiv-web,rpiv-ask
```

Omit `--extensions` for baseline Pi. `--agent-dir /absolute/path` selects Pi state; the default is `~/.pi/agent`. The service listens at `127.0.0.1:3284`; `--host` and `--port` change it. A service manager can run the same command and environment file. Provider setup is available in Pixie or Pi's native configuration.

## Containers

From the repository root:

```sh
cp .pixie.example .pixie
chmod 600 .pixie
```

Set `PIXIE_DATA_PATH`, `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. The [example](../.pixie.example) lists optional addresses, authentication and resource limits. Create `app`, `browser/artifacts` and `browser/state` inside the data directory. They must be writable by container UID/GID `1000:1000`. The `browser` directory mounts into the same Pixie container at `/var/lib/pixie-browser`.

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

Open <http://127.0.0.1:7312>. Containers use host networking; bridged-container loopback cannot reach host Pi. For remote access configure authentication, HTTPS and an exact public origin.

## MCP and Signet

The Browser MCP publisher lives inside the main Pixie process on port `7312`: Browser tools are served at `/mcp/browser`, the module catalog at `/api/mcp/modules` and status at `/api/mcp/status`, authenticated with `PIXIE_MCP_TOKEN`. Enable Browser in Settings → Tools with a compatible MCP extension loaded. The universal [Pi MCP extension](../pi/mcp/README.md) also accepts unrelated stdio, HTTP and SSE servers.

Optional Signet memory is an operator-owned external service, not a Pixie-managed MCP connection. Install it with `signet setup` and run the daemon separately; Pi loads the managed `signet-pi.js` file extension with fail-open lifecycle hooks and hidden auto-recall. Pixie never reads or writes `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`), `signet.json` or per-session `SIGNET_ENABLED`.

## Operations

Application `/health` and `/livez` check liveness; `/readyz` checks state, UI and Pi connectivity. The in-process MCP publisher shares the application listener. See [MCP publisher](mcp.md) for diagnostics.

Schedules run while Pixie is up. Ask a chat with MCP support to create, list, pause, resume, run or stop a schedule. Each scheduled run is a separate chat in that project. For example, `schedule_manage` with `action: "create", prompt: "Review open tasks", cron: "0 9 * * 1-5", timezone: "Europe/Warsaw", mutationId: "weekday-review-1"` runs at 09:00 on weekdays. It uses the current project; repeat the mutation ID only for a retry of the same request. Cron has five fields and an IANA timezone (UTC by default). Pause prevents future dispatch; stop cancels the current run; run-now starts one immediately. See [state and restart behavior](architecture.md#state-and-lifecycle).

Back up Pi state, Pixie data and private environment files after active work settles. A publishing workflow produces digest references; set `PIXIE_IMAGE` to that reference and use Compose `pull` followed by `up -d --no-build` for a prebuilt deployment.
