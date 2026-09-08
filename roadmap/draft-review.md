# Canvas and Openfig draft review

Reviewed inputs: the baseline [Canvas and Openfig drafts](sources.md#canvas-and-openfig-drafts). The implementation plans retain their product scope but reconcile source paths, registry work, UI ownership and security. Neither module is implemented by this roadmap commit. Both deployment variants in [builds-and-releases.md](builds-and-releases.md) must handle the modules' optional dependencies and trust boundaries.

## Canvas

Retain: disabled-by-default `canvas` / `pixie-canvas`, `/mcp/canvas`, one document per chat session, six tools, full HTML writes with optimistic versions, immutable revisions, module-owned agent-browser sessions, explicit removal, 512 KiB write and 64 MiB storage budgets, bounded actual image responses, a Mewa guide, and offline preview behavior.

| Draft gap | Resolution in canvas.md |
| --- | --- |
| A shared module bearer cannot identify the originating Pi session | Resolve session authority from server-issued scoped credentials or a tested session connection context; never trust a tool's sessionId. |
| CSP/no-fetch is described as a network boundary | Enforce egress denial in the renderer's actual execution enclosure. CSP is defense in depth. Include redirects, frame navigation, DNS, sockets and host services in testing. |
| Authenticated HTML URL is also supposed to work in a credential-free screenshot process | Feed the authorized immutable document to the isolated job through private input/IPC; never give it application cookies or a broad bearer. |
| An iframe sandbox attribute does not accompany top-level headless navigation | The worker boundary applies regardless of navigation mode. Do not rely on embedding attributes for screenshots. |
| Script-enabled HTML in the user's browser can undermine the offline promise | Default live viewing uses validated raster output from the isolated renderer. Direct interactive HTML is conditional on equivalent verified containment. This explicitly revises the draft's serving path. |
| A separate agent-browser session still shares UID/files/network | Require a tested worker boundary or report Canvas unavailable; existing Browser conventions are not isolation. |
| Version checks do not define retry identity or quota races | Add mutation identities, reservations, commit points and generation/version-bound jobs. |
| Disable/removal leaves cleanup ambiguous | Disable revokes reads/jobs but retains documents; authenticated owner management can remove them. Tombstones prevent restart or stale-job resurrection. |

Raster-first live preview preserves agent-authored HTML, human-visible updates and screenshot iteration. It does not claim direct DOM interaction in the user's browser. A richer interactive view must keep the same network and credential boundary. [Policy references](sources.md#external-contracts).

The original second-module registry work is absorbed by the shared extension stream. Canvas uses slots 4/5/6 and must not fork Browser's registry or implement another MCP client in the assistant.

## Openfig

Retain: `design` / `pixie-design`, `/mcp/design`, disabled by default, one instance-wide document, explicit scope disclosure, original source retained until removal, conflict on a second upload, read-only agent tools, human-owned upload/remove/shared-focus actions, offline headless inspection, public `openfig-core` parsing, and separate structure/renderer milestones.

| Draft gap | Resolution in openfig.md |
| --- | --- |
| Old pixie/internal and pixie/webui paths | Use package/internal/design, package/design-worker and package/webui/src/design; keep assistant/ independent. |
| Repeated two-module registry generalization | Reuse the shared Browser/Canvas/Design registry. Openfig is the last feature, not a second registry project. |
| Design panel could inherit session/project scope accidentally | Declare instance scope in descriptor and UI; current chat context is not authorization. |
| Helper runtime could become mandatory for core chat | Package an optional application-side worker; no Node/Bun requirement for the Go assistant or basic full-host workspace. |
| Worker memory boundary not selected | Choose, enforce and fault-test an actual process/container limit before untrusted uploads. Heap tuning is insufficient. |
| Shared focus and private navigation could conflict | Persist only explicit shared-focus actions with optimistic revision. |
| Saved thumbnail could imply per-frame rendering | Return preview kind/dimensions, label the cover and report unavailable frame rendering explicitly. |
| Reindex could revive stale selection or jobs | Keep immutable source identity, parser/index revision and separate selection revision; validate node references and reject stale work. |

The inspected core parser performs synchronous ZIP/chunk expansion and compiles an uploaded schema. The CLI export map does not export its internal Design rasterizers. These findings support bounded public-API integration, not assertions about every future release. Recheck released artifacts at FIG-01/FIG-06. [Pinned upstream sources](sources.md#canvas-and-openfig-drafts).

## Prior research evidence

The original Openfig draft records core 0.4.1 at `f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6` and CLI 0.6.0 at `0d74102f0cba4139ca14ba2e0f31664139744154`. Their manifests and core parser were inspected for this consolidation. The following execution results are the draft's prior observations, not rerun results:

| Fixture | Input | Draft-reported parse | Saved thumbnail |
| --- | --- | --- | --- |
| basic-shapes.fig | 49,363 bytes | 11 nodes; 2 FRAME nodes; one user page | 20,937 bytes |
| medium-complex.fig | 870,716 bytes | 211 nodes; 12 FRAME nodes; 17 TEXT nodes; three user pages | 108,184 bytes |

The draft reports roughly 63 ms per single parseFig call under Node 24.18.1, excluding import and I/O. Neither fixture exercised embedded images. These are smoke observations, not capacity/fidelity guarantees. FRAME counts are not artboard counts. Reported unpacked package sizes (about 488 KB core and 7.0 MB CLI) are not installed runtime footprints.

It also reports blocked normal deep imports, an internal SVG result followed by missing-WASM failure, a successful CLI export with zero Design-frame output, implicit font-download concerns, and unresolved font/fixture rights. Keep these as investigation leads with pinned references. Do not turn them into passing tests, proof that rendering is impossible, or permission to copy private renderers/fonts.

## Order and completion

Canvas is phase 9; Openfig is phase 10. Research can occur earlier, but neither delays the vanilla assistant or shell foundation through speculative integration work. Required release preparation covers both binary variants before core cutover.

Canvas completes only with session authority and tested offline execution. Openfig structure completes independently of frame previews. A verified upstream renderer gap blocks only that milestone and stays visible in the task ledger. Publication approval remains separate.
