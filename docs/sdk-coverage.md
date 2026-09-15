# Pi SDK coverage

This is a source-level inventory of the public Pi SDK boundary and Pixie policy. It is not live-runtime, provider, TUI/PTY, Docker, systemd, arm64, or publication evidence.

## Evidence and negotiation

The host verifies and imports only Pi's public package root. Its required public symbols are `createAgentSession`, `SessionManager`, `DefaultResourceLoader`, `ModelRuntime`, `AgentSessionRuntime`, and `SettingsManager` (`assistant/src/probe.ts:16-24`). The host's typed adapters mark many session and runtime members optional and turn missing members into `CapabilityError` (`assistant/src/host.ts:114-181`).

`runtime.hello` returns capability groups and an exhaustive host operation set; it reports `sessions` and `agents`, while each catalogued host operation is marked by the host implementation set (`assistant/src/host.ts:56-112`, `assistant/src/host.ts:300-314`). The controller requires core operations and gates negotiated optional operations before dispatch (`web/internal/controller/pi_client.go:299-343`, `web/internal/controller/pi_client.go:427-456`).

`shared/schema/protocol-catalog.json` names candidate operations; it is not implementation or public-SDK proof (`shared/schema/protocol-catalog.json:4-86`). In particular, `runtime.capabilities` is catalogued but absent from the host implementation set, and controller `pi.capabilities` derives a view from the negotiated host profile rather than calling Pi (`web/internal/controller/pi_agents.go:32-47`).

Host operation names, source/agent files, MCP connection records, dialog projection, and controller capability mapping are Pixie policy unless a cited public Pi symbol performs the work. Do not treat a Pixie route as a public Pi SDK capability.

## Coverage

| Category | Coverage | Boundary and evidence |
| --- | --- | --- |
| Web/assistant E2E | Core session lifecycle | The host implements list, create, load, prompt, cancel, release, transcript reads, and event projection; the controller requires the core lifecycle operation bits before use (`assistant/src/host.ts:2376-2458`, `assistant/src/host.ts:2575-2698`, `web/internal/controller/pi_client.go:325-343`). |
| Web/assistant E2E | Images and session configuration | Image prompts and model/thinking configuration cross the controller-host boundary only when their negotiated operation is true; model and thinking setters remain optional Pi session members (`web/internal/controller/pi_client.go:530-570`, `assistant/src/host.ts:2460-2569`). |
| Web/assistant E2E | Sources, agent mentions, defaults, preferences, and selected provider operations | Source and agent routes are Pixie policy; defaults, preferences, and provider portions use selected public settings/model APIs. Availability can fail closed when `SettingsManager`, `ModelRuntime`, or another required public member is unavailable (`assistant/src/host.ts:1990-2023`, `assistant/src/host.ts:2699-2833`, `assistant/src/host.ts:2882-3037`). |
| Native-TUI only | Normal Pi interaction and TUI extensions | Both Pi-bearing products retain the normal `pixie` command and TUI. Terminal input, custom TUI factories, headers, footers, autocomplete, and composer APIs are not rendered by the web bridge (`web/cmd/pixie/main.go:1-88`, [Pi integration extensions](pi.md#extensions)). |
| Native-TUI only | Real TUI and extension PTY behavior | Archive contents retain the TUI bundle, but no executed runtime/PTY evidence is recorded here. This remains unproven. |
| Controller-only | Workspace and application state | Projects, read-only files/Git, goals, schedules, web state, diagnostics, and browser-MCP registration belong to the Go controller rather than Pi's SDK (`shared/schema/protocol-catalog.json:87-189`, `web/internal/controller/runtime.go:180-266`). |
| Controller-only | Browser MCP pointer | Pixie stores and registers an operator-chosen endpoint; Pi connects to that endpoint directly. Pixie is neither a browser nor an MCP proxy (`web/internal/controller/mcp_browser.go`). |
| Partial | Fork, clone, compaction, rename, statistics, follow-up, commands, and skills | The host has routes, but each relies on optional public Pi members or is best-effort. Command enumeration tolerates failures and returns what the resident session exposes (`assistant/src/host.ts:2571-2665`). |
| Partial | Native resource and extension configuration | Pixie projects selected public settings and package-manager results into its policy surface. It can write confirmed top-level configuration, but loaded state is deferred and non-top-level resources are rejected (`assistant/src/host.ts:3038-3208`). |
| Partial | MCP connection records | Pixie host policy keeps configured and per-session HTTP/streamable-HTTP connection records. It is not proof of a public Pi MCP attachment or tool API (`assistant/src/host.ts:3210-3349`). |
| Partial | Providers, defaults, and preferences | Inventory, readiness, login, default selection, and selected preferences are implemented through public APIs; provider configuration is not universal, and `compactionReserveTokens` has no public setter (`assistant/src/host.ts:2882-3037`). |
| Unavailable | Steering | The host rejects steering because Pi `0.85.1` has no public run identifier that can bind it to the active run (`assistant/src/host.ts:2666-2671`). |
| Unavailable | `runtime.capabilities` | The schema lists it, but the host operation set marks only explicitly implemented routes and does not include it (`shared/schema/protocol-catalog.json:29-35`, `assistant/src/host.ts:56-112`). |
| Unavailable | Universal tool inventory and resource attachments | The controller has a `pi.tools.list` call path, but the host does not register that operation; `session.prompt.resource` is catalogued but likewise not registered (`web/internal/controller/pi_extensions.go:278-294`, `shared/schema/protocol-catalog.json:19-35`, `assistant/src/host.ts:56-112`). |
| Absent/not Pi | Canvas and Openfig production capability | Canvas and Design are controller modules, disabled by default. They do not establish Pi SDK coverage; Canvas lacks a contained production worker, and Openfig lacks a licensed `.fig` parser and worker ([Canvas blockers](../roadmap/roadmap-canvas.md#blockers), [Openfig blockers](../roadmap/roadmap-openfig.md#blockers)). |

## Bundled native interface

`pixie_cli` and full `pixie` ship the bundled Pi TUI/CLI at Pi `0.85.1`. The root `pixie` command runs `runtime/bin/bun runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js`, so interactive input, native commands and TUI extension surfaces execute inside Pi's own process and never cross the controller-host protocol. The web bridge renders only the negotiated subset in the table above; terminal input, custom TUI factories, headers, footers, autocomplete, composer APIs and `/llama` management remain native-TUI-only.

The host is not a Pi extension. An extension lives only inside a Pi process and runs under the TUI runtime; it cannot own a durable multi-session registry, provider/model/settings or MCP mutation, or deletion authority, and it would expose the controller secret to every loaded extension and the model's bash tool. A dedicated owner-locked host process remains required. An in-TUI extension can only ever be an optional additive surface that never owns sessions or the loopback endpoint.

Archive layout and release-runtime checks verify that the bundled TUI/CLI is present and Pi RPC is absent. Interactive TUI/PTY behavior and credentialed Pi remain unproven, as in the evidence limits at the top of this document.

## Prioritized additions

1. Obtain a public Pi run identifier and expose it with a safe steering contract.
2. Add a public resource-attachment API, or keep resource prompts unavailable.
3. Obtain a public, authoritative per-session tool inventory instead of projecting an unavailable host route.
4. Define a public typed provider/settings configuration contract that makes support discoverable before mutation.
5. Keep one negotiated capability source: replace catalog-name inference with tested operation-set and public-SDK evidence for every new route.
