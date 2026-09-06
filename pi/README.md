# Pi

This directory supplies a reproducible overlay on the unmodified Pi SDK, not a Pi fork or replacement executable. `host/` owns the Bun service and extension bridges. `mcp/` preserves the [compatibility entry](mcp/README.md) for the upstream MCP adapter. `config/profiles.json` selects host factories, `scripts/` owns setup, and [agents/](agents/README.md) documents user-owned definitions.

## Setup

Start with a checkout and the Bun version in root [`package.json`](../package.json). The CLI rejects a different Bun version. Root `package.json` and `bun.lock` are the only workspace dependency authority. [`host/package.json`](host/package.json) pins Pi and upstream extensions. The root `patchedDependencies` declaration applies the checked-in [MCP host API patch](extensions/local-patches/README.md) during installation. Keep that patch with the manifests and lockfile for a clean install. No global Pi package, duplicate lockfile or additional dependency is required. An existing vanilla Pi installation and its configuration can be reused in place, without copying credentials or native state.

Run from the repository root, replacing `/absolute/path/to/pi-agent` with the chosen Pi state directory (for example, your existing `~/.pi/agent` expanded to an absolute path):

```sh
bun pi/scripts/overlay.ts install
bun pi/scripts/overlay.ts configure --agent-dir /absolute/path/to/pi-agent --profile overlay --dry-run
bun pi/scripts/overlay.ts configure --agent-dir /absolute/path/to/pi-agent --profile overlay
bun pi/scripts/overlay.ts verify --agent-dir /absolute/path/to/pi-agent
bun pi/scripts/overlay.ts start --agent-dir /absolute/path/to/pi-agent
```

Before `start`, supply `PIXIE_PI_SECRET_KEY` through your private environment, as described in [deployment](../docs/deployment.md#host-service). The CLI does not create secrets or install a service. It launches the existing host in the foreground on its normal loopback address and port. Provider authentication and model selection remain native Pi operations. Use separate sessions for simultaneous vanilla Pi CLI and host work.

`install` runs the frozen production install for the host and MCP workspaces from the repository root, regardless of the caller's working directory, then checks the installed direct dependency versions and both patched public MCP APIs. `install --dry-run` prints the command without changing dependencies. `configure` requires an explicit profile and agent directory. `verify` checks dependencies, patched APIs, saved profile and managed Signet content without contacting a daemon or provider. `start` runs those checks before loading the host with the saved profile. Unknown arguments and command-inappropriate options fail.

## Profiles and state

[`config/profiles.json`](config/profiles.json) is the profile source of truth:

- `baseline`: no optional host factories. Native user resources and extensions still load.
- `overlay`: upstream todo, web, questions, Signet, subagents and llama.cpp, plus agent-definition CRUD and the upstream MCP adapter selected through `mcp`.
- `adapter-evaluation`: the same overlay using the explicit `pi-mcp-adapter` name. The saved profile name remains supported, with one MCP runtime and tool surface.

Both MCP names use the same pinned upstream runtime with public raw resource and App-tool APIs. See [MCP client verification](../docs/mcp-client-verification.md) for contract checks and the patch support boundary. Profiles do not disable existing user extensions or resolve user-created duplicate tool registrations.

Configuration writes only `<agentDir>/pixie-overlay.json` and, when Signet is selected, `<agentDir>/extensions/signet-pi.js`. Native `settings.json`, `auth.json`, `models.json`, trust, prompts and agent definitions are never rewritten. Writes use private files and atomic publication. Symlinked destination paths are rejected. Repeating configuration with unchanged inputs is a no-op. A different existing Signet extension is preserved and reported for manual review, including when a pinned connector update requires replacement. Review and move that one file aside before rerunning configure. Do not run competing configuration commands on the same directory.

Switching profiles changes only host factories. It never deletes the Signet file: Pi auto-discovers that file even under `baseline`. To disable memory, use Signet's operator-owned `SIGNET_ENABLED=false` environment setting. A saved managed Signet file remains verified after a profile switch.

## Signet bootstrap

Configuration calls the installed connector's public `PiConnector.install()` API in a disposable subprocess and publishes its generated extension, without copying upstream implementation into this repository. Upstream honors `PI_CODING_AGENT_DIR`, but also cleans legacy copies under `HOME` and writes `XDG_CONFIG_HOME/signet/pi.json`. All three locations are isolated during generation. Only the verified extension reaches the selected agent directory, never the installer's global configuration. Runtime `PI_CODING_AGENT_DIR` is set to the same selected directory before importing Pi.

Installer generation does not inherit Signet credentials, workspace paths or endpoint overrides because upstream can embed them into generated JavaScript. Supply `SIGNET_DAEMON_URL`, `SIGNET_PATH`, `SIGNET_AGENT_ID` and credentials through the runtime environment if needed. Existing `extensions/signet.json` remains untouched. The external daemon is operator-owned and is not installed, configured, started or probed by this CLI. An unavailable daemon remains fail-open. External web-search configuration likewise stays operator-owned, as described in the [extension contract](../docs/pi-extensions.md).

## Verification

Pi-facing tests live in the existing [`pixie/tests/pi-native-parity/`](../pixie/tests/pi-native-parity/) directory:

```sh
bun test pixie/tests/pi-native-parity/overlay-bootstrap.test.ts
```

These tests use disposable directories, not real `~/.pi`. They cover a fresh frozen production install with the root patch, isolated configure/verify, native-state preservation, repeated bootstrap, profile/verification agreement, missing patched APIs, drift, unsafe destinations and argument validation. The clean-install check invokes the bootstrap from outside its copied checkout, verifies both public MCP APIs through the normal dependency check and confirms the lockfile stays unchanged. It needs registry access or a populated Bun cache and enough temporary disk space for a separate production install. They do not establish daemon availability, provider inference, Browser/MCP adapter parity or a live deployment. See [development](../docs/development.md) for broader checks.

The pinned upstream subagent runner derives child commands from `process.execPath` and supplies an entry script only when that executable is named `node`. Under Bun it invokes bare Bun with Pi arguments. This bootstrap does not patch that upstream execution path. Child inference through this launcher requires separate verification despite the existing host's recorded parity result.
