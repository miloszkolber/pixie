# Openfig roadmap

Openfig lets a user upload a local Figma Design `.fig` file and lets people and authorized agents inspect the same persistent document without Figma, an online renderer or a browser tab staying open. One document is active across the single-user Pixie instance. Openfig owns decoding and any eventual scene rendering; Pixie owns storage, bounded normalized queries, access control, shared focus, the UI and MCP publication.

This plan is self-contained. The module is disabled by default and optional: missing dependencies or containment degrade Design only, and basic chat never depends on it.

## Current state

The service lives in `internal/design` (slot lifecycle, preflight, parser interface, upload, normalized index, queries, HTTP and MCP) with its contribution in `webui/src/design`. Tests sit in `tests/go/design` and `tests/webui/design`.

Implemented at source level:

- One instance-wide read-only slot. Upload and removal are human application operations; MCP tools cannot mutate the slot or shared focus. The original source is retained until explicit removal, including across restarts. A second active or pending upload conflicts.
- A bounded ZIP preflight that rejects traversal, absolute or invalid names, duplicate paths, flattened-basename collisions, excessive entries and declared expansion, and independently bounds observed decompression.
- A transactional upload lifecycle: staged, hashed source, one-shot parse, validated normalized output, atomic move, published slot pointer last, tombstoned and payload-cleaned removal, and restart reconciliation of interrupted uploads.
- A normalized index stored through a dedicated bounded artifact path (64 MiB), not generic persisted JSON or a host/browser frame, with bounded cursors that bind document, generation, kind, page, parent, root and depth.
- Read-only tools `design_status`, `design_structure`, `design_node`, `design_text` and `design_preview`, plus `pixie://design/guide` / `design_guidance`.
- Bounds: 50 MiB source upload, 256 MiB declared expansion with an independent observed bound, 4,096 archive entries, 100,000 normalized nodes, graph depth 128, 100 nodes and 256 KiB per query, 2 MiB encoded PNG, 2048 preview dimension, 4,194,304 decoded pixels, 64 KiB worker diagnostics and 512-byte cursors.
- A minimal UI contribution with instance status, document summary, cover preview and explicit refresh in the existing workspace slots.

`design_preview` currently supports `kind: "cover"` only. A selected-frame request returns `unavailable`; the saved document cover is never presented as a frame.

Integration tests use `tests/internal/designfixture/parser.go`, an offline fixture adapter that accepts a preflighted archive containing `design.json` or `fixture.json` and fails closed on an archive without one. Production exposes the parser interface without a built-in fixture implementation; Figma decoding still requires the contained upstream parser.

## Blockers

1. **Independently released licensed parser and runtime behind the `Parser` interface.** There is no real `.fig` decoder. Openfig must use a pinned, publicly released parser through the existing `Parser` interface, reproduced from a clean install with a lockfile and owned or licensed real `.fig` fixtures. Do not require a CLI, proxy an upstream MCP service, deep-import private rasterizer paths, copy a scene engine into Pixie or implement a Figma renderer from scratch.
2. **Pinned design worker package.** Parsing must run outside the Go controller in a one-shot, enclosed worker. No design worker package or dependency lock exists, and there is no enforced memory, process, filesystem or network boundary. The inspected upstream parser expands archives and compiles schemas synchronously with no inspected cancellation or expansion limits, so upload caps and heap tuning are not process or memory boundaries. A same-UID subprocess with environment filtering is fault containment, not isolation.
3. **Focus and draft identity.** The shared-focus selection revision, the private browsing state and the chat draft reference need end-to-end identity guards. A changed document, generation or selection must return `stale_document` or `stale_selection` and never silently read a replacement. Draft reference insertion stays an explicit user action that never submits, changes the model prompt or attaches the whole file.
4. **Hostile, offline and recovery tests.** The structure milestone needs processing with the network unavailable and canary files outside the worker mounts, plus malformed signatures, traversal and collisions, extreme expansion, huge text/images/index, timeout, crash, memory and cancellation failures, disk-full, concurrent upload/remove/query, stale source or focus, unauthorized reads and mutations, and restart/reindex.
5. **Real upstream frame rendering.** Rendering a selected frame requires a released public upstream Design-frame API with a pinned artifact, an in-memory image resolver, approved local fonts and bounded PNG or SVG output. PNG headers prove format, not fidelity, so rendering must be compared against owner-supplied reference screenshots using licensed files with images, nested instances, masks, effects, custom fonts and pages. This is the FIG-06 milestone.

## Phased plan

### Phase 1 — dependency and packaging (FIG-01)

- Select and pin one independently released, licensed parser and runtime, and adopt it behind the `Parser` interface.
- Add a minimal design worker package with its own lockfile and no runtime dependency in the full-host core binary.
- Reproduce the pinned parser in a clean install with owned or licensed real `.fig` fixtures.

### Phase 2 — normalization and slot (FIG-02)

- Keep bounded Go preflight and validate worker schema, unknown fields, output paths, symlinks, sizes, finite geometry and image headers before commit.
- Normalize to versioned bounded JSON: active pages, stable node IDs, validated sibling order, duplicate/missing/cycle/depth checks.
- Confirm the transactional slot: one active document, retry identity, tombstone-first removal, restart reconciliation and dedicated index artifact.

### Phase 3 — queries and MCP (FIG-03)

- Expose one read-only query service over the stored index for structure, node, text and preview reads with explicit truncation and stable cursors.
- Keep management routes separate from agent read credentials; an authorized reader cannot mutate the slot or focus.

### Phase 4 — UI and focus (FIG-04)

- Contribute the instance-scoped Design rail item, upload progress/cancel, clear single-slot scope, and explicit removal confirmation.
- Prove private browsing, explicit shared focus publication, optimistic selection revision, reconciliation across clients and reconnect, and the explicit draft-reference action.

### Phase 5 — structure MVP and boundaries (FIG-05)

- Run hostile, offline and recovery tests against the actual worker enclosure on both the Docker and local-process deployments and both architectures.
- Prove processing with external network unavailable and cannot read a canary file outside its mounts.

### Phase 6 — frame rendering (FIG-06)

- Inspect newly released supported upstream APIs and test a pinned artifact for real selected-frame rendering.
- If no public renderer exists, prepare a small upstream proposal and record the precise gap; continue all usable inspection work while this milestone is blocked.

## Acceptance criteria

**Structure MVP.** A user enables Design, uploads one local file, inspects the same bounded pages, layers, text and cover as an authorized agent, publishes explicit shared focus, closes the Web UI while queries still work, restarts without losing the source, and removes it with immediate access revocation. No custom `.fig` parser, implicit cloud service or mandatory core worker runtime is required.

**Frame previews.** Real upstream-rendered selected frames and model-visible images pass offline assets and fonts, limits, caching, deletion and cancellation, fidelity and packaging checks. The structure milestone cannot mark frame previews complete, and a cover is never substituted for an unsupported frame request.
