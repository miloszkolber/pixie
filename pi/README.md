# Pi

This directory supplies a reproducible overlay on the unmodified Pi SDK, not a Pi fork or replacement executable. `host/` owns the Bun service and extension bridges. `config/profiles.json` records the blessed extension sets, and [agents/](agents/README.md) documents user-owned definitions.

## Setup

Start with a checkout and the Bun version in root [`package.json`](../package.json). Root `package.json` and `bun.lock` are the only workspace dependency authority. [`host/package.json`](host/package.json) pins Pi and upstream extensions. The root `patchedDependencies` declaration applies the checked-in [MCP host API patch](extensions/local-patches/README.md) during installation. Keep that patch with the manifests and lockfile for a clean install. No global Pi package, duplicate lockfile or additional dependency is required. An existing vanilla Pi installation and its configuration can be reused in place, without copying credentials or native state.

Install once from the repository root:

```sh
bun install --frozen-lockfile
```

Pick an extension set from [`config/profiles.json`](config/profiles.json) (`overlay` is the full set below) and start the host, replacing `/absolute/path/to/pi-agent` with the chosen Pi state directory (for example, your existing `~/.pi/agent` expanded to an absolute path):

```sh
bun pi/host/src/main.ts --agent-dir /absolute/path/to/pi-agent --extensions mcp,agents,rpiv-todo,rpiv-web,rpiv-ask,signet,pi-subagent,pi-mcp-adapter,llama
```

Before starting, supply `PIXIE_PI_SECRET_KEY` through your private environment, as described in [deployment](../docs/deployment.md#host-service). Provider authentication and model selection remain native Pi operations. Use separate sessions for simultaneous vanilla Pi CLI and host work.

## Profiles

[`config/profiles.json`](config/profiles.json) records the blessed extension sets:

- `baseline`: no optional host factories. Native user resources and extensions still load.
- `overlay`: upstream todo, web, questions, Signet, subagents and llama.cpp, plus agent-definition CRUD and the upstream MCP adapter selected through `mcp`.
- `adapter-evaluation`: the same overlay using the explicit `pi-mcp-adapter` name.

Both MCP names use the same pinned upstream runtime with public raw resource and App-tool APIs. See [MCP client verification](../docs/mcp-client-verification.md) for contract checks and the patch support boundary. Profiles do not disable existing user extensions or resolve user-created duplicate tool registrations. Switching profiles changes only host factories.

## Signet bootstrap

Install the managed extension with the Signet CLI (`signet setup`, see [deployment](../docs/deployment.md)) or, for an isolated agent directory, run the installed connector's public `PiConnector.install()` API with `PI_CODING_AGENT_DIR` pointed at that directory, without copying upstream implementation into this repository. Only the generated `extensions/signet-pi.js` file belongs in the agent directory; never the installer's global configuration. A different existing Signet extension is preserved — move it aside before installing.

Installer generation does not inherit Signet credentials, workspace paths or endpoint overrides because upstream can embed them into generated JavaScript. Supply `SIGNET_DAEMON_URL`, `SIGNET_PATH`, `SIGNET_AGENT_ID` and credentials through the runtime environment if needed. Existing `extensions/signet.json` remains untouched. The external daemon is operator-owned and is not installed, configured, started or probed by Pixie. An unavailable daemon remains fail-open. To disable memory, use Signet's operator-owned `SIGNET_ENABLED=false` environment setting; Pi auto-discovers the managed file under every profile. External web-search configuration likewise stays operator-owned, as described in the [extension contract](../docs/pi-extensions.md).

## Verification

Pi-facing tests live in [`pixie/tests/pi-native-parity/`](../pixie/tests/pi-native-parity/). They use disposable directories, not real `~/.pi`. See [development](../docs/development.md) for broader checks. They do not establish daemon availability, provider inference, Browser/MCP adapter parity or a live deployment.

The pinned upstream subagent runner derives child commands from `process.execPath` and supplies an entry script only when that executable is named `node`. Under Bun it invokes bare Bun with Pi arguments; the checked-in [subagent patch](extensions/local-patches/README.md) resolves Pi's public RPC entrypoint for that case instead. Child inference through this launcher is covered by the parity suite's real-child tests.
