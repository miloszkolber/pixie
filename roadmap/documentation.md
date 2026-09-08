# Documentation plan

Current behavior belongs in docs/. Implementation requirements and evidence belong in roadmap/. [execution.md](execution.md) tracks DOC tasks. Never describe planned Go/UI/module behavior as shipped before its tests pass.

## Ownership

| File | Responsibility |
| --- | --- |
| Root README.md | Purpose, prerequisites, currently available install path and links |
| docs/deployment.md | Tested install/configure/diagnose/upgrade/rollback/uninstall for both target modes when shipped |
| docs/pi.md | Native ownership, supported Pi profiles and same-session handoff limits |
| docs/pi-protocol.md | Exact generated method/event/schema details and concise transport explanation |
| docs/pi-extensions.md | Native inventory/configuration, optional bridge and UI mapping actually available |
| docs/mcp.md | Module endpoints, scopes, configuration and trust |
| docs/architecture.md | Actual logical/state/process ownership |
| docs/security.md | Real assumptions, credentials, enforcement and residual risks |
| roadmap/README.md | Implementation entry point |
| roadmap/execution.md | One task/dependency/evidence ledger |
| roadmap/contracts.md | Target identities, defaults, transitions and migrations |
| roadmap/feature-coverage.md | Retained-feature inventory and cutover evidence |
| Root AGENTS.md | Paths, invariants, test/commit rules and roadmap authority |

Remove docs/roadmap.md, docs/mcp-draft-canvas.md and docs/mcp-draft-openfig.md entirely; do not keep redirect stubs or parallel draft copies. Their reviewed requirements live here. Pinned historical source links in sources.md remain valid evidence. Keep architecture, deployment, protocol and security documentation: operating the current product is different from implementing its replacement.

Update all live links and source-comment references to the deleted paths, including root guidance and docs/pi-extensions.md. Preserve only explicitly pinned historical URLs. Validate the entire Markdown tree, not just new files.

## Writing

Use short factual paragraphs and direct examples. One fact has one home. Describe what a command needs and how failure is diagnosed. Comments explain non-obvious constraints, not the next line's action.

Remove machine-specific paths/backups, one-off timings, repeated feature lists, completed operations, release diaries, obsolete source names and unverified security language. Review evidence belongs in the roadmap review files, not quick starts. Keep essential constraints and actionable failure information.

After cutover the introduction can read:

> Pixie is a web workspace for Pi. It uses your installed Pi, its configuration and its native sessions. Use pixie-assistant with the Docker interface, or run the complete workspace with the pixie host binary.

Explain that Pi's own runtime and optional isolated workers remain separate requirements. The full-host build is one binary/service, not an unsupported alternative or two executables managed separately. Base chat needs no native extension; advanced web administration requires the capability profile in feature-coverage.md. Never describe a TUI fallback as equivalent web functionality.

Release/download instructions use the exact sha-<12> identity from builds-and-releases.md for GitHub assets and Docker. The full commit/image digest is available for verification. Do not require semantic versions, compare hashes as versions or present latest as immutable.

## Corrections and migration documentation

Fix existing invalid source links and absent mount descriptions. A Compose service without a build definition should not be documented as locally built. Read an actual environment key rather than the entire dotenv file for authentication examples. Distinguish container runtime flags, filesystem access and real worker isolation.

Update root guidance for assistant/ and package/, shared Go composition, independent selections, optional bridges and colocated Go unit tests. Do not keep instructions that forbid the implementation explicitly requested here.

Document exact flag/config/environment precedence; first-run pairing for Docker and internal pairing for full-host; native ownership conflicts; state locations; corrupt-state diagnostics; supported release profiles; and safe mode switching. Include recovery of uncertain delivery and pending deletion authority, not only happy-path binary replacement.

Explain Canvas's session scope and raster-first live preview, Design's instance-wide scope, actual worker requirements and the difference between saved cover images and frame rendering. Do not imply deletion can erase content already copied to transcripts/downloads.

## Validation and completion

Add a Markdown link/anchor checker that understands local relative links, case, encoded paths, duplicate heading anchors and pinned external source URLs. Test deleted-path references separately; exclude pinned historical URLs only, not arbitrary stale prose. Check code examples, source paths and task IDs as well as Markdown links.

Run both install/health/upgrade/rollback/mode-switch examples in disposable environments using final artifacts and dummy credentials. Run docs-only validation independently of source/image publication; a roadmap edit must not accidentally publish a container.

DOC is complete when examples work, current docs match shipped features, all live links resolve and the three duplicate planning files are absent. Human factual review complements linting; passing a link check does not prove runtime behavior.
