# Pixie roadmap

This is the canonical implementation plan and forward backlog. Operating behavior belongs in `docs/`. The project is experimental: tests should be simple, effective, and useful, and the frozen release-evidence matrix under `web/scripts` does not gain new rows.

## Target architecture

Pixie builds three public Linux products from one repository.

| Product | Role |
| --- | --- |
| `pixie_web` | Go controller and web workspace only. It connects to a separately managed loopback `pixie_cli` host and never starts Pi. |
| `pixie_cli` | Bundled Bun, normal Pi TUI, Pi SDK, and assistant host. `pixie_cli serve --config ABS` starts its host. |
| `pixie` | Bundled Bun, normal Pi TUI, Pi SDK, assistant host, and controller. Its full-suite service is internal `libexec/pixie_full serve --assistant-config ABS --web-config ABS`. |

`pixie_assistant` is internal to the Pi-bearing archives and is never a public product, unit, command, or archive. It is a bundled JS host at `libexec/pixie_assistant.js`, run by the bundled `runtime/bin/bun`. Both Pi-bearing products expose `pixie` as the native Pi command and bundle pinned Bun `1.4.0` and Pi `0.85.1`; Node is never bundled, Pi RPC is excluded, and Pi self-update is blocked. Bun `1.3.14` cannot run Pi's bundle and must never be selected.

The split `pixie_cli` + `pixie_web` topology and full `pixie` topology are alternatives. They cannot share the global `pixie` command or `PI_CODING_AGENT_DIR`. A supported owner collision exits `73`; use an explicit idle handoff with the managed owner stopped, or separate agent directories. Pixie does not attach to an active TUI.

Docker mirrors the two alternatives: controller-only `pixie_web` connects to a separately installed host, while full `pixie` includes the bundled Bun runtime, host, and controller and persists Pi state in its dedicated volume. Docker configuration is not proof of an approved deployment, a sandbox, or a published image.

Reducing the host to a Pi extension is rejected. An extension lives only inside a Pi process, cannot own a durable multi-session registry, provider/model/settings or MCP mutation, or deletion authority, and would expose the controller secret to every loaded extension. A dedicated owner-locked host process remains required; an in-TUI extension could only ever be an optional additive surface that never owns sessions or the loopback endpoint.

## Source and ownership

`assistant/` owns direct public-Pi SDK interaction and the archive-internal host. `web/` owns the controller, UI, persistence, workspace modules, MCP publisher, and browser-MCP pointer. `shared/` owns `shared/schema/protocol-catalog.json` and generated Go and TypeScript catalogs. The narrow authenticated loopback host protocol is not a Pi execution fallback.

Pi owns execution, transcripts, native credentials, models, settings, tools, extensions, and trust. Pixie must not intercept tools, replace prompts, silently install packages, auto-trust projects, or introduce another model or MCP policy. The bundled native TUI is first-class and does not establish untested web parity.

## Current source state

The controller implements web workspace state, sessions, read-only files and Git inspection, goals, schedules, browser-MCP registration, and optional module registration. The internal host implements direct Pi session lifecycle and selected public SDK projections. The controller gates host calls through the negotiated `runtime.hello` operation set; the catalog is a candidate-operation schema, not proof of universal runtime support.

The SDK coverage inventory lives in [docs/sdk-coverage.md](../docs/sdk-coverage.md). It separates proven public Pi SDK behavior from Pixie policy, records what is web-covered, native-TUI-only, partial, or unavailable, and is the reference for the coverage work below.

## Open evidence and decisions

1. Real archive runtime evidence is missing for the native TUI and extension PTYs, credentialed Pi, standalone Pi use, arm64, live systemd and Docker, updates and rollback, and remote publication.
2. `runtime.capabilities`, controller `pi.capabilities`, tools, steering, and provider configuration are not universal public Pi capabilities. The controller must keep negotiating and failing closed rather than treating catalog names as availability.
3. Public Pi lacks the run identifier needed to bind steering safely, and the resource-attachment API remains unavailable.

## SDK integration coverage

The primary forward work is closing the gap between the public Pi SDK surface and what Pixie exposes. Priorities:

1. Obtain a public Pi run identifier and expose a safe steering contract, or keep steering explicitly unavailable.
2. Add a public resource-attachment API, or keep resource prompts unavailable rather than rewriting prompts.
3. Obtain an authoritative per-session tool inventory instead of projecting an unavailable host route.
4. Define a typed, secret-safe provider and settings configuration contract that is discoverable before mutation.
5. Keep one negotiated capability source and extend it with tested operation-set and public-SDK evidence for every new route.
6. Keep the SDK coverage inventory current as Pi releases change, and record proven, partial, and unavailable boundaries without over-claiming.

## Lifecycle and reliability

These remain after the host replacement and need decisions or evidence.

1. **Diagnostics and recovery.** Authenticated runtime diagnostics project negotiated capabilities, host health, active-run count, retained deletion uncertainty, schedule health and remediation. `runtime.supportSnapshot` exports a bounded redacted controller snapshot only when `PIXIE_AUTH_ENABLED=true`; it never collects assistant or system logs. Real operator recovery and scenario coverage remain incomplete.
2. **Persistence crash consistency.** Staged migrations validate every managed flat ledger input before writes, preserve authority ledgers during rollback, reject retained backups, and classify partial publication as durability-uncertain. Full multi-file power-loss atomicity and real disk-full, permission, rename, and fsync behavior still need filesystem-specific evidence.
3. **Observability.** The support snapshot supplies bounded controller facts and allowlisted event codes without raw errors. Secret-safe logs across processes, boot and run identities, health-transition history, and child stderr retention remain incomplete.
4. **Long-lived operations.** New schedules have a persisted configurable 24-hour default budget, while existing schedules remain unlimited and cancellation uncertainty blocks redispatch. WebSocket reconnect storms, slow clients, compaction during disconnect, state growth, artifact cleanup, and clock changes still need bounds.
5. **Browser-MCP threat model.** The operator-chosen endpoint may be unauthenticated. Pixie never proxies MCP traffic. Loopback access, DNS rebinding, tool-result injection, egress, sockets, and crash cleanup belong to the deployment; the threat model and fail-closed guidance need to be explicit.

## Next steps

1. Exercise the Pi-bearing archives against real Pi, credentials, and lifecycle transitions on both architectures, including the native TUI and extension loading, before treating the source layout as release evidence.
2. Close the SDK integration coverage priorities above, keeping the coverage inventory and negotiation exhaustive.
3. Complete the diagnostics, persistence, observability, long-lived-operation, and Browser-MCP threat-model items.
4. Obtain separate authorization before any remote publication or live deployment.
