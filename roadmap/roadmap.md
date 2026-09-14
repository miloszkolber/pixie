# Pixie roadmap

This is the canonical implementation plan and forward backlog for Pixie. Operating behavior belongs in `docs/`. The project is in an experimental development phase: obsession with exhaustive evidence is out, and tests should be simple, effective and useful. Release evidence is produced under `web/scripts` and is frozen — no new evidence rows are added.

## Target architecture

Pixie is a self-hosted web workspace for the operator's installed Pi. Three binary targets share one protocol contract: `pixie_assistant` (assistant host), `pixie_web` (Go controller and UI) and `pixie_cli` (assistant plus a bundled Pi for users without their own).

`assistant/` builds `pixie_assistant`: a Bun host that runs Pi sessions in-process through the operator's installed Pi SDK, resolved from that installation at runtime and never bundled. It replaces the interim Go host's `pi --mode rpc` child processes and the separate Bun administration bridge. There is no RPC child model and no bridge sidecar.

`web/` builds `pixie_web`: the Go controller and UI. It contains the `internal/controller`, `internal/canvas`, `internal/design`, `internal/mcpserver`, `internal/persist`, `internal/workspace`, `internal/security`, `internal/diagnostics` and `internal/identifier` packages, plus the `web/webui` Svelte interface. It runs as the Docker container or a local process.

`cli/` is a build flavor of `assistant/`, not a separate source tree. It packages the assistant with a bundled Pi.

`shared/` is the protocol-contracts module. `shared/schema/protocol-catalog.json` is the single source and generates the Go catalog under `shared/piprotocol` and the TypeScript catalog under `shared/src`. The package is `@pixie/shared`; the Go module is `github.com/miloszkolber/pixie/shared`. A root `go.work` links `assistant`, `shared` and `web`, each keeping its own `go.mod` and local `replace` so `GOWORK=off` still works.

Tests are organized per module: the assistant suite (currently colocated with the interim host and moving under the assistant tests directory as the Bun host lands), `web/tests` and `shared/tests`.

Deployment uses one host binary (`pixie_assistant`) plus the `pixie_web` controller container with Docker, or `pixie_assistant` and `pixie_web` as local processes without Docker.

## Current state

`web/` implements the Go controller and UI: HTTP/WebSocket serving, the six-slot workspace, projects and sessions, durable persistence with validate-first staging and typed outcomes, schedules and the durable run ledger, read-only files and Git inspection, the in-process MCP publisher for the workspace modules, and the browser MCP pointer. Browser MCP is a pointer model: Pixie stores one setting, writes only its own entry through Pi's `pi.mcp.servers.*` operations and reports a bounded probe. Pixie never hosts or proxies a browser.

`shared/` owns the wire contracts. The protocol catalog generates the Go and TypeScript catalogs, and the generated artifacts are checked for drift.

`assistant/` currently holds the interim Go `pixie_assistant`, which supervises the selected Pi executable over native RPC, and the opt-in administration bridge. The target Bun host owns all Pi interaction in-process: session lifecycle, transcript and dialog projection, provider and model administration, and the private loopback service the controller consumes. Pi continues to own execution, credentials, models, settings, tools, extensions and trust.

Tailwind is being removed from the UI. Do not add Tailwind dependencies or utilities to the frontend.

## Implementation streams

The remaining work runs as three parallel streams.

- **Assistant** — land the in-process Bun `pixie_assistant`, retire the Go RPC child model and the administration bridge, and prove the surviving behavior against a real Pi.
- **Canvas** — a session-scoped HTML draft module with a contained renderer. See [roadmap-canvas.md](roadmap-canvas.md).
- **Openfig** — an instance-wide read-only Figma `.fig` inspection module. See [roadmap-openfig.md](roadmap-openfig.md).

## Resolved with the host replacement

These defects existed only because Pi was supervised through a Go host, RPC child processes or a separate bridge. They are resolved or removed by the in-process Bun host design and are not carried forward.

- Wrong-session and wrong-cwd dispatch, acceptance-versus-settlement races and per-event attribution are resolved. One host process owns Pi sessions directly, so there is no child-to-logical-session registry or event multiplexing to reconcile.
- The administration bridge and its sidecar protocol are retired. Provider, settings, extension and MCP administration run in-process against the installed Pi SDK.
- Host-v2 envelopes, per-connection protocol negotiation and durable cross-process pairing helpers are removed as production concerns. A single in-process host does not pair two processes over a transport.
- The hand-maintained Go `nativeOperationSet` and its missing binding to controller call sites are removed with RPC capability negotiation. The shared catalog is the contract.

## Open defects and risks

These remain after the host replacement and need decisions or evidence.

1. **Real-Pi execution evidence.** The in-process host and the surviving web behavior need execution against real standalone and npm Pi distributions, including concurrent sessions, cancellation and reconnect. Mocks and compiler success do not establish compatibility.
2. **Deletion UX.** Native Pi has no delete command. `pixie_web` must present a confirm/retain reconciliation view for unmatched or uncertain deletion records and must never clear tombstones blindly.
3. **Full-host lifecycle.** arm64 install, start, stop and restart, plus upgrade and rollback under real systemd, are unproven. The amd64 fresh-archive path is the only exercised lifecycle.
4. **Provider and model contract alignment.** Provider catalog projection, model and thinking selection, and resource prompts need one typed contract shared by the host and the controller, plus recovery from a partially applied create.
5. **Diagnostics and recovery.** There is no operator view of negotiated capabilities, the current run, host health, pending uncertainty or actionable remediation, and no disposable `doctor --scenario` path.
6. **Persistence crash consistency.** Validate-first staging is covered for some mutations. Multi-file mutations, disk-full, permission, rename, fsync and stale-backup cases need a full audit.
7. **Observability.** Secret-safe logs and support bundles, boot and run identities, health transitions, child stderr retention and bounded diagnostic export are incomplete.
8. **Long-lived operations.** WebSocket reconnect storms, slow clients, schedule overlap, compaction during disconnect, state growth, artifact cleanup and clock changes need bounds.
9. **Browser-MCP threat model.** The operator-chosen endpoint may be unauthenticated. Loopback access, DNS rebinding, tool-result prompt injection, egress, sockets and crash cleanup belong to the deployment, but the threat model and fail-closed guidance need to be explicit.

## Next steps

Ordered by stream.

### Assistant

1. Replace the Go RPC host with the in-process Bun `pixie_assistant` and resolve Pi from the operator's installation at runtime.
2. Retire the administration bridge and `pi --mode rpc` child supervision with it.
3. Prove real-Pi execution for session lifecycle, settlement, cancellation, reconnect and concurrent sessions.
4. Align the controller to the shared catalog and one typed provider/model/resource contract.
5. Deliver the deletion reconciliation UX and the diagnostics and recovery surface.
6. Prove arm64 and upgrade/rollback lifecycle, then close persistence crash consistency, observability and long-lived-operation bounds.

### Canvas

1. Implement a contained `WorkerLauncher` and wire a production `CanvasConfig`.
2. Prove exact-version raster previews through the contained renderer.
3. Register native session authority so Canvas access is session-scoped, then run hostile and recovery tests.

### Openfig

1. Adopt an independently released, licensed parser and runtime behind the `Parser` interface and pin the design worker package.
2. Prove focus and draft identity, hostile/offline/recovery behavior, and the structure milestone.
3. Complete or explicitly record the real upstream frame-rendering blocker.

## Development phase

Tests should be simple, effective and useful: cover observable behavior and realistic failure modes without rebuilding an exhaustive evidence matrix. Keep the assistant, `web/tests` and `shared/tests` suites narrow and trustworthy.
