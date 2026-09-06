# Deployment

The primary setup is Linux x86-64 or arm64 with Docker Compose and Bun on the host. Pi runs as the host user through the source SDK service. No custom Pi executable is built.

## Host service

```sh
git clone https://github.com/miloszkolber/pixie.git
cd pixie
bun install --frozen-lockfile --production --filter @pixie_ai/pixie-assistant
```

This installs the host and MCP extension runtime dependencies without the web development toolchain.

Generate separate random values for `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. Store them in a private environment file with mode `0600`, load it into the host service environment, and use the same values in Compose's `.pixie` file. The optional Browser connection expands its token from the host environment.

```sh
bun pi/host/src/main.ts --extensions mcp,agents,rpiv-todo,rpiv-web,rpiv-ask
```

Omit `--extensions` for baseline Pi. `--agent-dir /absolute/path` selects Pi state; the default is `~/.pi/agent`. The service listens at `127.0.0.1:3284`; `--host` and `--port` change it. A service manager can run the same command and environment file. Provider setup is available in Pixie or Pi's native configuration.

Published artifacts remove the checkout from the critical path. Each `pi-host-v*` tag publishes `@pixie_ai/pixie-assistant` to npm via OIDC trusted publishing (no stored token); the container workflow already publishes `ghcr.io/<owner>/pixie` on every `v*` tag. One-time npm setup, in order: publish the package once under the existing `pixie_ai` organization so it exists (any short-lived granular token with publish access to the scope works, then discard it); open the package Settings → Trusted publisher and add this repository with the `npm-publish.yml` workflow; afterwards every `pi-host-v*` tag publishes unattended with provenance attestation. Prefer the published artifacts for clean machines:

```sh
bunx @pixie_ai/pixie-assistant@<version> --extensions mcp,agents,rpiv-todo,rpiv-web,rpiv-ask
```

One `bunx` caveat: the upstream subagent child-runner patch does not travel through npm (root `patchedDependencies` apply to workspace installs only), so `subagent` child runs fail under `bunx` until upstream accepts the entrypoint fix. Everything else, including local `llama.cpp` inference, works unchanged from the published package (verified: tarball install, `--version`, host boot with five profiles, capability snapshot).

## Optional local models

Append `,llama` to the host `--extensions` list to register Pi's built-in llama.cpp provider inside Pixie sessions (embedded hosts do not auto-load it; without this profile the provider is absent from `pi.providers.list`). The endpoint comes from `LLAMA_BASE_URL` in the host service environment (for example `http://127.0.0.1:4667/v1`); authentication falls back to the dummy key `local`, which llama.cpp ignores. This stays opt-in: the universal profile list above omits it, and host-specific setup (such as a systemd unit) adds both the profile and the variable only where a local endpoint actually runs.

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

The Browser MCP publisher lives inside the main Pixie process on port `7312`: Browser tools are served at `/mcp/browser`, the module catalog at `/api/mcp/modules` and status at `/api/mcp/status`, authenticated with `PIXIE_MCP_TOKEN`. Enable Browser in Settings → Tools with a compatible MCP extension loaded. The universal MCP adapter also accepts unrelated stdio, HTTP and SSE servers.

Optional Signet memory is an operator-owned external service, not a Pixie-managed MCP connection. Install it with `signet setup` and run the daemon separately; Pi loads the managed `signet-pi.js` file extension with fail-open lifecycle hooks and hidden auto-recall. Pixie never reads or writes `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`), `signet.json` or per-session `SIGNET_ENABLED`.

## Without Docker

Docker stays the primary method, but the application also runs as plain binaries for machines where containers are unavailable. Build time needs Go, Bun and Node toolchains; run time needs only the two binaries plus a Chromium build and `agent-browser` on `PATH` if the Browser module is enabled (it degrades to `disabled` otherwise).

```sh
bun install --frozen-lockfile
bun run build:web      # web UI into pixie/webui/dist (build time only)
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
