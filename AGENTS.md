# Pixie

Pixie is a lightweight web workspace for the user's installed Pi.

## Read first

Read README.md and the current behavior in docs/architecture.md, docs/pi.md, docs/security.md, docs/deployment.md and docs/development.md relevant to the task. Use [roadmap/roadmap.md](roadmap/roadmap.md) as the central implementation plan, and [roadmap/roadmap-canvas.md](roadmap/roadmap-canvas.md) and [roadmap/roadmap-openfig.md](roadmap/roadmap-openfig.md) for those feature streams.

The canonical implementation plan lives under roadmap/. Current operating documentation remains under docs/. Do not recreate removed planning or draft files or copy future behavior into current docs before it ships. Keep human prose brief, factual and present-tense; one fact has one owner.

## Target and retained behavior

Three binary targets build from one repository: `pixie_assistant` (assistant host), `pixie_web` (Go controller and UI) and `pixie_cli` (assistant plus a bundled Pi). `pixie_assistant` is a Bun host that runs Pi sessions in-process through the operator's installed Pi SDK, resolved from that installation at runtime and never bundled. It replaces the Go host's `pi --mode rpc` child processes and the separate Bun administration bridge; there is no Pi RPC child model and no bridge sidecar. `pixie_web` consumes a narrow authenticated loopback host event protocol because the two binaries are separate processes; that protocol is not a Pi execution fallback. `cli/` is a build flavor of the assistant, not a separate supervisor.

`pixie_web` runs as the Docker controller container or a local process. Docker runs controller-only mode and never starts local Pi.

Pi owns execution, transcripts, native credentials/models/settings/tools/extensions and trust. Do not intercept tools, replace prompts, auto-trust projects, install native packages silently or implement another model/MCP policy. Retained web features need tested coverage; a TUI-only fallback is not parity.

Sharing an installation does not attach to an arbitrary running TUI. Use separate sessions or explicit idle handoff with the managed owner terminated. Preserve independent native IDs/branches, unknown source records, ambiguous dispatch and deletion authority.

## Workspace and modules

Use the six slots in roadmap/roadmap.md: primary rail/sidebar/view, independent secondary view/sidebar/rail. Primary areas are Chats, Archive, Schedules and Settings. Hide/Close/Archive/Delete/Stop are different actions. View lifetime never owns accepted agent execution.

Projects group sessions but native identity/cwd and filesystem admission are separate. Support ungrouped sessions without a hidden all-files project. Files/Git are read-only inspection: no IDE, Monaco, LSP, terminal or automatic worktree manager.

Mewa owns shared foundations, appearance and control semantics; Pixie owns composition/state. Retain correct adapters/integrity checks and one lifecycle owner per DOM region. Avoid a second generated visual system. Tailwind is forbidden: it is being removed from the UI, and new frontend work must not add Tailwind dependencies or utilities.

Native extensions, native MCP integration and workspace modules are separate. Browser is the first registered contribution and is a pointer to an operator-chosen external MCP endpoint. Canvas and Openfig are the last feature phases, disabled by default; basic chat does not depend on workers. No arbitrary remote frontend code or new MCP Apps framework.

## Trust and source

Native Pi intentionally has host-user authority. Untrusted browser pages, HTML and design files need the enforced worker boundary described in the stream plans: a same UID, filtered environment, read-only roots and host networking are not isolation, direct-host mode does not inherit container restrictions, and missing mandatory enforcement makes a module unavailable rather than unrestricted.

Source roots are `assistant/`, `web/` and `shared/`. `web/` owns the Go controller, UI, workspace, persistence, MCP publisher and browser-MCP pointer; `shared/` owns the protocol schema (`shared/schema/protocol-catalog.json`) and the generated Go (`shared/piprotocol`) and TypeScript (`shared/src`) catalogs; `assistant/` owns Pi interaction. Keep the three Go modules with a root `go.work` and exact local module replacement so `GOWORK=off` works. Do not reintroduce removed source roots or another module's internal imports.

Use lowercase kebab-case source/component/script filenames, conventional lowercase entrypoints and Go underscore/_test.go naming. Tests are split under `assistant/tests`, `web/tests` and `shared/tests`; assistant Go tests are currently colocated with the interim host and move as the Bun host lands. Production artifacts contain only required runtime/assets/notices, not source/tests/secrets or unused workers.

## Validation and commits

The project is in an experimental development phase. Write simple, effective and useful tests that cover observable behavior and realistic failure modes; do not rebuild an exhaustive evidence matrix. The release-evidence matrix under `web/scripts` is frozen, so do not add rows to it. Run narrow checks during development and cross-boundary gates before cutover.

Current root commands include lint/typecheck/test/build/check:deps/check:filenames/check:docs/Mewa validation; Go checks run per module under `web/`, `assistant/` and `shared/`. Documentation-only changes require link/path/consistency checks, not claims of runtime testing. Review dependencies, generated files, stale references and migrations before committing.

Make coherent outcome-specific commits. Do not amend/squash/rewrite history without request. Use Miłosz Kolber <143708325+miloszkolber@users.noreply.github.com> where author selection is supported.

Local implementation/tests/commits follow the requested task. Remote pushes/merges, upstream submissions, initial publication-policy enablement and live/native-state changes require separate authorization. After an explicitly approved continuous-release policy is enabled, eligible main commits publish under that policy; live deployment remains separate. Inspect indirect publication effects before pushing. This roadmap documentation is not authorization to deploy or release its implementation.
