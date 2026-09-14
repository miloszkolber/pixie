# Canvas roadmap

Canvas is a session-scoped HTML drafting module: an agent stages an HTML draft, a person sees version-aware previews in Pixie, and the agent iterates with bounded DOM/text and screenshot feedback. One Canvas belongs to one authenticated native chat session. There is no shared gallery, online assets, editing framework or independent agent orchestration.

This plan is self-contained. The module is disabled by default and optional: missing dependencies or containment degrade Canvas only, and basic chat never depends on it. The stream is tracked in [roadmap.md](roadmap.md).

## Current state

The service lives in `web/internal/canvas` (service, storage, revisions, jobs, HTTP and MCP) with its contribution in `web/webui/src/canvas`. Tests sit in `web/tests/go/canvas`, `web/tests/go/controller/session_canvas_test.go` and `web/tests/webui/canvas`.

Implemented at source level:

- Session-scoped authority. `Registry.AttachCanvas` and `AttachCanvasManagement` mint opaque capabilities bound to a native session and generation; Canvas IDs are references, not credentials. Management keeps retained documents inspectable and removable while the renderer is unavailable.
- Immutable revisions. `canvas_write` publishes a complete UTF-8 HTML revision behind a version compare-and-swap and a mutation ID. Retries return the committed result; a repeated mutation ID with different content is a conflict. Patches and merges are not supported.
- Transactional storage under the controller data directory, with staging, atomic rename, tombstone-first removal, restart reconciliation and a global quota. Persistence-uncertain outcomes are typed and reconciled rather than treated as a rollback.
- The six tools (`canvas_create`, `canvas_write`, `canvas_read`, `canvas_screenshot`, `canvas_list`, `canvas_remove`) plus `pixie://canvas/guide` / `canvas_guidance`.
- Bounded limits: 512 KiB HTML per write, 64 MiB durable Canvas storage, 2 MiB encoded PNG before MCP base64, 1280x800 at DPR 1 by default, at most 2048 pixels per dimension and 4,194,304 pixels, 512 selector bytes, 4,096 matches and 64 KiB returned text or DOM. Jobs are single-flight keyed by generation, revision, viewport, renderer version and asset set, bounded to one active plus two queued.
- A UI contribution with revision/status controls, a viewer, a sidebar, tool cards and recovery states.

## Blockers

Canvas cannot reach Ready in production until these are resolved.

1. **Contained `WorkerLauncher`.** `WorkerLauncher` is an empty interface. The only built-in launcher, `NewDeterministicWorkerLauncher`, is a metadata-only fixture renderer and does not execute the draft or claim browser fidelity. There is no launcher with enforced egress denial, filesystem and memory limits, and process-tree teardown. Without one, screenshots return `unavailable` and Canvas never silently falls back to an uncontained controller or browser process.
2. **Production `CanvasConfig` wiring.** `web/internal/mcpserver/module_runtime.go` uses `config.CanvasConfig` only when a caller supplies one, and no production caller does. The default config has no launcher, so the readiness gate reports "The Canvas contained worker is not configured." Canvas therefore cannot reach Ready through `pixie_web`.
3. **Exact-version raster previews.** Previews must be real raster output for the exact requested revision. The fixture launcher produces deterministic placeholder PNGs, so no path proves that a requested revision renders through the contained worker and is served as the authenticated artifact for that version.
4. **Registered native authority.** The registry can attach a session capability, but no production native-MCP binding registers the authenticated session principal and generation before tools are exposed. A static module bearer plus a model-supplied session id is not sufficient.

## Phased plan

### Phase 1 — containment and authority (CAN-01)

- Implement a contained `WorkerLauncher` behind the existing interface: one-shot input is the immutable revision only, output is a validated bounded PNG. Enforce egress-denied networking, no controller credentials, no project mounts, no application databases and no daemon socket; use an operator-configured isolated worker or a tested sandbox launcher.
- Add process-tree cancellation, a hard wall deadline and enforced memory/resource bounds; validate MIME, dimensions, bytes and output path without following symlinks.
- Reserve global quota and serialize commits before staging.
- Wire a production `CanvasConfig` in `pixie_web` that selects the contained launcher, and make readiness/diagnostics explicit when no launcher is configured.
- Register the authenticated native session principal and generation through the existing registry so agent calls are session-scoped without a model-supplied token.

### Phase 2 — revisions and durability (CAN-02)

- Confirm atomic revision publication, retry/mutation identity, tombstone-first removal and restart recovery against the new launcher.
- Retain tombstones until stale jobs and retries cannot re-authorize an old generation.
- Keep logical removal durable before physical cleanup, and never recover a pre-deletion backup over a tombstone.

### Phase 3 — rendering and tools (CAN-03)

- Serve real exact-version raster previews through the contained renderer and authenticated artifact delivery with no tokens in URLs.
- Label a prior preview stale while a newer render is pending, and never return a cached placeholder as current.
- Keep the guide accurate for the deployed launcher and offline limits.

### Phase 4 — UI (CAN-04)

- Surface the rendering state, warnings, current and viewed version, and update time in the existing right-rail/slot composition.
- Provide explicit refresh and removal where authorized. A session change changes scope, and stale events cannot restore the previous session's document.
- Keep tool cards as bounded operation/version/result summaries, not executable HTML replay.

### Phase 5 — validation (CAN-05)

- Prove create, two writes, a rejected stale write, exact-version read and screenshot, visible update, iteration and removal.
- Prove cross-session and revoked credentials fail, and that remove/disable during rendering denies access immediately and cannot be undone by restart or a late completion.
- Cover flooded and oversized writes, quota concurrency, disk failure, malformed selectors, hung scripts, huge pixel requests, child death, forbidden network and filesystem access, image limits, expired credentials, reconnect and disabled-management cleanup.
- Validate both the Docker and local-process deployments on amd64 and arm64.

## Acceptance criteria

Canvas is complete when an authorized session can create one document, publish and read immutable revisions, reject a stale write, receive a real bounded screenshot of the exact requested revision, iterate, and remove the document with immediate access revocation.

Authority and containment hold: a Canvas ID never acts as a credential, cross-session and revoked capabilities fail, the renderer cannot read a canary file outside its input/output or reach the network, and a missing or unavailable launcher reports `unavailable` instead of executing draft HTML in the controller. Stored documents survive restart, a repeated mutation ID never duplicates a write, and a late or stale job cannot recreate a tombstoned document or make an old preview look current. Basic chat stays usable with Canvas absent.
