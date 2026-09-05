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
bun pi/host/src/main.ts --extensions mcp,agents,plans,web
```

Omit `--extensions` for baseline Pi. `--agent-dir /absolute/path` selects Pi state; the default is `~/.pi/agent`. The service listens at `127.0.0.1:3284`; `--host` and `--port` change it. A service manager can run the same command and environment file. Provider setup is available in Pixie or Pi's native configuration.

## Containers

From the repository root:

```sh
cp .pixie.example .pixie
chmod 600 .pixie
```

Set `PIXIE_DATA_PATH`, `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. The [example](../.pixie.example) lists optional addresses, authentication and resource limits. Create `app`, `browser/artifacts` and `browser/state` inside the data directory. They must be writable by container UID/GID `1000:1000`.

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

Open <http://127.0.0.1:7312>. Containers use host networking; bridged-container loopback cannot reach host Pi. For remote access configure authentication, HTTPS and exact, distinct application and MCP origins.

## MCP and Signet

Enable Browser in Settings → Tools with a compatible MCP extension loaded. Pixie discovers its catalog at `http://127.0.0.1:8787/v1/mcp/modules` and registers `pixie-browser` at `/browser`. The universal [Pi MCP extension](../pi/mcp/README.md) also accepts unrelated stdio, HTTP and SSE servers.

Optional Signet memory uses a separately running daemon reachable by Pi and Pixie. Configure it in Settings → Signet. New or reattached sessions receive its connection. The default is `http://127.0.0.1:3850/mcp`. Its health check verifies the daemon, not memory retrieval.

## Operations

Application `/livez` checks liveness; `/readyz` checks state, UI and Pi connectivity. MCP `/health` checks liveness; authenticated `/readyz` checks published modules without launching Chromium. See [MCP service](mcp.md) for diagnostics.

Schedules run while Pixie is up. Ask a chat with MCP support to create, list, pause, resume, run or stop a schedule. Each scheduled run is a separate chat in that project. For example, `schedule_manage` with `action: "create", prompt: "Review open tasks", cron: "0 9 * * 1-5", timezone: "Europe/Warsaw", mutationId: "weekday-review-1"` runs at 09:00 on weekdays. It uses the current project; repeat the mutation ID only for a retry of the same request. Cron has five fields and an IANA timezone (UTC by default). Pause prevents future dispatch; stop cancels the current run; run-now starts one immediately. See [state and restart behavior](architecture.md#state-and-lifecycle).

Back up Pi state, Pixie data and private environment files after active work settles. Use matching application and MCP image revisions. A publishing workflow produces digest references; set `PIXIE_IMAGE` and `PIXIE_MCP_IMAGE` to those references and use Compose `pull` followed by `up -d --no-build` for a prebuilt deployment.
