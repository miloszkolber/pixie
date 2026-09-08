# Openfig Design inspection

Last feature integration, after Canvas Gate 6. Read [contracts.md](contracts.md), the [consolidated draft review](repository-review.md#canvas-and-openfig-draft-review), [extensions.md](extensions.md) and [security-and-validation.md](security-and-validation.md). Track FIG-01 through FIG-06 in [execution.md](execution.md). Shared contracts govern bounds, index-artifact handling and publication/durability outcomes.

## 1. Outcome and boundaries

A user uploads a local Figma Design `.fig`; users and authorized agents inspect the same persistent document without Figma, an online renderer, or a browser tab remaining open. OpenFig owns decoding and any eventual scene rendering. Pixie owns storage, bounded normalized queries, access control, shared focus, UI and MCP publication.

Registry ID `design`, connection `pixie-design`, endpoint `/mcp/design`, supported Streamable HTTP binding. Default disabled. Reuse the shared registry rather than implementing another two-module generalization.

Keep one active document across the single-user Pixie instance. This is explicitly instance-wide, not project-private or session-private. Explain that scope before upload and in module settings: every authorized Design reader can inspect it. Do not infer privacy from a caller-provided project ID or from the chat that opened the panel.

Retain the original until explicit user removal, including across restarts. A second active or pending upload returns a conflict; no silent replacement or automatic source expiry. Upload, removal and shared-focus mutations belong to the human application API, not model tools. Agent operations are read-only.

Storage is application data, not native transcripts, Signet, embeddings or a vector database. Do not inject full design content into model context automatically. A saved document thumbnail is useful but is not a rendering of an arbitrary selected frame.

## 2. Dependency and renderer decisions

Use pinned public `openfig-core` for parsing/indexing. Core 0.4.1 is the inspected candidate; reproduce it in a clean install with owned/licensed real .fig fixtures and a lockfile. Recheck released artifacts rather than adopting repository HEAD blindly. [Pinned evidence](sources.md#canvas-and-openfig-drafts).

Do not require `openfig-cli`, proxy its upstream MCP service, deep-import private rasterizer paths, copy its scene engine into Pixie, or implement a Figma renderer from scratch. The inspected CLI's package exports do not expose its Design rasterizers. A small PNG rasterizer wrapper does not supply missing scene construction.

The original draft reports parse/thumbnail smoke tests and failed private-renderer experiments. They are retained with explicit attribution in [the consolidated review](repository-review.md#prior-openfig-experiments), not treated as freshly executed validation or capacity/fidelity guarantees.

Frame previews remain a separate desired milestone. Recheck for a released public upstream Design-frame API at FIG-06. Structure inspection must not depend on that milestone or pretend that repeating a cover thumbnail is rendering.

## 3. Source and process ownership

```text
Authenticated user HTTP upload/management     Authorized Design MCP reads
                    |                               |
                    +---- Pixie Design service -----+
                          persistent document slot
                          normalized query index
                          explicit shared focus
                                  |
                          bounded one-shot worker
                          public openfig-core API
                                  |
                          optional upstream renderer
```

Use `package/internal/design/` for slot lifecycle, queries, API/MCP, artifacts and worker supervision; `package/design-worker/` for a minimal worker package; `package/webui/src/design/` for the contribution. Integrate tests into existing areas and colocated Go tests. No repository-wide moves or changes to assistant architecture.

The worker is application-side. It is optional in both Docker and full-host deployments and is not part of the assistant-only runtime. A small pinned Node ESM worker is the initial reference implementation. A compiled Bun helper may replace its runtime packaging only after schema compilation, assets and isolation pass identical tests. Reuse the repository's dependency authority, not an unrelated drifting lockfile.

The full-host core binary must start without the worker or Node. Adding optional Design dependencies does not replace either required release build. Describe actual costs and install/enclosure requirements in the [two-variant release matrix](builds-and-releases.md).

Run parsing outside the Go controller in a one-shot operation. Subsequent queries use the stored normalized index, not reparsing or a persistent JS service. Do not call Browser to parse .fig or depend on an open Web UI. Introduce a warm worker only after measurements justify it.

## 4. Persistent slot and mutations

Suggested storage below `<PixieDataDir>/mcp-design/`:

```text
slot.json
documents/<random-document-id>/source.fig
documents/<random-document-id>/index.json
documents/<random-document-id>/thumbnail.png
documents/<random-document-id>/previews/<opaque-cache-key>.png
staging/<upload-id>/...
```

Use generated IDs, never uploaded names or raw Figma node IDs as paths. `slot.json` records immutable document ID/hash, display name, upload time/size, parser/index versions, generation, availability and shared selection. Selection revision is separate from source identity. Index/cache revisions cannot change the source silently.

Stream a bounded upload/checksum to staging; reserve the single slot while pending. Authenticate and check mutation origin before expensive work. Validate input, parse/index in the enclosed worker, and validate worker outputs in Go. Move complete immutable outputs into place, then durably publish the slot pointer last. Emit availability only after commit. The index uses its dedicated bounded artifact path, not generic persist.Write or a full-index host/browser frame. Post-publication errors follow the persistence-uncertainty contract, not an assumption that every failed call left the old pointer installed.

Competing uploads cannot both commit. A retry identity/fingerprint or upload operation ID returns the same committed outcome rather than allocating a second document. Failure leaves committed state unchanged only when non-publication is known; otherwise reconcile the validated primary before dependent actions. Clean staging with bounded diagnostics. A client disconnect has an explicit cancel/operation-status policy, not an ambiguous background replacement.

Removal requires current document identity/revision. Durably tombstone its generation, revoke access, cancel matching jobs and then delete files. Physical cleanup can retry, but logical reads fail immediately. No stale job, backup recovery or upload completion can republish the removed source. Removal must work while the module is disabled or its worker is unavailable, without invoking the parser.

Restart restores the committed slot, cleans interrupted staging, validates derived-state references and reports missing/corrupt state. Reindex the retained source when appropriate under the same bounds; never silently delete it or call corruption an empty success. Reindex validates persisted shared-focus node references and invalidates incompatible caches without letting an old job overwrite new state.

Every discovered document query names its document ID. Selection-sensitive queries name the expected selection revision. Mismatches return `stale_document` or `stale_selection`; they never silently read a new upload. User removal cannot erase copies already downloaded or recorded in native transcripts; state that in the removal UI.

## 5. Parser projection

Use the public normal upload path:

```js
import { parseFig, nodeId } from "openfig-core";
const document = parseFig(uploadBytes);
```

`parseFigBinary` handles raw inner `canvas.fig`, not an arbitrary replacement for complete uploaded .fig processing. Go preflight and worker enclosure do not mean implementing a second .fig decoder.

Normalize to versioned bounded JSON. Enumerate active CANVAS pages, identify internal pages by a verified format rule rather than an English name, and preserve sibling order using validated `parentIndex.position`. The inspected `childrenMap` is not sorted. Validate duplicate IDs, missing parents, removed nodes, cycles and maximum depth before walking.

Expose ordinary children lazily. Frame/artboard candidates are not every FRAME: group-like/nested nodes may share that type. Keep a layer tree so documents without simple top-level artboards remain inspectable.

Project stable IDs, parent/page identity, type/name/visibility, local size/transform, bounded styles and direct text. State units and coordinate space; do not call local values absolute without verified transform composition. Reject non-finite geometry. Keep component/instance override metadata distinct until effective resolved values are verified. Never label default component text as resolved instance text.

Do not serialize compiled schemas, executable objects, raw chunk arrays, binary blobs or the whole parsed message into MCP. Bound each field as well as total response; preserve explicit truncation, stable cursors and index version. Uploaded names/text/metadata remain untrusted data, not instructions or executable markup.

Embedded image hashes identify assets, not frame previews. Keep required assets privately; never resolve file-provided host paths. Validate thumbnail MIME, dimensions, pixels and bytes before serving. Invalid/missing thumbnail produces a warning or absent cover, not a failed otherwise-valid document.

## 6. Worker bounds and offline execution

The inspected core parser expands archives/chunks synchronously and compiles the file's Kiwi schema. It has no inspected cancellation or comprehensive expansion limits. Upload caps, declared ZIP sizes and V8 heap tuning are not sufficient process/memory boundaries. [Parser source](sources.md#canvas-and-openfig-drafts).

Preflight ZIP metadata in Go: entry count, total declared expansion, required input, paths, duplicates and ambiguous canvas/meta/thumbnail/image names. Reject traversal, absolute/invalid names and collisions caused by suffix matching or flattened asset basenames. Validate actual decompression/output too; compressed inner schema/zstd content can exceed archive estimates.

Run the worker with a hard wall deadline, process-tree cancellation, bounded diagnostics/output and an enforced memory/resource enclosure. Use and test the concrete limits in contracts.md on both architectures before enabling upload. The initial worker memory budget is 512 MiB; adjust only from measured representative/hostile fixtures and document the enforced value. `--max-old-space-size` is tuning, not the hard cap.

Use private read-only input and an isolated writable output directory. No Pi credentials, project mounts, application databases, broad host HOME, privileged daemon socket or network. A same-UID subprocess with only environment filtering is fault containment, not filesystem isolation. Both Docker and direct-host deployment must establish the actual worker boundary or mark Design unavailable without weakening it.

No Figma API, online fonts/images, telemetry, package installation or downloads during upload/query/render. Use approved local assets/fallbacks with diagnostics. Test processing with external network unavailable and canary files outside the worker mounts.

Validate worker protocol/schema, unknown response fields, output paths, symlinks, sizes, finite numeric values and image headers in Go. Do not trust a helper's success report without checking its bounded artifacts.

Initial budgets from the draft, governed by contracts.md and tested rather than advertised as capacity:

| Resource | Initial budget |
| --- | --- |
| Source upload | 50 MiB |
| Declared archive expansion | 256 MiB; actual expansion is independently bounded |
| Archive entries | 4,096 |
| Normalized nodes / graph depth | 100,000 / 128 |
| Normalized index | 64 MiB through its dedicated artifact path |
| Regenerable preview cache | 128 MiB |
| Parse/render concurrency | One active heavy job initially |
| Parse deadline | 30 seconds |
| Worker diagnostics | 64 KiB |
| Image tool result | 2 MiB encoded image bytes, before MCP base64 |

Cap rendered pixels before allocation. Evict only derived caches, never source, for preview budgets. Bound queued work/admission as well as concurrent workers. Enforce the shared scratch/inode/decoded-memory limits; do not enlarge shared transport or metadata caps to accommodate the index.

## 7. UI and shared focus

Contribute an instance-scoped Design rail item. Slot 5 contains source/upload status, pages/layers/frame candidates, shared-focus controls and inspector options. Slot 4 shows the selected content, saved cover or real frame preview when supported. Reuse Mewa and shell Focus/Restore; no new tabbed workspace.

Empty/upload UI clearly states the single instance-wide slot and who can read it. Upload uses streamed binary HTTP, not chat-WebSocket base64. Provide progress/cancel, format/limit errors, file name/size/date and explicit removal confirmation.

Label the saved cover Document thumbnail. Until actual frame rendering is supported, show Frame preview unavailable with usable structure/text, not the cover repeated across frame cards. Render document strings as text.

Private browsing in a browser tab does not change agent focus. An explicit Shared focus action publishes a page and optional node/frame with optimistic selection revision. Reconcile through existing state events, including another client changing focus, document removal and reconnect.

Reference in chat inserts a compact document/page/node reference into the draft only on explicit user action. It does not automatically submit, change the model prompt or attach the whole file. An instance-scoped Design panel may remain available across project changes while clearly declaring that scope; session-specific content must not inherit its broader authority.

## 8. HTTP and MCP

Application management and MCP reads use one Design service/query index, not duplicated parsing state. Suggested application paths:

- `POST /api/design/document`: one streamed .fig part with limits/operation status; conflict for active/pending slot.
- `GET /api/design/document`: metadata, availability and shared focus.
- `DELETE /api/design/document`: current identity/revision and mutation-origin checks.
- Authenticated page/children/node/focus methods using the same query service.
- `GET /api/design/artifacts/<document-id>/<artifact-id>`: validated image-only bytes, correct MIME, no symlinks/browsing, `nosniff`, `no-store`; no tokens or host paths in URLs.

| Read-only tool | Result |
| --- | --- |
| design_status | Active ID/name/counts, parser/preview availability, warnings and shared focus/revision; no full content |
| design_structure | Required document ID, optional page/parent, bounded depth/pagination; stable native IDs and truncation |
| design_node | Required document/node IDs and bounded geometry/style/direct-text/override field groups |
| design_text | Document ID, optional page/frame root, paginated direct text with references and unresolved-override warnings |
| design_preview | Exact document ID; cover in the initial milestone, selected frame plus fixed thumbnail/detail variant only after renderer acceptance |

Return structured content and compact text. `design_preview` returns actual bounded PNG image content plus an authenticated UI artifact reference and `previewKind`, dimensions, node/document identity and warnings. An artifact URL alone is not proof the model can see the image. Never return source .fig bytes or silently substitute a cover for an unsupported frame request.

Provide `pixie://design/guide` and a tool-accessible equivalent for essential guidance if the native adapter needs it. No new raw-resource/MCP Apps frontend or Design-specific Pi protocol. No model-facing upload/remove/write/shared-focus mutation, arbitrary-file-path or web-search tools.

Separate human cookie/session authority from agent read credentials. An authorized reader cannot use management routes to mutate the slot/focus. Validate Host/Origin/auth before parsing or serving expensive artifacts. An MCP session identifier is not a security principal, and shared module credentials do not provide multi-user isolation.

## 9. Frame-preview milestone

At FIG-06, inspect newly released supported upstream APIs and test a pinned artifact. Needed contract: parsed document + node ID + in-memory image resolver + explicit approved local fonts + correctly supplied WASM/assets + bounded output, with PNG or supported SVG intermediate and diagnostics. No implicit downloads or arbitrary extraction.

Verify Design-frame semantics, image fills, component overrides, transforms, clipping, masks, effects and text. Cache by document hash/node/variant/renderer/index/font-set versions. Prioritize visible frames, bound concurrency/pixels and reject stale/deleted generations before committing results.

If a public renderer is absent, prepare a small upstream proposal and report the precise gap. A minimal export/path-resolution patch may be proposed with evidence, but private deep imports, copied render engines and an unrelated renderer are not the permanent solution. Submission/publication retains approval requirements. Continue all usable inspection work while this milestone is blocked.

Compare actual renders against owner-supplied reference screenshots using licensed .fig files with images, nested instances, masks, effects, custom fonts and pages. PNG headers prove format, not fidelity. Do not redistribute fonts merely because upstream code is MIT or obtain missing private fonts without separate authorization.

## 10. Acceptance and completion

FIG-01 verifies dependency/API/license assumptions and worker packaging. FIG-02 implements bounded normalization and transactional slot. FIG-03 exposes one read-only query/MCP service. FIG-04 integrates UI/shared focus. FIG-05 proves structure MVP, failure boundaries and both deployment variants. FIG-06 independently completes or records the renderer blocker.

Cover valid real Design files with/without thumbnails; nested/unsorted/removed nodes and overrides; duplicate/missing IDs/cycles; malformed ZIP/signatures/chunks; traversal/collisions/extreme expansion; huge text/images/index; timeout/crash/memory failure/cancellation/disk full; concurrent upload/remove/query/render; stale source/focus; disabled/unavailable management; restart/reindex; unauthorized reads/mutations; offline behavior; accessible UI; and real calls through the supported native Pi MCP integration.

**Structure MVP complete:** user enables Design, uploads one local file, inspects the same bounded pages/layers/text/cover as an authorized agent, shares explicit focus, closes the Web UI while queries still work, restarts without losing source, and removes it with immediate access revocation. No custom .fig parser, implicit cloud service or mandatory core worker runtime.

**Frame previews complete:** real upstream-rendered selected frames and model-visible images pass offline assets/fonts, limits, caching, deletion/cancellation, fidelity and packaging tests. The structure milestone cannot be used to mark this one complete.
