# 09 — Openfig / Design

Owner: I with E/F/G. This is the final implementation stage, after Canvas K6. It supersedes docs/mcp-draft-openfig.md. Use the established registry, shell and worker contracts rather than reopening assistant architecture or introducing a Design-specific Pi extension. [Sources](sources.md#canvas-and-openfig).

## Scope and retained decisions

Publish an optional module with ID design, connection pixie-design and MCP route /mcp/design. The UI label is Design; Openfig is the parsing/rendering dependency, not a second module ID. Default disabled. One active locally uploaded Figma Design .fig document across the single-user Pixie instance.

Adopt the draft's instance-wide slot for this implementation. It is deliberately available to authorized Design read clients, not project-private. Explain this before upload and beside shared focus. Do not imply project isolation by accepting a caller-supplied projectId. A future scope change requires an explicit authorization/storage migration.

Persist original and derived data under Pixie application storage until explicit user removal. This is not Pi transcript memory, Signet, embeddings or a vector database. No Figma account/API/cloud renderer and no automatic insertion of the whole design into model context. A second active or pending upload returns conflict; no silent replacement or source expiry.

Inspection is read-only. Upload, deletion and shared-focus changes belong to authenticated human management APIs, not model tools. Disable stops processing and agent/content access but retains the source; human status/removal remains available while disabled or worker-unavailable.

Two separate completions: I1/K7a provides structure, direct text, shared focus and any valid saved cover thumbnail. I2/K7b provides actual frame rendering through a supported upstream API. Never label the first as completion of the second.

## Dependency decision and verification

The draft investigated openfig-core 0.4.1 at f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6 and openfig-cli 0.6.0 at 0d74102f0cba4139ca14ba2e0f31664139744154. The reviewed core exports parsing, node utilities and geometry helpers, not a complete public PNG Design renderer. The draft reports CLI deep-import/export/font/WASM limitations and limited fixture smoke results. Those experiments were not rerun during roadmap consolidation.

Start with pinned openfig-core only. Reproduce public parsing from a clean release installation and a licensed real .fig fixture. Check newer published releases before the renderer stage; repository HEAD or a marketing claim is not a supported artifact. Freeze dependencies in the existing workspace lock.

Do not install/proxy the full CLI MCP stack, deep-import private renderer files, vendor its SVG engine or write a Figma renderer from scratch. A thin rasterizer wrapper cannot supply missing scene rendering. Do not redistribute CLI-bundled fonts merely because source code is MIT; font and fixture provenance are separate checks.

## Source and runtime

```text
User upload / Design viewer        Pi or another authorized read client
             \                          /mcp/design
              → Go Design service ←
                 persistent slot
                 bounded normalized index
                 shared focus revision
                         ↓
                 isolated one-shot worker
                 public openfig-core API
                         ↓
                 optional public frame renderer
```

Use package/internal/design, package/design-worker, package/webui/src/design, package/tests/go/design, package/tests/design-worker and package/tests/webui/design. These paths are relative to the repository root; do not introduce the draft's obsolete pixie/internal tree.

The control plane remains in Go and uses the existing MCP SDK. Parsing runs outside the controller in a bounded one-shot helper; queries read the persisted normalized index. The initial helper candidate is a small Node ESM entrypoint with core only. A compiled Bun helper is acceptable only after equivalent schema/runtime/artifact tests. Core browser compatibility does not make browser-only parsing suitable for headless persistent MCP.

The worker is application-side, unrelated to host Pi or the Go assistant. Document and measure any Node/compiled-helper footprint. Package it as an optional worker/runtime capability; missing it must not prevent ordinary Pixie, Browser or Canvas startup. Do not require a UI tab to remain open or use Browser automation just to parse .fig.

## Worker boundary and limits

The reviewed parser synchronously unzips and expands chunks and dynamically compiles an uploaded Kiwi schema without cancellation. Treat all source bytes as hostile. A source-size check or V8 heap flag alone is not a total memory/filesystem/network boundary. Use the enforced job enclosure in [05](05-reliability-security.md).

Preflight ZIP metadata and actual extraction: signature/required canvas entry, allowed relative names, entry count, duplicates, traversal, ambiguous suffix matches, collisions from image basename flattening and declared/actual expansion. ZIP bounds do not bound inner zstd/schema expansion, which still requires runtime memory/CPU/wall limits. Reject ambiguous inputs rather than choosing the first matching entry.

Give the parser immutable source input and an isolated output directory. It cannot read Pi credentials, project mounts, controller storage or sibling jobs, install dependencies, call Figma, fetch fonts/images or reach external/host services. Sanitize environment and enforce resource limits; do not silently fall back to an unrestricted same-UID process. Processing is unavailable if required enclosure cannot be established.

Validate response schemas, finite geometry, paths and byte counts in Go. No raw executable objects, compiled schemas or arbitrary output files become API responses. Recheck files without following symlinks and validate image dimensions before decoding/serving. Terminate the entire job on cancellation, deadline, memory or output overflow.

Initial budgets, to validate with representative files:

| Resource | Starting limit |
| --- | --- |
| Source upload | 50 MiB, streamed |
| Declared ZIP expansion | 256 MiB, plus actual extraction bounds |
| Archive entries | 4,096 |
| Normalized nodes / depth | 100,000 / 128 |
| Normalized index | 64 MiB |
| Derived preview cache | 128 MiB; source never evicted |
| Parse/render concurrency | One job per Design module |
| Parse wall time / diagnostics | 30 seconds / 64 KiB |
| MCP image | 2 MiB decoded, with encoded envelope separately bounded |

Select and test a real total memory/PID/CPU and preview pixel limit before enabling the worker. These budgets are starting parameters, not measured supported capacity. Lazy bounded warm workers require profiling evidence; the first implementation is one-shot parsing with stored-query service.

## Persistent slot

Use <PixieDataDir>/mcp-design/ with slot.json, documents/<opaque-id>/source.fig, index.json, optional thumbnail.png, previews/<opaque-cache-key>, and staging/<upload-id>. Never use uploaded filenames or raw node IDs as paths.

Slot metadata records source ID/generation/hash/name/size/time, parser/index format versions and persisted shared-focus identity/revision. Selection revision is separate from immutable source identity. Keep sensitive content out of generic health logs.

1. Authenticate, reserve the empty slot and stream one bounded multipart source to staging with checksum. UI reports processing; MCP cannot query an uncommitted document.
2. Preflight, parse and normalize inside the worker. Go validates all staged outputs and image limits.
3. Commit the immutable directory and atomically publish the slot pointer last. Only then emit availability. Concurrent uploads cannot both commit; retry completion is idempotent.
4. Failure removes staging and returns bounded diagnostics without fabricating an empty-success document. Restart cleans interrupted staging and restores the committed slot.
5. Human removal checks current ID/generation, durably tombstones, cancels work and revokes new content/agent reads before physical cleanup. Cleanup retries independently of worker availability.
6. Missing/corrupt derived index is a recoverable diagnostic; a bounded reindex can use the retained source. Never silently delete the source or treat corrupt state as an empty slot.

Every query after discovery requires document ID/generation. Selection-sensitive queries carry expected selection revision. Return stale_document/stale_selection rather than querying a new upload silently. Bind pagination cursors to document, query and index generation. Removed content already downloaded or recorded in a model transcript cannot be recalled; state that in removal UI.

## Parsing and normalization

Use public parseFig/nodeId for complete ZIP .fig files, or the separately supported parseFigBinary entry only for a deliberately pre-extracted canvas payload. Keep original uploaded bytes intact regardless of processing path. No handwritten format decoder.

Enumerate active CANVAS pages using verified format rules; flag internal pages and offer an option to show them. Do not recognize a page by an English name alone. Preserve sibling ordering from verified parentIndex.position semantics; childrenMap is not sufficient evidence of sorted order.

Validate duplicate IDs, missing parents, cycles and depth before graph traversal. Lazy page/child queries expose stable native node IDs and explicit truncation. Frame candidates are not every FRAME node: nested groups may share that type. Keep a layer tree for documents without simple top-level artboards.

Normalize identity, parent/page, name/type/visibility, local size/transform, bounded style fields and direct text. Label coordinate space and units. Do not call local coordinates absolute without validated ancestor transforms. Non-finite geometry is rejected.

Keep component/instance overrides distinct until effective values are verified. Never substitute component defaults and label them resolved instance text. Expose unresolved warnings. Unknown structures are supported as bounded unknowns rather than silently discarded or falsely interpreted.

Do not serialize compiledSchema, raw chunks/blobs or the whole parsed message. Embedded image hashes identify assets, not screenshots. File-provided asset paths are never host paths. Store only necessary bounded outputs; treat names/text/metadata as untrusted data, not agent instructions.

A valid saved thumbnail is an optional document cover. Verify MIME/dimensions/bytes; omit invalid cover data with a warning. A file without a cover remains inspectable. Never repeat the cover on frame cards and call it a rendered preview.

## HTTP and MCP

Binary upload/download stays on authenticated HTTP, not base64 chat WebSocket bodies. Reuse one Design service for human queries and MCP projections; do not duplicate parsing or slot state.

Suggested management paths: POST/GET/DELETE /api/design/document and authenticated image-only /api/design/artifacts/<document-id>/<artifact-id>. Delete requires the current identity/revision; second active/pending upload returns 409. Use normal mutation-origin checks, bounded upload/status/cancel, validated MIME/no-follow/no-store/nosniff and no directory listing. Tokens never go in URLs.

Read credentials authorize the explicit instance-wide document but not upload/delete or shared-focus mutation. Human metadata/removal remains available when disabled or the worker is absent. Authentication/Host/Origin checks precede expensive parse or image delivery.

| Tool | Read-only contract |
| --- | --- |
| design_status | Document ID/generation, name/counts, parser/preview availability, warnings and shared focus/revision; no full file |
| design_structure | Required document identity; optional page/parent, bounded depth/page cursor; stable native IDs and truncation |
| design_node | Required document/node; allowlisted bounded geometry/style/direct-text/override groups; no raw blobs |
| design_text | Required document; optional page/frame root; paginated direct text with node references and unresolved warnings |
| design_preview | Exact document plus explicit cover or frame kind; frame ID and fixed thumbnail/detail variant only after renderer support; kind/dimensions/warnings in result |

Return structuredContent plus concise text. Image previews also return real bounded MCP image bytes, not only an artifact URL the model cannot fetch. Do not output source .fig bytes. Provide pixie://design/guide and a tool-accessible equivalent where required by the native adapter. No model upload/remove/write/arbitrary-path tools, separate search engine or revived interactive MCP App framework.

## Workspace and shared focus

Register Design through the same contract as Browser/Canvas. Column 5 contains document status/management, page/layer/frame navigation and shared-focus controls; column 4 presents bounded inspection and cover/actual frame preview. Use shell Focus/Restore and Mewa rather than inventing another layout. Instance-wide scope remains visible across session switches.

UI includes empty, upload progress/cancel, parse error, ready, disabled-with-retained-file and worker-unavailable states. The header shows source name/size/time and explicit confirmed removal. A processing upload is not an available document.

Private browsing selection is local. Only an explicit Shared focus action updates persisted page/optional frame and revision for agents. Compare expected revision to avoid two tabs silently overwriting one another. Reconcile on ordinary state events and reconnect.

Reference in chat inserts a compact document/page/node reference into the current draft on explicit action; never auto-send source, pages or hidden context. Escape all document text. Keep keyboard/focus/pagination accessible. Actual frames, when supported, load lazily for visible items; before that show Frame preview unavailable honestly.

## Frame-renderer gate

Check newly published supported Openfig APIs before implementing rendering. Pin a clean-install artifact and verify Design-frame semantics, not only Slides export. Required inputs: parsed document, node ID, bounded options, in-memory/local image resolution, explicit licensed local fonts, and correctly resolved WASM/runtime assets. Required outputs: bounded PNG or a supported SVG intermediate plus diagnostics, with no implicit downloads.

Test image fills, component overrides, transforms, clipping/masks, effects and text. Bound pixel count/dimensions before raster allocation. Cache by source hash, frame/variant, renderer version and font/asset-set version; evict only regenerable cache. Cancel/revoke results on remove/disable and do not publish a stale document image.

If the public API is insufficient, record exact missing capabilities and prepare a small upstream proposal. Submission needs separate approval. A narrow export/WASM-resolution patch can be proposed with evidence; offline asset/font support may require more than such a patch. Do not deep-import or copy a renderer into Pixie as a permanent workaround.

No unsupported renderer is substituted merely to tick the preview box. Complete I1 and all unblocked tests; leave I2 explicitly blocked until the supported path is demonstrated. This is an API/verification gate, not a claim that frame rendering is impossible.

## Sequence and acceptance

Sequence: reproduce released public parser with owned/licensed fixtures; implement enclosure/preflight/index; transactional slot and deletion; register Design as the third module; deliver human inspection and five MCP tools; then pass the renderer gate and implement frame images; package, measure and document both statuses.

K7a tests real .fig files, nested/unsorted/internal/removed nodes, frames versus groups, text/overrides, cycles/duplicates, missing/invalid cover, archive/chunk corruption, path traversal/collisions, expansion attacks, hostile schema, output limits, timeout/crash/memory exhaustion, cancelled upload, disk/rename failure and restart. Design worker failure cannot take down controller/Browser/Canvas.

Also test concurrent upload/remove/query/render, stale IDs/focus, unauthorized/wrong-role requests, disabled-module reads, removal while dependency-missing followed by restart, no token leakage, text escaping and external-network denial. Native MCP initialization/tool calls must return the expected structure and actual image bytes with the UI closed. Three-module persistence/routing/toggle/failure tests replace the draft's obsolete two-module assumptions.

K7b additionally requires clean-install renderer startup, deterministic offline fonts/assets, bounded pixels/cache, cancellation and representative visual comparison against owner-supplied/licensed reference screenshots. PNG-header validity alone is not fidelity. Do not obtain fonts from a Figma installation or remote service without an explicit authorized/licensed source.

Measure optional image footprint and cold start/RSS/query/render timings on supported amd64/arm64 configurations. Report limitations per format/rendering feature. Structure acceptance does not imply complete Figma fidelity, and source retention never depends on transient Browser cache lifetime.
