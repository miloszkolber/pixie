# Pi

This directory contains `pixie-assistant/`, the small Bun service around the unmodified Pi SDK, and [agents/](agents/README.md), which documents user-owned definitions.

## Setup

Start with a checkout and the Bun version in root [`package.json`](../package.json). Root `package.json` and `bun.lock` are the workspace dependency authority. [`pixie-assistant/package.json`](pixie-assistant/package.json) pins the SDK and essential service dependencies. Optional packages are development dependencies for parity tests, not runtime requirements. An existing Pi configuration can be reused in place without copying credentials or native state.

Install once from the repository root:

```sh
bun install --frozen-lockfile
```

Start the assistant with the chosen Pi state directory:

```sh
bun agent/pixie-assistant/src/main.ts --agent-dir /absolute/path/to/pi-agent
```

Before starting, supply `PIXIE_PI_SECRET_KEY` through your private environment, as described in [deployment](../docs/deployment.md#host-service). Provider authentication and model selection remain native Pi operations. Use separate sessions for simultaneous vanilla Pi CLI and host work.

## Native extensions

Install and configure optional packages with Pi's native package commands and settings. User file extensions in `<agentDir>/extensions` and project resources load through Pi's `DefaultResourceLoader`. No package-specific Pixie wrapper or marker is needed to execute a tool or use the generic UI bridge. See [Pi extensions](../docs/pi-extensions.md) for optional integrations and the read-only inventory contract.

Agent-definition CRUD is an application API, not an extension or an execution runtime. MCP administration becomes available when an installed adapter answers its public runtime event. The explicit `--llama` bootstrap retains the SDK's local provider until equivalent public built-in startup is available.

## Signet bootstrap

Install the managed extension with the Signet CLI (`signet setup`, see [deployment](../docs/deployment.md)) or, for an isolated agent directory, run the installed connector's public `PiConnector.install()` API with `PI_CODING_AGENT_DIR` pointed at that directory, without copying upstream implementation into this repository. Only the generated `extensions/signet-pi.js` file belongs in the agent directory; never the installer's global configuration. A different existing Signet extension is preserved — move it aside before installing.

Installer generation does not inherit Signet credentials, workspace paths or endpoint overrides because upstream can embed them into generated JavaScript. Supply `SIGNET_DAEMON_URL`, `SIGNET_PATH`, `SIGNET_AGENT_ID` and credentials through the runtime environment if needed. Existing `extensions/signet.json` remains untouched. The external daemon is operator-owned and is not installed, configured, started or probed by Pixie. An unavailable daemon remains fail-open. To disable memory, use Signet's operator-owned `SIGNET_ENABLED=false` environment setting. External web-search configuration likewise stays operator-owned, as described in the [extension contract](../docs/pi-extensions.md).

## Verification

Pi-facing tests live in [`pixie/tests/pi-native-parity/`](../pixie/tests/pi-native-parity/). They use disposable directories, not real `~/.pi`. See [development](../docs/development.md) for broader checks. They do not establish daemon availability, provider inference, Browser/MCP adapter parity or a live deployment.

The pinned upstream subagent runner derives child commands from `process.execPath` and supplies an entry script only when that executable is named `node`. Under Bun it invokes bare Bun with Pi arguments; the checked-in [subagent patch](extensions/local-patches/README.md) resolves Pi's public RPC entrypoint for that case instead. Child inference through this launcher is covered by the parity suite's real-child tests.
