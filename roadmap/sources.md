# Sources and verification scope

Pixie baseline: `f63d0d5bcb7f6e342058734b2e741ad2619d8867`, reviewed on 8 September 2026. References below preserve the evidence behind the plans; they are not instructions to depend on a moving default branch.

The review inspected source and reported CI results. It did not independently run Pixie, reproduce the supplied empty-content screenshot, benchmark the Go rewrite, or execute the Openfig draft's parsing experiments. The latter measurements are attributed to the existing draft and must be reproduced before implementation relies on them.

The five supplied wireframes and current-UI screenshot are user-provided design inputs. Their behavior is transcribed into `workspace-ui.md` so an agent can implement the plan without recovering conversation attachments. Wireframe dimensions are approximate layout references, not immutable CSS values.

## Assistant and native Pi

| ID | Source | Supports |
| --- | --- | --- |
| A1 | [assistant/package.json](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/package.json) | Bundled SDK, Bun runtime and current distribution |
| A2 | [assistant/src/sessions.ts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/sessions.ts) | Direct SDK sessions, lifecycle, catalog, history and prompt settlement |
| A3 | [assistant/src/server.ts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/server.ts) | Dispatch, restart callback, ID coercion, attachment buffering and chunking |
| A4 | [assistant/src/main.ts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/main.ts) | Production startup and termination wiring |
| A5 | [runtime-restart.test.ts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/tests/pixie-assistant/runtime-restart.test.ts) | Injected restart callback coverage |
| A6 | [ui-bridge.ts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/extensions/ui-bridge.ts) | Dialog ownership and current composer limitations |
| A7 | [Pi 0.85.1 RPC reference](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/docs/rpc.md) | JSONL, prompt acceptance, agent_settled, queues, clone/fork, entries and native UI |
| A8 | [Pi package at inspected upstream revision](https://github.com/earendil-works/pi/blob/f53ac1135149f03fd1e2a5bfd29861120eaf5b96/packages/coding-agent/package.json) | Public package/CLI surface; not an arbitrary-version guarantee |
| A9 | [pi-web README](https://github.com/agegr/pi-web/blob/main/README.md) | User-specified reference for shared native configuration/sessions; moving reference, not a dependency pin |

## Controller

| ID | Source | Supports |
| --- | --- | --- |
| C1 | [pi_client.go](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/pi_client.go) | Host connection, loopback configuration and connection generations |
| C2 | [session_queues.go](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/session_queues.go) | Durable follow-ups and delivery uncertainty |
| C3 | [Pi integration](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/pi.md) | Current ownership and separate-process same-session write limitation |

## Workspace and Mewa

| ID | Source | Supports |
| --- | --- | --- |
| U1 | [shell.svelte](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/src/workspace/shell.svelte) | Global availability gating and modal Settings |
| U2 | [project-work-area.svelte](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/src/workspace/views/project-work-area.svelte) | Shared content slot, repeated create admission and Browser-specific integration |
| U3 | [session-state.ts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/src/workspace/store/session-state.ts) | Normal activation, runtime ownership and restoration complexity |
| U4 | [index.css](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/src/index.css) and [mewa.css](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/src/mewa.css) | Parallel foundation imports |
| U5 | [Pixie structural tokens](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/src/styles/tokens.css) | Independent spacing/radius definitions |
| U6 | [vendor lock](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/vendor/mewa.lock.json) | Mewa 0.1.2 revision and verified assets |
| U7 | [Button adapter](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/src/components/button.svelte) and [pinned Button CSS](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/webui/vendor/mewa-ui/css/components/button.css) | Existing correct wrapper worth retaining |
| U8 | [Mewa design contract](https://github.com/miloszkolber/mewa_ui/blob/master/library/DESIGN.md) | Library appearance/semantics versus product composition; verify the installed release before implementation |
| U9 | [Mewa Svelte integration](https://github.com/miloszkolber/mewa_ui/blob/master/library/adapters/svelte/README.md) | One lifecycle owner, scoped attachments and cleanup |

## Extensions and deployment

| ID | Source | Supports |
| --- | --- | --- |
| E1 | [MCP registry](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/mcpserver/registry.go) | Browser-only registry, persistence ordering, repeated startup, storage roots |
| E2 | [Compose](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docker-compose.yaml) | Actual mounts, UID, host networking and runtime flags |
| E3 | [Dockerfile](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/Dockerfile) | Browser/controller packaging and runtime contents |
| E4 | [Security documentation](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/security.md) | Documented shared-UID/no-sandbox posture, evidence and overclaims to reconcile |
| E5 | [MCP documentation](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/mcp.md) | Existing module publication model |

## Documentation and validation

| ID | Source | Supports |
| --- | --- | --- |
| D1 | [CI workflow](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/.github/workflows/ci.yml) | Workspace/native-host and architecture-specific checks |
| D2 | [Container workflow](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/.github/workflows/container-images.yml) | Validation/publication coupling and push path filters |
| D3 | [Assistant protocol](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/pi-protocol.md) | Method documentation requiring dispatcher reconciliation |
| D4 | [Browser acceptance](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/tests/ui/run.sh) and [reported CI run](https://github.com/miloszkolber/pixie/actions/runs/34226327666) | Existing continuity/keyboard/lifecycle tests and reported successful run |
| D5 | [Deployment](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/deployment.md) | Examples, host-specific details and mount contradictions |
| D6 | [Agent instructions](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/AGENTS.md) and [architecture](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/architecture.md) | Source/ownership inconsistencies |
| D7 | [Previous roadmap](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/roadmap.md) | Carry-forward items and publication boundaries |

## Canvas and Openfig drafts

- [Canvas draft](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/mcp-draft-canvas.md): six tools, session scope, revisions, quotas and proposed iframe rendering. Reviewed and superseded for implementation by `canvas.md`.
- [Openfig draft](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/docs/mcp-draft-openfig.md): instance-wide slot, parser findings, worker constraints, read-only tools and renderer gate. Reviewed and superseded for implementation by `openfig.md`.
- [openfig-core 0.4.1 package](https://github.com/OpenFig-org/openfig-core/blob/f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6/package.json), [parser](https://github.com/OpenFig-org/openfig-core/blob/f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6/src/parser.ts), and [types](https://github.com/OpenFig-org/openfig-core/blob/f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6/src/types.ts): public parsing, synchronous expansion/schema compilation, unsorted children, and returned data.
- [openfig-cli 0.6.0 package](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/package.json): exports and dependency tree. The inspected export map does not export its internal Design rasterizers.
- [Design SVG builder](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/lib/rasterizer/svg-builder.mjs), [WASM rasterizer](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/lib/rasterizer/deck-rasterizer.mjs), [export command](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/bin/commands/export.mjs), and [font resolver](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/lib/rasterizer/font-resolver.mjs): original draft's renderer investigation references, not permission to deep-import them.
- [Upstream fixture directory](https://github.com/OpenFig-org/openfig-cli/tree/0d74102f0cba4139ca14ba2e0f31664139744154/test/fixtures/figs/reference): provenance must be established before copying fixtures into permanent tests. Source-code licensing does not establish rights to every fixture or bundled font.

## External contracts

- [Pi RPC](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/docs/rpc.md): version-tested candidate baseline, not a promise of support for arbitrary Pi releases.
- [systemd.service](https://www.freedesktop.org/software/systemd/man/systemd.service.html), [systemd.kill](https://www.freedesktop.org/software/systemd/man/systemd.kill.html), and [systemd.exec](https://www.freedesktop.org/software/systemd/man/systemd.exec.html): service restart, readiness distinction, process termination and sandbox options. Check the target host's supported directives.
- [MCP 2025-11-25 transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports) and [2026-07-28 Streamable HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http): transport revisions differ. Test the revision supported by the locked SDK/native adapter. Neither an MCP transport session ID nor a caller-provided field establishes Pixie session authorization.
- [W3C CSP Level 3](https://www.w3.org/TR/CSP3/): browser content policy and sandbox directives are defense in depth, not an OS/network enclosure for arbitrary renderer processes.
- [W3C CSP Embedded Enforcement](https://www.w3.org/TR/csp-embedded-enforcement/): an additional embedding policy proposal, not a compatibility guarantee for every supported browser. Do not make an unverified browser feature the only Canvas egress control.

External reference pages may evolve. Pin packages and record actual tested protocol/runtime versions during implementation. Proposed limits and defaults in the plans are design budgets, not measured guarantees.
