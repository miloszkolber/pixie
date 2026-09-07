# Deployment

The primary setup is Linux x86-64 or arm64 with Docker Compose and Bun on the host. Pi runs as the host user through the source SDK service. No custom Pi executable is built.

## Host service

```sh
git clone https://github.com/miloszkolber/pixie.git
cd pixie
bun install --frozen-lockfile --production --filter @pixie_ai/pixie-assistant
```

This installs the assistant's runtime dependencies without optional packages or the web development toolchain. Install and configure optional extensions through Pi's native mechanisms.

Generate separate random values for `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. Store them in a private environment file with mode `0600`, load it into the host service environment, and use the same values in Compose's `.pixie` file. The optional Browser connection expands its token from the host environment.

```sh
bun assistant/src/main.ts --llama
```

`--agent-dir /absolute/path` selects Pi state, defaulting to `PI_CODING_AGENT_DIR` or `~/.pi/agent`. The service listens at `127.0.0.1:3284`. `--host` and `--port` change it. A service manager can run the same command and environment file. Provider setup is available in Pixie or Pi's native configuration. Existing native user and project resources load without a prescribed extension bundle.

The npm workflow uses `pixie-assistant-v*` tags for `@pixie_ai/pixie-assistant`, with OIDC trusted publishing and provenance. Publication is a separate approval gate. Published `0.1.0` is immutable and does not contain the current source changes. For an approved release containing this loading path:

```sh
bunx @pixie_ai/pixie-assistant@<version>
```

The optional subagent child-runner patch applies only to workspace installs, not native independently installed packages. Child-launch compatibility and the final packaging form remain separate release gates.

## Optional local models

Use `--llama` to load Pi's built-in llama.cpp provider inside assistant sessions. The embedded SDK does not automatically supply this CLI built-in. The endpoint comes from `LLAMA_BASE_URL` in the service environment, and authentication falls back to the dummy key `local`. This stays opt-in and uses Pi's existing provider implementation, not a Pixie model database.

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

The Browser MCP publisher lives inside the main Pixie process on `PIXIE_CONTROLLER_PORT` (default `7312`): Browser tools are served at `/mcp/browser`, the module catalog at `/api/mcp/modules` and status at `/api/mcp/status`, authenticated with `PIXIE_MCP_TOKEN`. Enable Browser in Settings → Tools with a compatible MCP extension loaded. The universal MCP adapter also accepts unrelated stdio, HTTP and SSE servers.

Optional Signet memory is an operator-owned external service, not a Pixie-managed MCP connection. Install it with `signet setup` and run the daemon separately; Pi loads the managed `signet-pi.js` file extension with fail-open lifecycle hooks and hidden auto-recall. Pixie never reads or writes `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`), `signet.json` or per-session `SIGNET_ENABLED`.

## Without Docker

Docker stays the primary method, but the application also runs as plain binaries for machines where containers are unavailable. Build time needs Go, Bun and Node toolchains; run time needs only the two binaries plus a Chromium build and `agent-browser` on `PATH` if the Browser module is enabled (it degrades to `disabled` otherwise).

```sh
bun install --frozen-lockfile
bun run build:web      # web UI into package/webui/dist (build time only)
cd pixie && CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/pixie ./cmd/pixie
```

Run with the same environment as the Compose service, plus two directory overrides that default to container paths:

```sh
PIXIE_DATA_DIR=/var/lib/pixie \
PIXIE_STATIC_DIR=/path/to/pixie/webui/dist \
PIXIE_PI_URL=ws://127.0.0.1:3284/pi \
PIXIE_PI_SECRET_KEY=<secret> PIXIE_MCP_TOKEN=<token> \
pixie
```

Data, auth, project mounts and health endpoints behave identically to the container layout. The Pi host service itself already runs on the host with no container involved.

## Operations

Application `/health` and `/livez` check liveness; `/readyz` checks state, UI and Pi connectivity. The in-process MCP publisher shares the application listener. See [MCP publisher](mcp.md) for diagnostics.

Schedules run while Pixie is up. Ask a chat with MCP support to create, list, pause, resume, run or stop a schedule. Each scheduled run is a separate chat in that project. For example, `schedule_manage` with `action: "create", prompt: "Review open tasks", cron: "0 9 * * 1-5", timezone: "Europe/Warsaw", mutationId: "weekday-review-1"` runs at 09:00 on weekdays. It uses the current project; repeat the mutation ID only for a retry of the same request. Cron has five fields and an IANA timezone (UTC by default). Pause prevents future dispatch; stop cancels the current run; run-now starts one immediately. See [state and restart behavior](architecture.md#state-and-lifecycle).

Back up Pi state, Pixie data and private environment files after active work settles. A publishing workflow produces digest references; set `PIXIE_IMAGE` to that reference and use Compose `pull` followed by `up -d --no-build` for a prebuilt deployment.
