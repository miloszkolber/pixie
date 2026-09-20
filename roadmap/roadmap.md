# Pixie roadmap

This is the canonical implementation plan and forward backlog. Operating behavior belongs in `docs/`. The project is experimental: tests should be simple, effective, and useful, and the frozen release-evidence matrix under `scripts` does not gain new rows.

## Target architecture

Pixie builds two public Linux products from one repository.

| Product | Role |
| --- | --- |
| `pixie_web` | Go controller and web workspace only. It connects to a separately managed loopback `pixie` host and never starts Pi. |
| `pixie` | Bundled Bun, normal Pi TUI, Pi SDK, and assistant host connector. Bare `pixie` runs the native Pi TUI; `pixie serve --config ABS` starts its host. The TUI runs concurrently with the server against the same agent directory. |

`pixie_assistant` is internal to the host archive and is never a public product, unit, command, or archive. It is a bundled JS host at `libexec/pixie_assistant.js`, run by the bundled `runtime/bin/bun`. The host product exposes `pixie` as the native Pi command and bundles pinned Bun `1.4.0` and Pi `0.85.1`; Node is never bundled, Pi RPC is excluded, and Pi self-update is blocked. Bun `1.3.14` cannot run Pi's bundle and must never be selected.

`pixie serve` is the only Pi owner that takes the agent-directory lock. The TUI never takes it, so no idle handoff is needed between the TUI and the managed owner. Two servers still collide with exit `73`; stop the managed owner or use a separate agent directory.

Docker publishes only `pixie_web`, which connects to a separately installed host. The combined container was removed. Docker configuration is not proof of an approved deployment, a sandbox, or a published image.

Reducing the host to a Pi extension is rejected. An extension lives only inside a Pi process, cannot own a durable multi-session registry, provider/model/settings or MCP mutation, or deletion authority, and would expose the controller secret to every loaded extension. A dedicated host process remains required; an in-TUI extension could only ever be an optional additive surface that never owns sessions or the loopback endpoint.

## Source and ownership

`src/assistant/` owns direct public-Pi SDK interaction and the archive-internal host. `cmd/` and `internal/` own the Go controller, persistence, workspace modules, MCP publisher and browser-MCP pointer; `webui/` owns the frontend. `schema/`, `piprotocol/` and `src/shared/` own the protocol schema and generated Go and TypeScript catalogs. `scripts/` owns repository build and gate tooling. The narrow authenticated loopback host protocol is not a Pi execution fallback.

Pi owns execution, transcripts, native credentials, models, settings, tools, extensions, and trust. Pixie must not intercept tools, replace prompts, silently install packages, auto-trust projects, or introduce another model or MCP policy. The bundled native TUI is first-class and does not establish untested web parity.

## Current source state

The controller implements web workspace state, sessions, read-only files and Git inspection, goals, schedules, browser-MCP registration, and optional module registration. The internal host implements direct Pi session lifecycle and selected public SDK projections. The controller gates host calls through the negotiated `runtime.hello` operation set; the catalog is a candidate-operation schema, not proof of universal runtime support.

The SDK coverage inventory lives in [docs/sdk-coverage.md](../docs/sdk-coverage.md). It separates proven public Pi SDK behavior from Pixie policy, records what is web-covered, native-TUI-only, partial, or unavailable, and is the reference for the coverage work below.

Additive, optional work taken from neighbouring open-source Pi interfaces is planned in [roadmap-aux.md](roadmap-aux.md). Those items are subordinate to this plan and never change the product target or trust model.

Web UI composition and appearance work is planned in [roadmap-ui.md](roadmap-ui.md). It aligns the frontend with the Mewa foundation and the approved reference set; it does not change the product target or trust model.

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

Implemented at source level; the remaining evidence needs a live host, credentials, or filesystem fault injection:

1. **Diagnostics and recovery.** Authenticated runtime diagnostics, bounded recovery reports, and isolated host-boot proofs. Evidence against a live host and real credentials remains external.
2. **Persistence crash consistency.** Validated staged migrations, authority-ledger preservation, and durability-uncertain classification. Real filesystem and power-loss evidence remains external.
3. **Observability.** Secret-redacted logs, boot/run identities, bounded health history, and redacted child-stderr rings. Cross-process stderr remains surfaced through supervisor logs by design.
4. **Long-lived operations.** Connection caps, backoff gates, bounded slow-client shedding, capped retained state, monotonic schedule clocks, and shutdown scratch cleanup. New schedules carry a persisted 24-hour default budget.
5. **Browser-MCP threat model.** Documented in `docs/security.md`; loopback access, DNS rebinding, tool-result injection, egress, sockets and crash cleanup remain deployment responsibilities.

## Next steps

1. Exercise the host archive against real Pi, credentials, and lifecycle transitions on both architectures, including concurrent native TUI and server operation plus extension loading, before treating a published release as runtime evidence.
2. Close the remaining upstream-dependent SDK coverage priorities; the locally actionable ones are implemented.
3. Gather live evidence for diagnostics, persistence and observability; the source-level lifecycle and resilience work is complete.
4. Obtain separate authorization before any live deployment.
