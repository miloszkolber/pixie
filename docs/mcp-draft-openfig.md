# Design MCP implementation plan

## Purpose and scope

Add an optional second Pixie-published MCP module, `design`, for a locally uploaded Figma Design `.fig` file. The user and agents inspect the same document without connecting to Figma or an online rendering service. OpenFig owns format decoding and any eventual rendering. Pixie owns storage, bounded queries, access control, the collaboration UI and MCP publication.

This is a handoff plan, not an implemented feature. Research used published packages and upstream fixtures. Another agent is changing the assistant integration and shared UI contracts concurrently. Rebase the implementation plan onto its completed work before editing shared files. Do not change `pixie-assistant` architecture to implement this module, or add a Pi extension specific to designs.

MVP assumptions:

- One active document across a single-user Pixie instance, available to authorized clients connected to Design MCP. This is not project-private storage: label that scope in the upload UI. If project isolation is required, decide it before implementing authorization rather than trusting a caller-provided `projectId`.
- Store the original file and derived data on disk in Pixie's application data directory. Here, "memory" means persistent application storage, not Pi transcripts, Signet, embeddings or a vector database. Do not insert the full design into model context automatically.
- Keep the file until the user explicitly removes it, including across application restarts. A second upload returns a conflict asking the user to remove the current document first. No silent replacement or automatic source-file expiry.
- Read-only design inspection. Upload, removal and collaboration selection belong to the user-facing application API, not model-facing mutation tools.
- Structure and a saved document thumbnail form the first usable milestone. Per-frame renderings are the preferred visual milestone, but are not currently proven with the published OpenFig APIs. Do not describe an embedded file thumbnail as a frame rendering.

## Research findings and decision

### Published packages investigated

| Package | Version and release source | Relevant findings |
| --- | --- | --- |
| `openfig-core` | `0.4.1`, npm gitHead `f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6` | Public `parseFig`, `parseFigBinary`, `nodeId`, maps, raw nodes, metadata, embedded thumbnail and images. Pure JavaScript parsing. Dependencies: `fflate`, `fzstd`, `kiwi-schema`. No public PNG renderer. |
| `openfig-cli` | `0.6.0`, npm gitHead `0d74102f0cba4139ca14ba2e0f31664139744154` | Public `FigDeck` reader plus slide-oriented tools. Internal Design SVG rendering exists, but the renderer is not in the package exports. Additional dependencies include sharp, Playwright, resvg WASM, PDF and MCP tooling. |

Repository default branches were newer than the published releases during research. Use the release revisions above to reproduce findings, and a lockfile to freeze transitive dependencies. Published unpacked sizes are approximately 488 KB for core and 7.0 MB for CLI, not total installation footprints.

Actual parsing checks under Node 24.18.1:

| Upstream fixture | Input size | Result | Saved thumbnail |
| --- | --- | --- | --- |
| `basic-shapes.fig` | 49,363 bytes | 11 nodes, 2 FRAME nodes, one user page through the CLI reader | 20,937 bytes |
| `medium-complex.fig` | 870,716 bytes | 211 nodes, 12 FRAME nodes, 17 TEXT nodes, three user pages through the CLI reader | 108,184 bytes |

Single measured `parseFig` calls took approximately 63 ms each, excluding imports and file I/O. These are smoke results, not capacity estimates. Neither fixture exercised embedded image assets. Frame counts include nested/group-like frames and are not necessarily artboard counts. The CLI repository has an MIT license, but fixture assets have no separately established provenance. Use a small project-owned or explicitly licensed fixture corpus for permanent tests.

Rendering checks:

- Importing `openfig-cli/lib/rasterizer/svg-builder.mjs` or `deck-rasterizer.mjs` normally fails with `ERR_PACKAGE_PATH_NOT_EXPORTED`.
- A diagnostic filesystem import produced a 6,077-byte SVG for the 267 by 280 `basic_shapes` frame. PNG conversion failed with `ENOENT`: the WASM path assumes the dependency exists under the CLI package's own `node_modules`, which was false in the tested npm installation.
- `openfig export basic-shapes.fig` exited successfully with "Exported 0 slide(s)" and no frame previews. Its current export implementation calls a SLIDE renderer, not a Design-frame renderer. Its slide filter is applied after rendering all slides.
- The two internal rasterizer files do not themselves import sharp or Playwright, but installing the CLI still installs its declared dependency tree. Deep-importing is not dependency pruning.
- The CLI font resolver can fetch Google Fonts before looking for system fonts and has no inspected offline switch or explicit download bound. Direct rendering instead requires explicit fonts and has system-font loading disabled. Image rendering expects filesystem assets rather than directly consuming core's image map.
- Bundled fonts include macOS-derived Avenir Next files. Do not redistribute those merely because the repository's source license is MIT; font rights need separate verification.

**Decision:** require only `openfig-core` for parsing and inspection. Do not install the full CLI, proxy its upstream MCP server, deep-import its renderer as production glue, vendor its SVG engine, or implement a Figma renderer from scratch. A small rasterizer wrapper alone cannot supply missing scene rendering.

## Architecture

```text
Local file upload                         Pi or another authorized MCP client
       |                                                |
       v                                                v
Pixie Design panel                               /mcp/design
       |                                                |
       +------ authenticated application API -----------+
                              |
                     Pixie Design service
                  persistent single-document slot
                   bounded normalized query index
                       selection + generation
                              |
                    disposable parse worker
                     public openfig-core API
                              |
             optional future upstream frame renderer
```

Keep the MCP control plane inside the main Pixie process using the existing Go MCP SDK. Execute parsing outside the main Go process in a bounded helper. Do not call the Browser automation module just to parse a file, and do not require a browser tab to stay open for agent queries to work.

The primary helper candidate is a small Node ESM worker depending on pinned `openfig-core`. Node is not currently a production prerequisite of the application image, so measure and document its addition. This runtime is application-side and independent of how Pi or pixie-assistant runs on the host. A compiled Bun helper is an alternative only after its dynamic Kiwi schema compilation and dependency behavior pass the same tests. Core's browser compatibility does not by itself make a browser-only parser suitable for persistent headless MCP use.

Proposed source ownership, adjusted to the current branch before implementation:

- `pixie/internal/design/`: slot lifecycle, query service, HTTP management/artifacts, MCP handler and worker supervision.
- `pixie/design-worker/`: small JS entrypoint and package manifest with `openfig-core`; participate in the repository's existing lockfile rather than create a second dependency authority.
- `pixie/webui/src/design/`: upload, page/layer browsing, shared selection, thumbnail and future preview presentation.
- `pixie/tests/go/design/`, `pixie/tests/design-worker/`, `pixie/tests/webui/design/`: lifecycle, decoder projection and UI tests. Do not move existing tests as part of this feature.

## Generalize the registry only as far as two modules require

The inspected `internal/mcpserver/registry.go` is Browser-specific despite the registry name: it rejects any other ID, stores a single Browser service pointer, constructs a one-row catalog and writes a one-module map on toggle. The controller also explicitly routes `/mcp/browser`.

Introduce a small compile-time module definition/instance interface for ID, display metadata, route, status, handler and shutdown. Register Browser and Design through it. No external module plugin system or automatic code discovery is needed.

- Persist all known module states together. Toggling Design must preserve Browser state, must not restart Browser, and must not publish in-memory state before the state write succeeds.
- Aggregate status and compute the catalog revision over a deterministic ordering of all modules. Preserve existing catalog shape where sufficient, and update tests that correctly expected one module before this feature.
- Route module traffic centrally, preserving bearer, Host, Origin, request-size and cancellation checks. Avoid a second copy of the Browser auth implementation for Design.
- Proposed Design default: disabled until explicitly enabled. Disabled stops jobs and agent access but retains the user's source document. Authenticated user management can still report and remove that document when the module is disabled or its worker is unavailable; removal must not require restoring a dependency or invoking the parser. Missing worker dependencies degrade Design only, not chat or Browser.
- Distinguish module readiness from document availability. A healthy Design module may have `document.state = empty`; this is not an application readiness failure.
- Expose connection name `pixie-design` and endpoint `/mcp/design` through the registry. Pi consumes it through the ordinary configured MCP adapter. No marker-only design extension in pixie-assistant and no hardcoded model tools in Pi.

## Persistent slot and lifecycle

Suggested storage under `<PixieDataDir>/mcp-design/`:

```text
slot.json
documents/<random-document-id>/source.fig
documents/<random-document-id>/index.json
documents/<random-document-id>/thumbnail.png
documents/<random-document-id>/previews/<opaque-cache-key>.png
staging/<upload-id>/...
```

`slot.json` points at the active immutable document and records its SHA-256, original display name, upload time, original size, index format/parser version and persisted collaboration selection. Use generated IDs, never user names or raw Figma node IDs as directory paths. Keep selected page/frame revision separate from the source identity.

State transitions:

1. Stream upload into a temporary directory with an enforced byte limit and checksum. Mark the pending operation in the UI, not as an available MCP document.
2. Validate archive/binary structure, then parse and build a normalized index in the isolated worker. Save staged outputs and validate their schemas, paths and sizes in Go.
3. Move the complete immutable document directory into place and atomically publish the slot pointer last. Only then make it queryable and emit a state event.
4. On error, retain any previously committed state, remove staging files and return a bounded diagnostic. Competing uploads cannot both commit. Retried upload completion must not create a second document.
5. On user removal, invalidate/tombstone the active document generation durably before removing its files. Cancel matching jobs and prevent their late completion from republishing state. Physical cleanup may retry, but APIs must stop serving the removed document immediately.
6. On restart, restore the committed slot, clean interrupted staging and report corrupt/missing derived state. Reindex the retained source if appropriate under the same bounds. Never silently delete the user's source or return an empty success for corruption.

Every query/preview request after discovery carries the document ID. Selection-sensitive calls also carry a selection revision. Mismatches return `stale_document` or `stale_selection`; they never silently read a later upload. A deletion cannot erase content already copied into a model transcript or downloaded by a user. Explain that limit in the removal UI.

## Parsing and normalization

Use the public parser API, not the Slides high-level API:

```js
import { parseFig, nodeId } from "openfig-core";
const document = parseFig(uploadBytes); // complete ZIP .fig
// parseFigBinary is a separate entry for raw canvas.fig, not the normal upload.
```

Core provides all nodes and maps, but Pixie must make bounded, validated projections:

- Enumerate active CANVAS pages and flag/hide internal pages by a verified format rule with an option to show them. Do not identify a page solely by an English display name.
- Preserve verified Figma sibling order using `parentIndex.position`; `childrenMap` alone is unsorted. Validate duplicate IDs, missing parents, cycles and maximum depth before walking the graph.
- Expose ordinary layer children lazily. The frame gallery uses explicit renderable/artboard candidates, not every `FRAME`: nested groups can also be FRAME nodes with `resizeToFit`. Retain a layer tree so layouts without simple top-level artboards are still inspectable.
- Project IDs, parent/page identity, name, type, visibility, local size/transform and bounded style/text fields. Do not label local coordinates as absolute coordinates without composing ancestor transforms. Include units and coordinate-space descriptions.
- Extract direct text accurately. Identify component/instance overrides separately until effective resolved values are verified. Never substitute a component's default text and label it as the instance's resolved text.
- Do not serialize `compiledSchema`, executable objects, raw blob arrays or the whole message into MCP responses. Normalize to versioned JSON with explicit truncation and page cursors.
- Embedded image hashes are asset identities, not frame screenshots. Keep bytes on disk only if needed for later queries or rendering, and never resolve a file-provided path on the host.

The saved `thumbnail.png` is an optional cover preview. Validate PNG dimensions and size before serving it, and omit it with a warning if invalid. A document without a thumbnail remains usable. Treat node names, text, metadata and tool results as untrusted document content, never as instructions to the agent.

## Worker bounds and offline behavior

Core's parser calls `unzipSync` without resource limits, expands schema/message chunks synchronously, and compiles the uploaded Kiwi schema using `new Function`. It does not expose cancellation. A 100 MiB upload cap or Node `--max-old-space-size` alone is not an adequate safety boundary.

Before accepting untrusted files, implement and test:

- Go-side ZIP preflight using archive metadata: bounded entry count and declared total expansion, allowed paths, duplicates, required canvas entry and invalid names. Core's suffix matching and image-basename flattening make ambiguous entry names unsafe. Reject ambiguous input before calling it. Metadata limits do not replace limits on actual expansion.
- A process-level wall deadline, process-group termination on cancellation, bounded stdout/stderr and a real memory/resource enclosure appropriate to the deployment. Inner zstd/schema expansion can exceed ZIP estimates. Do not describe V8 heap limits as a hard total-memory cap.
- A private read-only input, isolated writable output directory, sanitized environment and no access to Pi credentials, project mounts or application databases. A separate process under the same unrestricted UID is fault containment, not filesystem isolation.
- No online font/image retrieval, Figma API calls or dependency installation during upload/query/render. If a renderer needs missing fonts or assets, use approved local fallbacks and report them. Test processing with external network access unavailable. Do not silently weaken isolation if the deployment cannot enforce it.
- Reject out-of-bound outputs, unknown worker response fields, non-finite geometry and output paths outside the operation directory. Check stored outputs without following symlinks before returning them.

Initial configurable limits to validate with representative files: 50 MiB upload, 256 MiB declared archive expansion, 4,096 entries, 100,000 normalized nodes, depth 128, a 64 MiB normalized index, a 128 MiB derived preview cache, one parse/render job at a time, 30-second parse timeout, 64 KiB worker diagnostics. Evict only regenerable cache entries, never the source document, to meet the preview budget. These are starting budgets, not measured capacity guarantees. Select and test a real worker memory limit before deployment. CPU/memory/output budgets must apply even to malformed files.

Run the first worker as a bounded one-shot parse/index operation rather than a new persistent server. Queries use the stored normalized index. Introduce a bounded warm worker only if real measurements justify reparsing or startup optimization.

## User interface and collaboration

Add a Design panel, separate from model tool output and from MCP Apps:

1. Empty state explains the instance-wide single-file slot and which agents can access it.
2. File picker/upload progress with cancel and actionable format/limit errors. No base64 file bodies over the chat WebSocket.
3. File header with name, size, upload date, processing state and explicit removal confirmation.
4. Optional saved-document thumbnail labelled as such.
5. Page selector, lazy layer tree, frame list/cards and an inspector for names, geometry and direct text.
6. A persisted "Shared focus" action selects a page and optionally a frame for collaborating agents. Private browsing in another tab need not overwrite that focus. Return/update its revision and reconcile it over ordinary Pixie state events after reconnect.
7. When rendering is available, lazily generate thumbnails for visible frames on the selected page. Click opens a larger preview with warnings. Until then show an honest "Frame preview unavailable" placeholder, not the document thumbnail repeated on every card.
8. "Reference in chat" inserts a compact document/page/node reference into the draft on explicit user action. Do not automatically send entire pages, file bytes or hidden context to the model.

Use the existing Svelte/Mewa components, accessible keyboard navigation and focus behavior. Render document strings as text, not HTML. Reuse HTTP security conventions, but do not store designs under the Browser session cache because closing a browser session deletes it.

## HTTP and MCP surface

Keep binary upload/download on authenticated HTTP and lightweight selection/status notifications on the existing application event channel. Suggested paths are implementation details:

- `POST /api/design/document`: one streamed `.fig` multipart part, bounded upload and explicit operation status. Reject a second active/pending upload with `409`.
- `GET /api/design/document`: metadata, availability and shared selection.
- `DELETE /api/design/document`: requires current document ID/revision and normal mutation-origin checks.
- Application requests for pages, bounded children, node details and selection use the same Design service as MCP. Do not duplicate parsing/query state in controller and MCP layers.
- `GET /api/design/artifacts/<document-id>/<artifact-id>`: authenticated, validated image-only response, bounded bytes, no directory browsing, correct MIME, `nosniff` and `no-store`. Never expose local filesystem paths or put bearer tokens in image URLs.

Initial MCP tools, all read-only with respect to the uploaded design and user selection:

| Tool | Contract |
| --- | --- |
| `design_status` | Active document ID, name, counts, parser/preview availability, warnings and shared selection/revision. No file contents. |
| `design_structure` | Required document ID, optional page/parent ID, bounded depth and pagination. Returns nodes with stable native IDs and explicit truncation. |
| `design_node` | Required document/node IDs, validated bounded field groups for geometry, styles, direct text and override metadata. No raw binary blobs. |
| `design_text` | Required document ID, optional page/frame root, paginated direct text with node references and unresolved-override warnings. |
| `design_preview` | Required document ID; embedded document thumbnail in the initial milestone, actual frame ID and fixed `thumbnail`/`detail` variant after the renderer gate passes. Clearly identifies preview kind, dimensions and limitations. |

Return `structuredContent` plus compact text summaries so clients that ignore structured content still work. For previews, return bounded MCP `image/png` content as well as an authenticated UI artifact reference: an artifact URL alone does not guarantee that an agent can see it. Proposed image response cap: 2 MiB, otherwise provide an appropriately downscaled verified preview or an explicit limit error. Do not send the source `.fig` as tool output.

Optionally add `pixie://design/guide` as a documentation resource, with a tool-accessible equivalent if the actual Pi adapter needs it. MVP functionality must work through tools; it must not depend on restoring the removed raw-resource/App-view integration or adding a new Pi-specific protocol. No upload, remove, write, search-engine or arbitrary-file-path MCP tools.

Separate authorities: Pixie browser cookies/session auth authorize user management, while the module's MCP credential authorizes agent reads. A read credential must not acquire upload/delete authority through shared routes. Apply Host/Origin validation to both surfaces and verify unauthorized requests before expensive parsing or image delivery. Shared module credentials are not multi-user isolation.

## Frame-preview gate and upstream path

Per-frame previews remain a desired second milestone. Do not claim the core parser renders Figma merely because it includes vector helpers.

1. Check for a newly released supported OpenFig renderer before implementation. Pin and test the released artifact, not just repository HEAD.
2. If still unavailable, prepare a small upstream proposal for an exported Design-frame API that accepts a parsed document, node ID, in-memory image resolver, explicit local fonts, WASM bytes or correctly resolved assets, and bounded output options. It should return PNG bytes or a supported SVG intermediate with diagnostics, without implicit downloads or filesystem extraction.
3. Verify that existing renderer semantics cover Design frames, image fills, component overrides, clipping, effects and text. Cap pixel count and dimensions before rasterization, not after allocating the result. Resolve font licensing and substitutions explicitly.
4. If upstream does not yet expose this, report the gap. A minimal local export/WASM-resolution patch may be proposed with measured evidence, but adding offline asset/font injection may exceed that scope. Do not copy the renderer into Pixie or import arbitrary filesystem internals as the permanent answer.
5. Do not introduce an unrelated rendering engine merely to check the thumbnail box. The first milestone remains useful without previews, but the preview milestone stays explicitly incomplete.

Renderer acceptance uses owner-supplied or licensed real `.fig` files with embedded images, nested instances, transforms, masks, effects, custom fonts and multiple pages. Compare to supplied reference screenshots. A PNG header test verifies packaging, not fidelity. Never obtain missing fonts from a user's Figma installation or remote services without an explicit separate decision.

## Implementation sequence for the receiving agent

1. **Confirm scope and dependencies.** Accept or adjust the single-instance slot assumption. Reinspect the current branch and coordinate ownership of registry/controller/UI/Dockerfile files with the assistant workstream. Reproduce core parsing in an isolated install using exact versions and a licensed fixture.
2. **Implement bounded worker and indexing.** Establish safe upload preflight, runtime enclosure, cancellation and normalized JSON before connecting the UI. Include saved-thumbnail extraction. Do not add CLI/render dependencies yet.
3. **Implement transactional storage.** Upload, validate/index, commit, restart, conflict and deletion races. Keep source persistence independent of transient preview jobs.
4. **Add Design to the publisher.** Minimal two-module registry generalization, independent enablement, correct status and credential boundary. Keep Browser behavior and routes unchanged.
5. **Deliver the usable UI and MCP milestone.** Page/layer/frame inspection, saved cover image, node/text MCP queries, shared focus, image tool content and explicit removal. Verify data remains queryable with the Web UI closed.
6. **Complete frame previews if the renderer gate passes.** Implement only the upstream-backed adapter, cache by document hash/node/variant/renderer/font-set version, prioritize visible frames, bound concurrency and revoke stale jobs/results on removal.
7. **Package and measure.** Add only actual required runtime assets. Record clean-image footprint and startup/RSS/query/render timings, including Node or compiled-helper costs. Validate Linux x86-64 and arm64 where supported. Preserve ordinary Pixie startup when Design is disabled/unavailable.
8. **Review and hand off.** Update MCP, architecture, security and deployment docs to describe a second optional module, not "Browser only" as a permanent restriction. Add roadmap status only after coordinating with its owner. Report both milestone statuses and remaining evidence. No push, package publication or deployment without the user's existing explicit approval requirements.

## Acceptance matrix

- Core API smoke with real Design files, not only `.deck` files or fabricated parser JSON.
- Nested/unsorted/removed nodes, internal pages, frames versus groups, duplicate/missing IDs, cyclic graph and rich-text/override limitations.
- Valid `.fig` without thumbnail, malformed archive, truncated chunks, wrong signature, duplicate/path-traversal entries, extreme declared expansion, hostile schema and oversized node/image outputs.
- Parser timeout, crash, memory exhaustion, cancelled upload, disk full, interrupted rename and restart recovery. Neither controller nor Browser is taken down by a worker failure.
- Second upload conflict, concurrent remove/query/render, stale document/selection reference, disabled module agent access and eventual physical cleanup without republishing removed content. Verify user removal while disabled or worker-unavailable, followed by restart, without restoring access or calling the parser.
- Unauthorized upload/delete/read, wrong Host/Origin, no token in URLs/logs, document text escaping and no external network dependency.
- UI empty/processing/error/ready states, page selection, pagination, focus reconciliation, removal confirmation and accessible fallback when rendering is unavailable.
- Real MCP handshake and direct calls through the supported Pi adapter, including structured output and actual image bytes. Do not treat a model saying "rendered" as evidence of a successful tool result.
- Two-module persistence and routing, independent failure/toggle behavior, Browser regression suite and primary app readiness with Design disabled.
- Preview milestone additionally requires successful clean-install renderer initialization, deterministic offline font/assets behavior, bounded pixels, representative screenshot comparisons and measured cost.

## Completion definitions

**Structure MVP complete:** user enables Design, uploads one local `.fig`, sees pages/layers/frames and any valid saved document thumbnail, shares a stable focus, and an authorized MCP client reads the same bounded data without a browser tab or Figma service. Restart preserves the file. User removal immediately makes it unavailable and cleans stored data. No custom `.fig` parser or interactive MCP App feature is introduced.

**Frame-preview milestone complete:** the selected page shows actual upstream-rendered frame thumbnails; agents can request the same bounded images; offline rendering, cache invalidation, fidelity limitations, font rights and cancellation are tested. This milestone is not yet proven by research and must not be marked complete from embedded-thumbnail support.

## Sources

- [Core README and public API](https://github.com/OpenFig-org/openfig-core), [published parser source](https://github.com/OpenFig-org/openfig-core/blob/f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6/src/parser.ts), [types](https://github.com/OpenFig-org/openfig-core/blob/f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6/src/types.ts).
- [CLI package exports and dependencies](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/package.json), [Design SVG builder](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/lib/rasterizer/svg-builder.mjs), [WASM rasterizer](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/lib/rasterizer/deck-rasterizer.mjs).
- [CLI export implementation](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/bin/commands/export.mjs), [font resolver](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/lib/rasterizer/font-resolver.mjs), [fixture directory](https://github.com/OpenFig-org/openfig-cli/tree/0d74102f0cba4139ca14ba2e0f31664139744154/test/fixtures/figs/reference), [CLI MIT license](https://github.com/OpenFig-org/openfig-cli/blob/0d74102f0cba4139ca14ba2e0f31664139744154/LICENSE).
