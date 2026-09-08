# Reliability, security and validation

Preserve the existing recovery and access-control assertions while changing architecture. Evidence must distinguish static inspection, fake transports, independent Pi execution, UI behavior and final deployment artifacts. Track FIX, SEC, PERF, PKG, CAN and FIG tasks in [execution.md](execution.md).

## 1. Regression priorities

| Boundary | Required regression |
| --- | --- |
| Production restart | Actual executable exits through bounded shutdown; systemd restarts it; repeated request is idempotent. Full-host restart drains the whole composition. |
| Module enablement | Failed persistence changes neither effective prior state nor active service; duplicate enable preserves the handle; another module's state survives. |
| Session create/selection | Repeated activation dispatches one pending create; selected resource resolves to its own loading/content/error state; stale restoration cannot win. |
| Protocol | Strict IDs/envelopes without coercion; malformed/oversized/fragmented frames, slow readers, duplicate in-flight IDs and unsupported versions have explicit outcomes. |
| Native execution | Acceptance differs from settlement; retries/compaction/continuations/commands/Stop do not duplicate accepted prompts. |
| Recovery | Mid-message/tool/dialog reconnect is scoped by session and process epoch; dead callbacks and deleted resources cannot reappear. |
| Data | Failed writes, uncertain delivery, corrupted state, tombstones and migration/rollback preserve declared commit semantics. |
| Files/Git | Root/realpath identity, symlinks, traversal, limits, multi-repository context and safe output remain read-only. |

The screenshot's empty-content state remains an unverified reproduction until a test establishes its cause. Repeated Chat labels are not themselves proof of duplicate execution. Recheck the pinned review against the actual checkout before fixing already-changed code.

## 2. Authority and threats

Pi intentionally executes with the host user's authority. Do not turn this roadmap into a generic tool-permission system or sandbox the user's normal Pi workflow accidentally. Project trust and provider/tool policy remain native.

Browser pages, Canvas HTML and uploaded .fig files are different: their content is untrusted and must not gain service credentials, controller storage or host authority merely because Pi requested a tool. Renderers/parsers require separate reviewed boundaries.

The reviewed merged Browser deployment uses a shared UID, host networking and a sandbox-disabled Chromium configuration. Scoped HOME/TMP/environment, non-root execution and read-only project mounts do not isolate it from controller files or local services. No exploit was demonstrated; do not describe those facts as proof of compromise. [Deployment evidence](sources.md#extensions-and-deployment).

Full-host mode increases the consequences of an unrestricted same-user worker: the surrounding process runs directly under the Pi owner's account, not inside the application container. Do not copy the merged Browser posture into a direct-host renderer and claim containment. Separate library/process ownership alone is not filesystem or network isolation.

Keep distinct human UI, assistant, MCP module and session-scoped credentials. Scope resource access server-side and redact secrets from logs, snapshots, URLs and model text. Never treat opaque IDs, tool-supplied project/session fields, or MCP transport session identifiers as authority.

## 3. Deployment hardening

For Docker, reconcile actual Compose flags with claims. Test non-root/read-only roots, bounded tmpfs, capability drops, no-new-privileges and practical memory/PID/CPU limits before documenting them. Do not claim image metadata or a successful screenshot enforces runtime isolation.

For direct host, retain loopback by default and require authenticated, explicitly configured remote UI access with TLS at the trusted edge. The internal full-host assistant listener is not exposed through public routes; never leak its private credential. Use the same service lock to detect assistant-only/full-host conflicts.

Validate exact Host/Origin handling, CSRF/mutation checks, unauthenticated endpoints, credential separation, path admission and response content types in both deployments. A loopback port is still reachable by host processes; bearer/origin boundaries matter.

Retain Browser artifact MIME/path/size checks, no-follow opens, lease renewal, cleanup and cancellation. A stronger separate worker deployment requires routing, identity, quotas, artifact mediation, restart and cleanup tests, not merely a new URL option.

Canvas's egress-denied promise and Design's hostile-file worker bounds are mandatory module gates. Use a tested optional worker enclosure/launcher without broad filesystem mounts or privileged daemon sockets. Deny external network for those workers, remove secrets, bound memory/CPU/time/output and test canary files and network destinations. Missing enforcement makes the module unavailable; core chat stays usable.

Chromium sandboxing and CSP are additional controls, not substitutes for that boundary. Account for iframe versus top-level navigation and the user's own browser, as specified in [canvas.md](canvas.md). A Node heap setting does not limit a worker's total memory, as specified in [openfig.md](openfig.md).

## 4. Test layers

### Fast contract/unit tests

Use reducer invariants, native JSONL fixtures, strict schema cases, state commit/failure injection, job identity and queue ownership. Prefer observable behavior over copied constants or trivial forwarding tests. Run Go race tests for actual concurrent boundaries.

### Independent Pi integration

Install Pi independently of the Pixie workspace in disposable locations. Test vanilla and customized configurations, optional extensions absent/present, custom HOME/agentDir/PATH, supported releases and explicit unsupported behavior. Hash native source files before/after read-only discovery. Never run against live user credentials/state by default.

Exercise images, native resources/trust, fork/clone, model/thinking, retries/compaction, native UI, pending queues, child death and idle TUI handoff. Simultaneous TUI/Web tests use separate sessions unless a tested shared-process mechanism exists.

### UI acceptance

Use production assets with real content in all five reference layout modes, light/dark, narrow screens, 200% zoom, reduced motion and keyboard-only operation. Check labels/focus, independently scrollable panes, composer visibility, long filenames, tree navigation and missing/error/loading states.

Keep streaming after Hide/Close, draft safety, native dialogs, reconnect deduplication, back/forward and stale-generation tests. Repeated mounting must not leak Mewa controllers, listeners, pollers or Browser leases. Capture screenshots for layout review; visual snapshots alone do not establish state correctness.

### Final artifact/deployment acceptance

Both required builds on amd64/arm64 are mandatory. Assistant-only tests use the real Docker controller; full-host tests use the single binary with embedded assets, no separate assistant process/toolchain and no Docker dependency for core functionality. Test explicit controller-only entrypoint behavior as well.

Run actual user units in an appropriate isolated systemd environment. Verify startup/readiness/diagnostics, stop, requested restart, hung descendants, incorrect configuration, ownership conflict, upgrade, rollback, mode switching and uninstall. A callback unit test is not an executable restart test.

Optional module tests run real native MCP calls, including actual image content. Validate supported protocol revisions through the locked SDK/adapter, not a latest-spec claim inferred from a transport name. [References](sources.md#external-contracts).

## 5. Performance measurements

Measure before and after under the same fixture/environment. Record commit, artifact digest/variant, architecture, OS/kernel, CPU/RAM, runtime/Pi versions, optional extensions/workers and methodology. Separate cold/warm runs and use repeated samples with p50/p95; one timing is not a capacity result.

Measure controller and assistant plus Pi children: idle/active RSS, catalog size/startup, session creation/reopen, large image/history attach, concurrent sessions, cancellation latency and service shutdown. For full-host mode do not compare only its main PID against a split deployment's complete process tree.

For UI, measure first usable render, streaming responsiveness, history pagination/scroll anchoring, split/focus transitions and browser memory with representative content. For Browser use bounded open/snapshot/close plus repeated sessions and cleanup. For Canvas/Design add worker startup, render/parse, stored-index queries, cache hits, artifact sizes and memory limits.

Use measurements to set regression budgets and residence limits. Do not copy old single-run timings or treat successful cross-compilation as arm64 measurement. Distribution/ownership can justify Go without an unproven performance claim.

## 6. Cutover and publication

Preserve current tests until the accepted replacement behavior has coverage. Review dormant methods/hooks before deleting them; wire retained functionality rather than removing it as cleanup. Remove old runtime/UI/package paths only after shared contracts, independent slices, continuity, optional integrations and both release variants pass.

Back up application metadata and private configuration after work settles. Native Pi state is not a migration target. Preserve uncertain-dispatch ledger entries; do not replay them merely because a service restarted or mode changed. Document schema rollback compatibility and exercise it with real artifacts.

Prepare releases locally/in CI without automatically publishing. The [build/release contract](builds-and-releases.md) requires the complete four-archive set and exact-source verification before final release promotion. Publication, upstream contributions and live deployment changes retain explicit approval requirements.

## Required evidence

Record task IDs, commit, commands/tests actually executed and results, UI/process/artifact evidence, changed contracts, data migration/rollback impact and remaining limitations in the execution ledger. Mark optional upstream renderer blockers separately; never claim an unexecuted or mocked test proves native compatibility, isolation or visual fidelity.
