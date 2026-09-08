# Documentation plan

User docs describe shipped behavior; this directory describes implementation work. Track DOC tasks in [execution.md](execution.md). Factual corrections can land before runtime/UI cutover.

## File responsibilities

| File | Keep |
| --- | --- |
| Root README.md | Purpose, prerequisites, two build choices, one short path for each and links |
| docs/deployment.md | Install/configure/diagnose/upgrade/rollback/remove for assistant-only + Docker and full-host |
| docs/pi.md | Native ownership, compatibility, supported behavior and TUI handoff limits |
| docs/pi-protocol.md | Exact methods, schemas, events, framing, capabilities and errors |
| docs/pi-extensions.md | Native extension integration and Web UI translation |
| docs/mcp.md | Module endpoints, configuration, scope, availability and trust |
| docs/architecture.md | Logical ownership plus split/full-host process composition |
| docs/security.md | Actual assumptions, credentials, boundaries and residual risks for both modes |
| roadmap/README.md and linked plans | Unfinished outcomes, implementation order and acceptance |
| roadmap/execution.md | One task ledger and concise evidence |
| Root AGENTS.md | Real paths, invariants, commands, ownership and approvals |

Convert `docs/roadmap.md` to a short link to `../roadmap/README.md`. Replace both MCP draft files with links to their reviewed plans once this task lands. Pinned originals remain in `roadmap/sources.md`; do not maintain competing live plans or append the audit to quick-start prose.

Avoid repeated feature lists. Link to the owner of each fact. Generate exact method details from the schema; review introductory explanations by hand.

## Editorial rules

Write short factual paragraphs. Say what a command does, needs and reports on failure. Examples prevent operational mistakes. Comments explain constraints or non-obvious reasons, not the next line's obvious action.

Remove operator paths, dated backups, one-off timings, release history, completed milestones, unexplained audit labels, migration essays and absent directories. Do not create another user-facing archive to retain all that debris.

Retain necessary constraints: independent processes do not coordinate same-session writes; Pi has host-user authority; MCP requires native integration; Browser/worker isolation depends on actual deployment; uncertain delivery is not automatically retried; Canvas is session-scoped; Design is instance-wide; removal cannot erase already exported copies.

Proposed opening after cutover:

> Pixie is a web workspace for Pi. It uses your installed Pi, its configuration, and its native sessions. The interface combines conversations, read-only files and Git views, schedules, and optional workspace tools.
>
> Use pixie-assistant with the Docker interface, or run the complete workspace with the pixie host binary. Optional Pi extensions add tools and supported UI interactions; basic chat needs none.

Avoid “any Pi version” and “attaches to a running TUI” without qualification. State tested support and session-sharing behavior. Both releases are mandatory as defined in [builds-and-releases.md](builds-and-releases.md), not Docker-primary versus an unsupported host fallback.

The full-host binary includes assistant/controller/UI and needs no separate assistant or asset directory. It does not eliminate Pi's own runtime or optional isolated rendering/parsing dependencies. State this near installation, not hidden in troubleshooting.

## Concrete corrections

Deployment describes a Browser mount absent from Compose and invokes build without a Compose build definition. Security's benchmark reads an entire dotenv file as a bearer. Roadmap/agent guidance mixes current behavior, obsolete paths and completed operations. Reconcile against clean installs rather than the author's machine. [Evidence](sources.md#documentation-and-validation).

Update root instructions early for this roadmap, actual assistant/package paths, the shared Go facade and colocated unit tests. Do not leave requirements forcing bundled SDK execution or the old mixed-content tabs. Preserve publication boundaries and narrow product scope.

Replace non-Docker two-process instructions with the tested full-host unit/CLI when implemented. Give Docker its explicit controller-only startup. Document mode switching, native ownership conflicts, metadata locations and rollback without pretending binary replacement always reverses a schema migration.

Keep review evidence out of normal deployment prose. Do not describe Canvas/Design as shipped while unimplemented. Label saved document thumbnails distinctly from frame previews and state Canvas's actual offline rendering boundary.

## Validation

Check Markdown links, anchors and mentioned existing paths in CI. Run both installation/health/upgrade/rollback examples in disposable environments with dummy credentials. Verify flag/env precedence, placeholders, architecture choice, mode conflict and optional-dependency behavior. Future proposed paths must be labelled as such until created.

Review every paragraph: true now, needed here, and owned here? Update docs with the relevant behavior, not ahead of it. Human review of wording and installed behavior complements linting; lint alone does not establish factual accuracy.
