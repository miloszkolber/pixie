# Pi SDK coverage

This is a source-level inventory of the public Pi SDK boundary and Pixie policy. It is not live-runtime, provider, TUI/PTY, Docker, systemd, arm64, or publication evidence.

## Evidence and negotiation

The host verifies and imports only Pi's public package root. Its required public symbols are `createAgentSession`, `SessionManager`, `DefaultResourceLoader`, `ModelRuntime`, `AgentSessionRuntime`, and `SettingsManager` (`src/assistant/probe.ts:16-24`). The host's typed adapters mark many session and runtime members optional and turn missing members into `CapabilityError` (`src/assistant/host.ts`, `PiSession` and `PiSdkApi`).

`schema/protocol-catalog.json` is the single capability source. Every host operation carries an explicit status: `available` (the host implements it and may advertise it), `unavailable` (the public Pi capability does not exist), or `absent` (not a host route, for example controller-owned state). Every non-available operation carries its reason. The generated TypeScript and Go catalogs derive from this file (`src/shared/generated/protocol-catalog.ts:177-272`, `piprotocol/catalog_generated.go:269-360`). The Bun host builds its advertised operation set from the generated `HOST_AVAILABLE_OPERATIONS` list, so `runtime.hello` marks a route true only when the catalog marks it `available`; `runtime.restart` is additionally gated by deployment configuration (`src/assistant/host.ts` `AVAILABLE_OPERATIONS`, `operationSet`, and the `runtime.hello` result construction). The welcome reports the fixed `sessions`, `agents` and `images` capability groups alongside the exhaustive operation set; the `tools` group is not advertised because no bounded, secret-free inventory route is available.

The controller requires core lifecycle operations and gates negotiated optional operations before dispatch (`internal/controller/pi_client.go:302-343`, `internal/controller/pi_client.go:460-475`). It also drops negotiated keys the generated catalog does not name, so a host cannot authorize a route outside the catalog; the bundled host never advertises a route the catalog marks non-available. An unsupported catalogued route fails closed with a typed `UNSUPPORTED_AGENT_CAPABILITY` code, and directly gated controller routes such as steering and resource prompts also surface the catalog's stated reason (`internal/controller/pi_client.go:352-368`, `internal/controller/pi_client.go:596-604`). Administration-routed operations return the generic administration error while preserving the typed cause.

`runtime.capabilities` is intentionally unavailable, not missing: `runtime.hello` already returns the negotiated capability groups and the exhaustive operation set, and controller `pi.capabilities` derives its view from the negotiated host profile rather than calling Pi (`schema/protocol-catalog.json:118-122`, `internal/controller/pi_agents.go:32-47`).

Host operation names, source/agent files, MCP connection records, dialog projection, and controller capability mapping are Pixie policy unless a cited public Pi symbol performs the work. Do not treat a Pixie route as a public Pi SDK capability.

## Coverage

| Category | Coverage | Boundary and evidence |
| --- | --- | --- |
| Web/assistant E2E | Core session lifecycle | The host implements list, create, load, prompt, cancel, release, transcript reads, and event projection; the controller requires the core lifecycle operation bits before use (`src/assistant/host.ts` `session.list`/`session.create`/`session.load`/`session.prompt`/`session.cancel`/`session.release`/`session.getMessages`, `internal/controller/pi_client.go:325-343`). |
| Web/assistant E2E | Images and session configuration | Images are a host capability, not a route: `session.prompt` accepts image content blocks, the host advertises `images`, and the retired `session.prompt.image` name is catalogued `unavailable` with that reason. The controller enables image prompts from the `images` capability, with a legacy host that still advertises `session.prompt.image` accepted as an explicit alternative. Model and thinking configuration cross the boundary only when its negotiated operation is true, and model and thinking setters remain optional Pi session members (`internal/controller/pi_client.go:302-354`, `src/assistant/host.ts` `session.prompt`/`hostCapabilities`, `schema/protocol-catalog.json`). |
| Web/assistant E2E | Sources, agent mentions, defaults, preferences, and selected provider operations | Source and agent routes are Pixie policy; defaults, preferences, and provider portions use selected public settings/model APIs. Availability can fail closed when `SettingsManager`, `ModelRuntime`, or another required public member is unavailable (`src/assistant/host.ts` `sdkModelRuntime`, `pi.sources.*`, `pi.providers.*`, `pi.preferences.*`). |
| Native-TUI only | Normal Pi interaction and TUI extensions | Both Pi-bearing products retain the normal `pixie` command and TUI. Terminal input, custom TUI factories, headers, footers, autocomplete, and composer APIs are not rendered by the web bridge (`cmd/pixie/main.go:1-88`, [Pi integration extensions](pi.md#extensions)). |
| Native-TUI only | Real TUI and extension PTY behavior | Archive contents retain the TUI bundle, but no executed runtime/PTY evidence is recorded here. This remains unproven. |
| Controller-only | Workspace and application state | Projects, read-only files/Git, goals, schedules, web state, diagnostics, and browser-MCP registration belong to the Go controller rather than Pi's SDK (`schema/protocol-catalog.json:87-189`, `internal/controller/runtime.go:180-266`). Git inspection also exposes the read-only `git.turnDiff` route, which returns the per-turn diff for a `write` or `edit` tool call and reports a Git execution failure as an unavailable result rather than a request error (`internal/workspace/turn_diff.go`, `internal/controller/handler.go:265-279`). |
| Controller-only | Browser MCP pointer | Pixie stores and registers an operator-chosen endpoint; Pi connects to that endpoint directly. Pixie is neither a browser nor an MCP proxy (`internal/controller/mcp_browser.go`). |
| Partial | Fork, clone, compaction, rename, statistics, follow-up, commands, and skills | The host has routes, but each relies on optional public Pi members or is best-effort. Command enumeration tolerates failures and returns what the resident session exposes (`src/assistant/host.ts` `session.commands`, `slashCommands`). |
| Partial | Native resource and extension configuration | Pixie projects selected public settings and package-manager results into its policy surface. It can write confirmed top-level configuration, but loaded state is deferred and non-top-level resources are rejected (`src/assistant/host.ts` `pi.extensions.list`, `pi.extensions.configure`, `pi.config.extensions.*`). |
| Partial | MCP connection records | Pixie host policy keeps configured and per-session HTTP/streamable-HTTP connection records. It is not proof of a public Pi MCP attachment or tool API (`src/assistant/host.ts` `pi.config.extensions.*`). |
| Partial | Providers, defaults, and preferences | Inventory, readiness, login, default selection, selected preferences, and a typed provider configuration provenance projection (`pi.providers.config.read`) are implemented through public APIs. The projection reports field-presence and a fixed source enum, never a key, token, URL or path, so settings can explain `defaults` or `unknown` before a mutation (`src/assistant/host.ts` `providerConfigProjection`, `pi.providers.config.read`). `pi.preferences.read` adds a per-key `writable`/`source` descriptor from the fixed `pi`, `controller` or `read-only` enum; `compactionReserveTokens` is reported `read-only` because the selected Pi exposes no public setter, and the settings UI disables it. Save and reset tolerate an unchanged read-only key but fail closed when a caller changes or resets a stored one (`internal/controller/pi_admin.go` `ReadPreferences`, `SavePreferences`, `ResetPreferences`). The settings provider card renders `Default connection only` for defaults and `Configuration could not be verified` for unknown rather than offering a blind mutation (`webui/src/settings/sections/provider-card.svelte:87`, `internal/controller/provider_configuration.go:12-64`). Support remains partial: provider configuration is not universal. |
| Unavailable | Steering (`session.steer`, `pi.session.steer`) | Pi `0.85.1` exposes no public active-run identity and no steering acceptance receipt, so a request cannot be proven to target the run the client observed. The catalog marks both names `unavailable` with that reason, the host does not advertise them, and the controller fails closed with the reason before dispatch (`schema/protocol-catalog.json:39-43`, `schema/protocol-catalog.json:103-107`, `src/assistant/host.ts:3292-3311`, `src/assistant/steering.ts`). |
| Unavailable | `runtime.capabilities` | Intentionally unavailable, not missing: `runtime.hello` already returns the negotiated capability groups and the exhaustive operation set, so the host does not advertise a second capability route (`schema/protocol-catalog.json:118-122`). |
| Unavailable | Tool inventory, tool invocation, and resource attachments | Pi `0.85.1` exposes no public bounded, secret-free tool inventory (`pi.tools.list`) or invocation API (`pi.tools.call`), and no public text-resource attachment API (`session.prompt.resource`). The host does not advertise the `tools` capability; the controller call path fails closed with a typed unsupported-capability error; and the UI probes `session.toolList` only when the `tools` capability is advertised, so it does not probe an unavailable native API (`schema/protocol-catalog.json:70-74`, `schema/protocol-catalog.json:108-117`, `internal/controller/pi_extensions.go:278-294`, `webui/src/settings/sections/pi-tools-settings.ts:39-44`). |
| Unavailable | Nonresident session metadata (`pi.session.info`) | Pi `0.85.1` exposes no public nonresident session metadata beyond `SessionManager.list`, so the catalog marks the route `unavailable` (`schema/protocol-catalog.json:83-87`). |
| Unavailable | Subagent execution (`pi.subagent.execute`) | Pi `0.85.1` exposes no public subagent execution API; Pixie authors Markdown agent definitions and an installed native extension performs execution (`schema/protocol-catalog.json:228-232`). |
| Unavailable | Local models and generic native-extension routes (`pi.llama`, `pi.native-extensions`) | llama.cpp management remains native-TUI-only with no public SDK route, and native extensions use the specific `pi.extensions.*` and `pi.config.extensions.*` routes rather than a generic operation (`schema/protocol-catalog.json:291-300`). |
| Unavailable | TUI handoff (`runtime.releaseToTui`) | There is no public TUI handoff from the in-process host (`schema/protocol-catalog.json:61-65`). |
| Absent/not Pi | Controller-owned session and plan state (`session.delete`, `session.archive`, `pi.todo.plan`) | Session deletion is controller-owned recoverable state and is gated on the negotiated `session.delete` operation. Archive is controller-owned too: the Bun host negotiates no archive route and the controller persists no archive marker, so `session.archive` and `session.unarchive` fail closed with no host dispatch, no credential revocation and no lifecycle event; the Archive primary area is unusable against the real host while no durable archive marker exists. Plan state is controller-owned with no public Pi plan API. The catalog marks the browser methods `session.archive` and `session.unarchive` `unavailable` and the host routes `session.archive`, `pi.session.archive` and `pi.session.unarchive` `absent`; there is no `session.unarchive` host operation, and the host dispatches no Pi route for them (`schema/protocol-catalog.json:49-52`, `schema/protocol-catalog.json:95-103`, `schema/protocol-catalog.json:718-733`). |
| Absent/not Pi | Canvas and Openfig production capability | Canvas and Design are controller modules, disabled by default. They do not establish Pi SDK coverage; Canvas lacks a contained production worker, and Openfig lacks a licensed `.fig` parser and worker ([Canvas blockers](../roadmap/roadmap-canvas.md#blockers), [Openfig blockers](../roadmap/roadmap-openfig.md#blockers)). |

## Bundled native interface

`pixie` ships the bundled Pi TUI/CLI at Pi `0.85.1`. The root `pixie` command runs `runtime/bin/bun runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js`, so interactive input, native commands and TUI extension surfaces execute inside Pi's own process and never cross the controller-host protocol. The web bridge renders only the negotiated subset in the table above; terminal input, custom TUI factories, headers, footers, autocomplete, composer APIs and `/llama` management remain native-TUI-only.

The host is not a Pi extension. An extension lives only inside a Pi process and runs under the TUI runtime; it cannot own a durable multi-session registry, provider/model/settings or MCP mutation, or deletion authority, and it would expose the controller secret to every loaded extension and the model's bash tool. A dedicated owner-locked host process remains required. An in-TUI extension can only ever be an optional additive surface that never owns sessions or the loopback endpoint.

Archive layout and release-runtime checks verify that the bundled TUI/CLI is present and Pi RPC is absent. Interactive TUI/PTY behavior and credentialed Pi remain unproven, as in the evidence limits at the top of this document.

## Method coverage

Every browser method has one generated owner, route and effective status; the table below enumerates all 102 methods from `schema/protocol-catalog.json`. `available` means the declared owner implements the route; `unavailable` means it is catalogued but fails closed with the stated reason; `absent` means it is not a host route. New methods land with a row here and a catalog status.

### available (98)

| Method | Route | Owner |
| --- | --- | --- |
| `browserMcp.configure` | `controller:browser-mcp` | controller |
| `browserMcp.remove` | `controller:browser-mcp` | controller |
| `browserMcp.status` | `controller:browser-mcp` | controller |
| `directory.list` | `workspace:files` | workspace |
| `fs.readDir` | `workspace:files` | workspace |
| `fs.readFile` | `workspace:files` | workspace |
| `git.diffFile` | `workspace:git` | workspace |
| `git.listBranches` | `workspace:git` | workspace |
| `git.listCommits` | `workspace:git` | workspace |
| `git.listRepositories` | `workspace:git` | workspace |
| `git.status` | `workspace:git` | workspace |
| `git.turnDiff` | `workspace:git` | workspace |
| `history.search` | `controller:history-index` | controller |
| `mcpAdapter.status` | `controller:mcp-adapter` | controller |
| `mcpRegistry.catalog` | `controller:mcp-registry` | controller |
| `mcpRegistry.moduleRestart` | `controller:mcp-registry` | controller |
| `mcpRegistry.moduleSetEnabled` | `controller:mcp-registry` | controller |
| `model.clampThinking` | `controller:session-config` | controller |
| `model.list` | `pi.providers.list` | pi |
| `model.refresh` | `pi.providers.inventory.refresh` | pi |
| `model.setAllVisibility` | `controller:settings` | controller |
| `model.setVisibility` | `controller:settings` | controller |
| `model.thinkingLevels` | `controller:session-config` | controller |
| `pi.agentCreate` | `pi.sources.create` | pi |
| `pi.agentDelete` | `pi.sources.delete` | pi |
| `pi.agentList` | `pi.sources.list` | pi |
| `pi.agentUpdate` | `pi.sources.update` | pi |
| `pi.capabilities` | `controller:pi-capabilities` | controller |
| `pi.defaultsClear` | `pi.defaults.clear` | pi |
| `pi.defaultsRead` | `pi.defaults.read` | pi |
| `pi.defaultsSave` | `pi.defaults.save` | pi |
| `pi.extensionAdd` | `pi.config.extensions.add` | pi |
| `pi.extensionList` | `pi.config.extensions.list` | pi |
| `pi.extensionRemove` | `pi.config.extensions.remove` | pi |
| `pi.extensionSetEnabled` | `pi.config.extensions.set-enabled` | pi |
| `pi.nativeExtensionConfigure` | `pi.extensions.configure` | pi |
| `pi.nativeExtensionReload` | `controller:deferred-reload` | controller |
| `pi.nativeExtensions` | `pi.extensions.list` | pi |
| `pi.preferencesRead` | `pi.preferences.read` | pi |
| `pi.preferencesReset` | `pi.preferences.reset` | pi |
| `pi.preferencesSave` | `pi.preferences.save` | pi |
| `pi.reload` | `runtime.restart` | pi |
| `pi.status` | `controller:pi-status` | controller |
| `project.close` | `controller:projects` | controller |
| `project.list` | `controller:projects` | controller |
| `project.open` | `controller:projects` | controller |
| `project.update` | `controller:projects` | controller |
| `project.watchReady` | `controller:project-watches` | controller |
| `provider.loginCancel` | `provider.loginCancel` | pi |
| `provider.loginReply` | `provider.loginReply` | pi |
| `provider.loginStart` | `provider.loginStart` | pi |
| `provider.logout` | `pi.providers.config.delete` | pi |
| `provider.readiness` | `pi.providers.readiness.check` | pi |
| `provider.status` | `pi.providers.list` | pi |
| `runtime.diagnostics` | `controller:runtime-diagnostics` | controller |
| `runtime.status` | `controller:runtime-status` | controller |
| `runtime.supportSnapshot` | `controller:runtime-support-snapshot` | controller |
| `schedule.create` | `controller:scheduler` | controller |
| `schedule.delete` | `controller:scheduler` | controller |
| `schedule.health` | `controller:scheduler` | controller |
| `schedule.list` | `controller:scheduler` | controller |
| `schedule.preview` | `controller:scheduler` | controller |
| `schedule.runNow` | `controller:scheduler` | controller |
| `schedule.stop` | `controller:scheduler` | controller |
| `schedule.update` | `controller:scheduler` | controller |
| `session.abort` | `session.cancel` | pi |
| `session.confirmExternalDeletion` | `controller:deletions` | controller |
| `session.create` | `session.create` | pi |
| `session.delete` | `controller:deletions` | controller |
| `session.deletionRecovery` | `controller:deletions` | controller |
| `session.extensionAdd` | `pi.session.extensions.add` | pi |
| `session.extensionList` | `pi.session.extensions.list` | pi |
| `session.extensionRemove` | `pi.session.extensions.remove` | pi |
| `session.fork` | `session.fork` | pi |
| `session.getAgentMentions` | `pi.agent-mentions.list` | pi |
| `session.getCommands` | `pi.slash-commands.list` | pi |
| `session.getMessages` | `session.getMessages` | pi |
| `session.getStats` | `controller:session-stats` | controller |
| `session.goalClear` | `controller:objectives` | controller |
| `session.goalGet` | `controller:objectives` | controller |
| `session.goalSet` | `controller:objectives` | controller |
| `session.list` | `session.list` | pi |
| `session.prompt` | `session.prompt` | pi |
| `session.queueAdd` | `controller:queues` | controller |
| `session.queueEdit` | `controller:queues` | controller |
| `session.queueRemove` | `controller:queues` | controller |
| `session.queueRetry` | `controller:queues` | controller |
| `session.release` | `controller:leases` | controller |
| `session.rename` | `session.rename` | pi |
| `session.retainExternalDeletion` | `controller:deletions` | controller |
| `session.setConfigOption` | `session.configure` | pi |
| `session.setLeases` | `controller:leases` | controller |
| `session.setModel` | `session.configure` | pi |
| `session.setThinkingLevel` | `session.configure` | pi |
| `session.uiCancel` | `session.uiCancel` | pi |
| `session.uiReply` | `session.uiResponse` | pi |
| `settings.update` | `controller:settings` | controller |
| `skill.list` | `pi.slash-commands.list` | pi |

### unavailable (4)

| Method | Route | Owner | Reason |
| --- | --- | --- | --- |
| `session.archive` | `controller:archive` | controller | Archive is controller-owned state; this controller fails closed until a durable archive marker exists. |
| `session.steer` | `session.steer` | pi | Pi 0.85.1 exposes no public run identifier to bind a steering request to the active run. |
| `session.toolList` | `pi.tools.list` | pi | Pi 0.85.1 exposes no public bounded, secret-free tool inventory API. |
| `session.unarchive` | `controller:archive` | controller | Archive is controller-owned state; this controller fails closed until a durable archive marker exists. |

## Decisions

- **AUX-03 steering.** Keep `session.steer` and `pi.session.steer` unavailable. The spike found that the host-owned generation guard plus the prompt `preflightResult` acceptance is necessary but not sufficient: Pi `0.85.1` exposes no public active-run identity and no steering acceptance receipt, and preflight acceptance reports only that a prompt was accepted, not that a later steer call was bound or delivered. The host therefore dispatches no route, records the evaluated reason, and throws a `CapabilityError`; the controller fails closed with the catalog reason before any native side effect; the binding registry is intentionally not wired to a negotiable route (`src/assistant/steering.ts`, `src/assistant/host.ts:3292-3311`, `tests/assistant/host-steering.test.ts`). Revisit only if Pi exposes a public run identity or a steering acceptance result that makes binding provable.
- **AUX-26 archive.** Keep `session.archive` and `session.unarchive` explicitly unavailable. The Bun host negotiates no archive route: the browser methods `session.archive` and `session.unarchive` are catalogued `unavailable` on `controller:archive`, there is no `session.unarchive` host operation, and the host operations `session.archive`, `pi.session.archive` and `pi.session.unarchive` are catalogued `absent`, so the host cannot advertise them; the controller persists no archive marker. Both calls fail closed before any controller mutation, with no host dispatch, no MCP credential revocation, no resident removal and no archived lifecycle event (`internal/controller/session_lifecycle.go:191-263`, `tests/go/controller/session_lifecycle_test.go`). A future marker must survive restart, reconcile the active/archived list filter, preserve native identity and deletion authority, and emit lifecycle events only after publication.
- **AUX-06 provider usage.** Live provider usage and rate-limit display needs a host route that the selected Pi does not expose: `checkAuth` and the auth-status surface report only configured/source, never a usage or rate-limit value. The controller's short-TTL provider inventory therefore reuses the already-resolved auth projection instead of issuing a second policy or round trip, Pi stays authoritative after the TTL or an invalidating mutation, and Pixie never writes provider configuration (`src/assistant/host.ts:2660-2700`, `src/assistant/host.ts:3531-3539`, `internal/controller/provider_inventory.go:10-41`, `tests/go/controller/provider_inventory_test.go:118-130`).
- **AUX-11 session index.** No second session index. The controller lists sessions through the host catalog; an append-only summary cache or index is added only if a benchmark under `tests/performance/` measures listing as a bottleneck. No such measurement exists, so the default is no index.

## Prioritized additions

1. Keep steering unavailable until Pi exposes a public run identity or a steering acceptance receipt that safely binds a request to the active run.
2. Add a public resource-attachment API, or keep resource prompts unavailable.
3. Obtain a public, bounded, secret-free per-session tool inventory and invocation API before advertising the `tools` capability or enabling the inventory UI.
4. Add a public setter for each preference that Pi exposes one for; `compactionReserveTokens` stays read-only through the typed per-key descriptor until then.
5. Maintain the generated catalog as the single capability source: every host operation records an explicit status and reason, the host advertises only `available` routes, and each new route lands with tested public-SDK and negotiated-operation evidence.
