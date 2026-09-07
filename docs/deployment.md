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

`--agent-dir /absolute/path` selects Pi state, defaulting to `PI_CODING_AGENT_DIR` or `~/.pi/agent`. The service listens at `127.0.0.1:3284`. `--host` and `--port` change it. A service manager can run the same command and environment file. Provider setup is available in Pixie or Pi's native configuration. Existing native user and project resources load without a prescribed extension bundle. List optional packages by file path or preinstall them: bare package names wait on Pi's missing-source approval in headless use. This host enables five packages by absolute path in `/home/core/.pi/settings.json` `extensions` (backups `settings.json.bak-20260907` and `settings.json.bak-20260907-adapter` beside it), installed permanently under `/home/core/.pi/packages/` (own `package.json` with exact pins, `core`-owned; refresh with `bun install --cwd /home/core/.pi/packages`, then re-apply the portable child-launch patch from the assistant package to `node_modules/@mjakl/pi-subagent`), deliberately decoupled from the `/repo/pixie` checkout so repo moves, branch changes or dev-dependency pruning cannot break the live Pi environment: `@mjakl/pi-subagent` (subagents, patched), `@juicesharp/rpiv-todo` (todo), `@juicesharp/rpiv-web-tools` (web search/fetch), `@juicesharp/rpiv-ask-user-question` (structured questions) and `pi-mcp-adapter` 2.32.1 (the native MCP client runtime; without it Pi sessions have no MCP tools at all, while the assistant's admin bridge stays fail-closed and basic sessions still work). Verified live 2026-09-07: headless `pi -p` turns executed the `todo` tool end to end from the permanent tree, and after adding the adapter, live settings resolve all five user resources and an isolated assistant host loading the same absolute paths reports the `mcp` capability, the native `mcp` proxy tool and zero extension errors. Deliberately excluded: `@signetai/connector-pi` (a library without a Pi extension manifest; Signet stays an operator-owned external service).

The npm workflow uses `pixie-assistant-v*` tags for `@pixie_ai/pixie-assistant`, with OIDC trusted publishing and provenance. Publication is a separate approval gate. `0.1.1` and `0.1.2` are published from their tags; `0.1.0` stays immutable. For an approved release containing this loading path:

```sh
bunx @pixie_ai/pixie-assistant@<version>
```

Subagents stay optional: the production install ships no subagent package. To enable them standalone, install the exact pinned version alongside (`bun add @mjakl/pi-subagent@3.0.1` or `npm install -S @mjakl/pi-subagent@3.0.1`) and re-run the assistant postinstall so the bundled Bun child-launch patch applies (`bun pm untrusted` aside, `npm rebuild @pixie_ai/pixie-assistant` replays it); without the package, Pi simply offers no subagent extension.

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

Open <http://127.0.0.1:7312>. Containers use host networking; bridged-container loopback cannot reach host Pi. For remote access either enable authentication with HTTPS and an exact public origin, or run on a trusted LAN with firewall-only protection and explicit `PIXIE_ALLOW_UNAUTHENTICATED_REMOTE=true` plus the exact `PIXIE_PUBLIC_ORIGIN`. Pi stays on loopback; set only `PIXIE_PI_PORT`.

## MCP and Signet

The Browser MCP publisher lives inside the main Pixie process on `PIXIE_CONTROLLER_PORT` (default `7312`): Browser tools are served at `/mcp/browser`, the module catalog at `/api/mcp/modules` and status at `/api/mcp/status`, authenticated with `PIXIE_MCP_TOKEN`. Enable Browser in Settings → Tools with a compatible MCP extension loaded. The universal MCP adapter also accepts unrelated stdio, HTTP and SSE servers.

Optional Signet memory is an operator-owned external service, not a Pixie-managed MCP connection. This host runs the managed `extensions/signet-pi.js` file extension (generated 2026-09-07 with the workspace connector's public `PiConnector.install()` API against a staging directory, so the operator's global `~/.config/signet/pi.json` was never touched; backup `pi.json.bak-20260907` beside it), and Pi loads it through native `<agentDir>/extensions` discovery with fail-open lifecycle hooks and hidden auto-recall. Verified live 2026-09-07: inventory shows the file with zero load errors and a headless turn executed `signet_recall` against the operator daemon successfully. Pixie never reads or writes `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`), `signet.json` or per-session `SIGNET_ENABLED`.

## Without Docker

Docker stays the primary method. Where containers are unavailable, the one supported non-Docker installation is a single self-contained `pixie` binary with the web UI embedded at build time, plus the published `pixie-assistant` package. No checkout, web toolchain, or asset directory is needed at run time. The Browser module stays optional and degrades to `disabled` without Chromium and `agent-browser` on `PATH`.

Build once from a checkout (build time needs the Go, Bun and Node toolchains):

```sh
bun install --frozen-lockfile
bun run build   # builds the web UI first so the Go binary embeds it
```

Install the result as your own user (no root, no privileged services): copy `package/dist/pixie` to `~/.local/bin/pixie` and install the assistant from the registry:

```sh
bun add --global @pixie_ai/pixie-assistant@<version>
```

Generate separate random values for `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. Store them in a private environment file such as `~/.config/pixie/pixie.env` with mode `0600`:

```sh
PIXIE_PI_SECRET_KEY=<secret>
PIXIE_MCP_TOKEN=<token>
```

Both secrets live in that one file: the assistant reads `PIXIE_PI_SECRET_KEY` for its Bearer credential, and `pixie` reads both. Reuse your existing Pi configuration; no Pi setup is needed beyond what the TUI already uses, and uninstalling Pixie never touches native Pi state.

The service listens on loopback (`127.0.0.1:7312` by default; `--host`/`--port` on the assistant, `PIXIE_CONTROLLER_HOST`/`PIXIE_CONTROLLER_PORT` on `pixie`). The remote-access rules from the container layout apply unchanged: either enable authentication with HTTPS and an exact public origin, or stay on a trusted LAN with firewall-only protection and explicit `PIXIE_ALLOW_UNAUTHENTICATED_REMOTE=true` plus the exact `PIXIE_PUBLIC_ORIGIN`. State defaults to `$XDG_DATA_HOME/pixie` (`~/.local/share/pixie`) when `PIXIE_DATA_DIR` is unset; set it explicitly only to use a different directory.

Foreground (two terminals, or one service manager):

```sh
pixie-assistant --agent-dir "${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}"
```

```sh
pixie
```

`pixie --version` prints the stamped version and revision. Logs go to stderr as JSON; under a service manager, read them with `journalctl --user -u pixie` and `journalctl --user -u pixie-assistant`.

Optional systemd user units (`~/.config/systemd/user/`). `pixie-assistant.service`:

```ini
[Unit]
Description=Pixie assistant (native Pi host)
After=network-online.target

[Service]
Type=simple
EnvironmentFile=%h/.config/pixie/pixie.env
ExecStart=%h/.bun/bin/pixie-assistant --port 3284
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=default.target
```

`pixie.service`:

```ini
[Unit]
Description=Pixie web application
After=network-online.target pixie-assistant.service
Requires=pixie-assistant.service

[Service]
Type=simple
EnvironmentFile=%h/.config/pixie/pixie.env
ExecStart=%h/.local/bin/pixie
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=default.target
```

Adjust the `ExecStart` paths to the actual install locations, then:

```sh
systemctl --user daemon-reload
systemctl --user enable --now pixie-assistant.service pixie.service
```

Whole-host reload: `pi.reload` asks the assistant to end itself so the service manager brings a fresh process up, applying configured native extensions to every session at once. Enable it on the assistant unit with `Restart=always` and `PIXIE_ALLOW_SELF_RESTART=1`. In-flight runs interrupt ([Pi integration](pi.md)); session transcripts stay durable on disk.

Upgrade by replacing the binary and reinstalling the assistant package, then restarting both units. Keep the previous binary (for example as `pixie.prev`) for instant rollback: the binary is self-contained, so rollback is just swapping the file back and restarting. Back up `PIXIE_DATA_DIR` and the private environment file after active work settles; both survive upgrades and rollbacks in place.

Removal: stop and disable both units, delete the binary (`~/.local/bin/pixie`), remove the global package (`bun remove --global @pixie_ai/pixie-assistant`), and delete the environment file. Optionally delete `PIXIE_DATA_DIR`. Native Pi state (`PI_CODING_AGENT_DIR` or `~/.pi/agent`) is left intact.

Advanced override only: `PIXIE_STATIC_DIR` serves the web UI from a disk directory instead of the embedded bundle (development use). A Go binary built before `bun run build:web` embeds only a placeholder and falls back to `PIXIE_STATIC_DIR`, then to the container asset path.

## Operations

Application `/health` and `/livez` check liveness; `/readyz` checks state, UI and Pi connectivity. The in-process MCP publisher shares the application listener. See [MCP publisher](mcp.md) for diagnostics.

Schedules run while Pixie is up. Ask a chat with MCP support to create, list, pause, resume, run or stop a schedule. Each scheduled run is a separate chat in that project. For example, `schedule_manage` with `action: "create", prompt: "Review open tasks", cron: "0 9 * * 1-5", timezone: "Europe/Warsaw", mutationId: "weekday-review-1"` runs at 09:00 on weekdays. It uses the current project; repeat the mutation ID only for a retry of the same request. Cron has five fields and an IANA timezone (UTC by default). Pause prevents future dispatch; stop cancels the current run; run-now starts one immediately. See [state and restart behavior](architecture.md#state-and-lifecycle).

Back up Pi state, Pixie data and private environment files after active work settles. A publishing workflow produces digest references; set `PIXIE_IMAGE` to that reference and use Compose `pull` followed by `up -d --no-build` for a prebuilt deployment.
