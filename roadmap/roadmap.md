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

Additive, optional work taken from neighbouring open-source Pi interfaces is planned in [roadmap-aux.md](roadmap-aux.md). Those items are subordinate to this plan and never change the product target or trust model.

## Open evidence and decisions

1. The remote release pipeline has published native archives and a controller image. Real runtime evidence is still missing for the native TUI and extension PTYs, credentialed Pi, standalone Pi use, arm64 lifecycle, live systemd and Docker lifecycle, updates and rollback.
2. `runtime.capabilities`, controller `pi.capabilities`, tools, steering, and provider configuration are not universal public Pi capabilities. The controller keeps negotiating the generated catalog and failing closed rather than treating catalog names as availability.
3. Public Pi lacks the run identifier needed to bind steering safely, and the resource-attachment API remains unavailable; both stay explicitly unavailable rather than emulated.

## SDK integration coverage

The primary forward work is closing the gap between the public Pi SDK surface and what Pixie exposes. Priorities:

1. Obtain a public Pi run identifier and expose a safe steering contract. Upstream API missing; steering is explicitly unavailable and fails closed.
2. Add a public resource-attachment API. Upstream API missing; resource prompts remain unavailable rather than rewritten.
3. Obtain an authoritative per-session tool inventory. Upstream API missing; the host does not advertise the tools capability.
4. Define a typed, secret-safe provider and settings configuration contract that is discoverable before mutation. Implemented as `pi.providers.config.read` plus per-key preference writability and source.
5. Keep one negotiated capability source and extend it with tested operation-set and public-SDK evidence for every new route. Implemented: the generated catalog is the single source and every available route has host dispatch tests.
6. Keep the SDK coverage inventory current as Pi releases change, and record proven, partial, and unavailable boundaries without over-claiming. Ongoing.

## Lifecycle and reliability

These are implemented at source level. The remaining evidence needs a live host, credentials, or filesystem fault injection.

1. **Diagnostics and recovery.** Authenticated runtime diagnostics project negotiated capabilities, host health, active-run count, retained deletion uncertainty, schedule health and remediation. `runtime.supportSnapshot` is auth-gated and never collects assistant or system logs. `pixie_web doctor` and internal `pixie_full doctor` print bounded recovery reports with stable codes, and `pixie_assistant doctor --scenario` proves an isolated host boot. Evidence against a live host and real credentials remains external.
2. **Persistence crash consistency.** Staged migrations validate every managed flat ledger input before writes, preserve authority ledgers during rollback, reject retained backups, and classify partial publication as durability-uncertain. Injected disk-full, permission, rename, fsync and staging-crash outcomes are covered; real filesystem and power-loss evidence remains external.
3. **Observability.** Controller and supervisor logs redact secrets, URLs and paths at the emission boundary; boot and run identities, a bounded health-transition history, and a bounded redacted child-stderr ring are implemented. The controller support snapshot is auth-gated and excludes another process's stderr by design; the supervisor surfaces that tail through its logs and `doctor`.
4. **Long-lived operations.** Connections are capped with a reconnect attempt and backoff gate, slow clients are bounded and shed, per-socket replay reservations are released on disconnect, retained state is capped, schedule deadlines use a monotonic clock, and controller scratch is cleaned on shutdown. New schedules have a persisted configurable 24-hour default budget; existing schedules remain unlimited and cancellation uncertainty blocks redispatch.
5. **Browser-MCP threat model.** The operator-chosen endpoint may be unauthenticated and Pixie never proxies MCP traffic. The threat model and fail-closed enablement guidance are documented in `docs/security.md`; loopback access, DNS rebinding, tool-result injection, egress, sockets and crash cleanup remain deployment responsibilities.

## Next steps

1. Exercise the Pi-bearing archives against real Pi, credentials, and lifecycle transitions on both architectures, including the native TUI and extension loading, before treating a published release as runtime evidence.
2. Close the remaining upstream-dependent SDK coverage priorities; the locally actionable ones are implemented.
3. Gather live evidence for diagnostics, persistence and observability; the source-level lifecycle and resilience work is complete.
4. Obtain separate authorization before any live deployment.
