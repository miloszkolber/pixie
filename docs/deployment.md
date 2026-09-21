# Deployment

This is a configuration and validation reference, not an approved deployment recipe. A verified release and controller image are published, but credentialed Pi validation, an approved Docker deployment, standalone-distribution proof, and arm64 live lifecycle evidence remain separate work.

Run a separately installed `pixie` host and a `pixie_web` controller. `pixie_web` never contains or starts Bun, Node or Pi. `pixie` bundles the pinned Bun `1.4.2` runtime `runtime/bin/bun`, the Pi SDK and the normal native `pixie` TUI, excludes Pi RPC, and uses the internal host; no Node runtime is bundled. See [assistant](assistant.md) and the [roadmap](../roadmap/roadmap.md).

## Host service

Install `pixie` for the host when a separately authorized archive is available. Candidate archives are not a published release or standalone-distribution proof. Install and configure optional extensions through Pi's native mechanisms.

Generate separate random values for `PIXIE_PI_SECRET_KEY`, `PIXIE_MCP_TOKEN`, and controller `PIXIE_TOKEN`. Store them in a private environment file with mode `0600`, load it into the host service environment, and use the same values in Compose's `.pixie` file.

```sh
PIXIE_PI_SECRET_KEY=<at-least-32-characters> \
PI_CODING_AGENT_DIR="$HOME/.pi/agent" \
pixie serve --config "$HOME/.config/pixie/assistant.json"
```

Point `--config` at an absolute private JSON file that selects the literal loopback host, required `port`, and agent directory; `systemd/assistant.json` is the example. Set controller `PIXIE_PI_PORT` to the same value as config `port`. `pixie` uses its own archive-local Pi package and rejects external `PIXIE_PI_PACKAGE` selection. Provider setup and optional extensions remain native Pi configuration. Remaining lifecycle, parity, and recovery gaps are tracked in the [roadmap](../roadmap/roadmap.md).

Bare `pixie` inherits the caller's working directory and exported environment, including Bun or npm cache locations. A systemd service does not source `.bashrc`; put any cache overrides needed by the managed host in its `pixie.env` environment file instead.

## Protocol negotiation

`PIXIE_PI_PROTOCOL` opts into host protocol version 2 negotiation. Unset or `v1` keeps the default legacy handshake byte-for-byte. `auto` offers protocol versions `[2,1]` and accepts a v1 host; `v2` requires the negotiated version to be 2 and rejects a v1 selection. The controller and the assistant host read the same variable, the value is case-insensitive, and an invalid value fails startup instead of silently selecting a version. A peer that cannot agree on a version is rejected rather than downgraded. This is a source-level operator surface; live and credentialed version-2 behavior is not verified here.

## Internal environment

These variables exist for the archive launchers, the supervisor and deletion authority; they are not part of the operator-facing configuration above.

- `PIXIE_BUNDLED_PI_PACKAGE` is the archive-local Pi package path set by the `pixie` launcher. An operator-supplied value is removed and replaced.
- `PIXIE_ASSISTANT_HOST` overrides the internal loopback host endpoint for the supervisor and the assistant service. The controller-only image rejects it and other `PIXIE_ASSISTANT_*` values so a shared environment cannot configure a local Pi.
- `PIXIE_DELETION_AUTHORITY` selects `auto`, `paired` or `legacy` deletion authority; `paired` requires a pairing key.
- `PIXIE_PI_STORAGE_KEY` supplies that pairing key for controller-only runs; full-host mode derives it from the selected agent directory.
- `PIXIE_SCHEDULE_DEFAULT_MAX_RUN` sets the default schedule run limit as a whole-second duration or `off` for unlimited.
- `PIXIE_SCHEDULE_DEADLINES` enables or disables schedule deadlines (`on` or `off`).

Rejected or retired selection knobs either fail startup or are ignored: `PIXIE_ASSISTANT_PORT`, `PIXIE_PI_EXECUTABLE` and `PIXIE_PI_ARGS` are rejected by the assistant host; `PIXIE_LLAMA` is stripped from the controller child environment and rejected by controller-only mode; any non-empty `PIXIE_ADMIN_BRIDGE*` value is rejected; and `PIXIE_PI_PACKAGE` is rejected because the archive supplies the bundled Pi package.

## Optional local models

Configure local providers through the selected Pi installation. `LLAMA_BASE_URL` is passed to the native Pi session when present; Pixie does not maintain a separate provider database.

## Docker

The supplied Compose file is for source-level validation. `docker compose --profile web up -d pixie_web` starts the controller-only service, which connects to a separately installed `pixie` host. The controller-only image contains no Bun, Node or Pi. The Compose file declares no fixed container names. This does not prove or constitute an approved Docker deployment.

From the repository root:

```sh
cp .pixie.example .pixie
chmod 600 .pixie
```

Set `PIXIE_DATA_PATH`, `PIXIE_PI_SECRET_KEY` and `PIXIE_MCP_TOKEN`. The [example](../.pixie.example) lists optional addresses, authentication and resource limits. The data directory must be writable by container UID/GID `1000:1000` and mounts into the `pixie_web` container at `/var/lib/pixie/data`.

Add project roots to the selected service's mounts, preserving host absolute paths:

```yaml
- type: bind
  source: /absolute/path/to/project
  target: /absolute/path/to/project
  read_only: true
  bind:
    create_host_path: false
```

```sh
docker compose --env-file .pixie --profile web up -d pixie_web
```

The validation controller listens on <http://127.0.0.1:7312>. Containers use host networking; bridged-container loopback cannot reach host Pi. The split host listens on the required config `port` (the example uses `3284`) and `pixie_web` dials `PIXIE_PI_PORT` (default `3284`); the values must match. Controller-only mode rejects shared assistant settings so a shared environment cannot silently configure local Pi. See [security](security.md#remote-access) for authentication, public-origin, and TLS-proxy requirements.

The images are not a sandbox, and their definition alone is not deployment evidence.

## MCP and Signet

The Pixie MCP publisher lives inside the `pixie_web` process on `PIXIE_CONTROLLER_PORT` (default `7312`) and serves the Canvas and Design workspace modules; enable them in Settings → Tools with a compatible MCP extension loaded. See [Pi integration](pi.md#mcp) for endpoints and tokens. This publisher does not establish universal Pi MCP, tool, or attachment support.

Browser is an external MCP endpoint, not a Pixie module or Compose service. Point Pi at an operator-managed endpoint from Settings → Browser. The endpoint may be unauthenticated, and its hardening, isolation, and egress are the deployment's responsibility. Enable it only with an operator-enforced boundary: a loopback bind, a strong endpoint credential, `Host`/`Origin` validation, a minimal tool set, restricted egress, and isolation from Pixie state and admitted project mounts. Pixie provides no sandbox and cannot enforce these controls; if the boundary is missing, leave the Browser contribution disabled. Docker hardening and same-UID process boundaries are defense in depth, not a sandbox. See [security](security.md#browser-mcp-threat-model).

Optional Signet memory is an operator-owned external service, not a Pixie-managed MCP connection. Pi loads a managed file extension through native `<agentDir>/extensions` discovery with fail-open lifecycle hooks and hidden auto-recall. Pixie never reads or writes `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`), `signet.json` or per-session `SIGNET_ENABLED`.

## Without Docker

For local validation, run `pixie serve` and `pixie_web` as separate local processes. The host owns Pi sessions and `pixie_web` serves the UI and controller, with `pixie_web` listening on the controller port and reaching the host over loopback. Do not run a second server for the same agent directory.

This topology is not yet an approved deployment recipe. Startup must fail closed when the agent directory is missing, and readiness must degrade while a session awaits reload. A conflicting server exits `73`; stop the managed owner or use a different `PI_CODING_AGENT_DIR`. arm64 lifecycle and upgrade/rollback evidence under real systemd is still missing. See the [roadmap](../roadmap/roadmap.md) for the current evidence status.

For development inspection only, `bun run build:pixie_web` builds the embedded UI and the controller binary. Do not replace a working service until final archive installation, Pi selection, readiness, failure propagation and lifecycle checks pass.

Advanced override only: `PIXIE_STATIC_DIR` serves the web UI from a disk directory instead of the embedded bundle (development use). A Go binary built before `bun run build:webui` embeds only a placeholder and falls back to `PIXIE_STATIC_DIR`, then to the container asset path.

## Operations

Application `/health` and `/livez` check liveness; `/readyz` checks state, UI and Pi connectivity. The in-process MCP publisher shares the application listener. See [Pi integration](pi.md#mcp) for diagnostics.

Schedules run while Pixie is up. Ask a chat with MCP support to create, list, pause, resume, run or stop a schedule, or use the workspace Schedules list/detail views and the Settings schedules section. Each scheduled run is a separate chat in that project. For example, `schedule_manage` with `action: "create", prompt: "Review open tasks", cron: "0 9 * * 1-5", timezone: "Europe/Warsaw", mutationId: "weekday-review-1"` runs at 09:00 on weekdays. It uses the current project; repeat the mutation ID only for a retry of the same request. Cron has five fields and an IANA timezone (UTC by default). Pause prevents future dispatch; stop cancels the current run; run-now starts one immediately. See [state and restart behavior](architecture.md#state-and-lifecycle).

Back up Pi state, Pixie data and private environment files after active work settles. Select an exact validated release tag rather than `latest`; publication does not establish a usable deployment or its operator configuration.
