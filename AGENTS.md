# Pixie

Pixie is a lightweight web workspace for the user's installed Pi.

## Read first

Read README.md and the current behavior in docs/architecture.md, docs/pi.md, docs/security.md, docs/deployment.md and docs/development.md relevant to the task. Use [roadmap/roadmap.md](roadmap/roadmap.md) as the central implementation plan, and [roadmap/roadmap-canvas.md](roadmap/roadmap-canvas.md) and [roadmap/roadmap-openfig.md](roadmap/roadmap-openfig.md) for those feature streams. [roadmap/roadmap-aux.md](roadmap/roadmap-aux.md) holds the implementable plan and gap analysis for additive, optional work taken from neighbouring open-source Pi interfaces; it never changes the target or trust model. [roadmap/roadmap-ui.md](roadmap/roadmap-ui.md) holds the web UI redesign stream plan and the six-slot shell grammar.

The canonical implementation plan lives under roadmap/. Current operating documentation remains under docs/. Do not recreate removed planning or draft files or copy future behavior into current docs before it ships. Keep human prose brief, factual and present-tense; one fact has one owner.

## Target and retained behavior

Two public Linux artifacts build from one repository: `pixie_web` (Go controller and UI) and `pixie` (bundled Bun, regular Pi TUI, Pi SDK and assistant host connector). `pixie_assistant` is an internal archive JS host (`libexec/pixie_assistant.js`, run by `runtime/bin/bun`), never a public product, unit or archive. Bare `pixie` is the native Pi command and TUI; `pixie serve --config ABS` starts the host. The TUI runs concurrently with the server against the same agent directory and never takes the owner lock; only `pixie serve` locks it. `pixie_web` stays controller-only. The Pi-bearing runtime is pinned Bun 1.4.0 and never Node; Pi is never resolved from a global installation. The Go host's `pi --mode rpc` child processes and separate Bun administration bridge are removed; there is no Pi RPC child model or bridge sidecar. `pixie_web` consumes the narrow authenticated loopback host event protocol; that protocol is not a Pi execution fallback.

Reducing the host to a Pi extension is rejected. An extension runs only inside a Pi process, cannot own a durable multi-session registry, provider/model/settings or MCP mutation, or deletion authority, and would expose the controller secret to every loaded extension. A dedicated owner-locked host process remains the only contract that satisfies availability and authority; an in-TUI extension may only ever be an optional additive surface that never owns sessions or the loopback endpoint.

Docker publishes only `pixie_web`: the controller-only image connects to a separately installed `pixie` host. The controller-only image never starts Pi and contains no Bun, Node or Pi package.

Pi owns execution, transcripts, native credentials/models/settings/tools/extensions and trust. Do not intercept tools, replace prompts, auto-trust projects, install native packages silently or implement another model/MCP policy. The bundled native TUI is a first-class Pi interface, not a substitute for tested web behavior.

Sharing an installation does not attach to an arbitrary running TUI. Use separate sessions or explicit idle handoff with the managed owner terminated. Preserve independent native IDs/branches, unknown source records, ambiguous dispatch and deletion authority.

## Workspace and modules

Use the six slots in [roadmap/roadmap-ui.md](roadmap/roadmap-ui.md): primary rail/sidebar/view, independent secondary view/sidebar/rail. Primary areas are Chats, Archive, Schedules and Settings. Hide/Close/Archive/Delete/Stop are different actions. View lifetime never owns accepted agent execution.

Projects group sessions but native identity/cwd and filesystem admission are separate. Support ungrouped sessions without a hidden all-files project. Files/Git are read-only inspection: no IDE, Monaco, LSP, terminal or automatic worktree manager.

Mewa owns shared foundations, appearance and control semantics; Pixie owns composition/state. Retain correct adapters/integrity checks and one lifecycle owner per DOM region. Avoid a second generated visual system. Tailwind is forbidden: it is being removed from the UI, and new frontend work must not add Tailwind dependencies or utilities.

Native extensions, native MCP integration and workspace modules are separate. Browser is the first registered contribution and is a pointer to an operator-chosen external MCP endpoint. Canvas and Openfig are the last feature phases, disabled by default; basic chat does not depend on workers. No arbitrary remote frontend code or new MCP Apps framework.

## Trust and source

Native Pi intentionally has host-user authority. Untrusted browser pages, HTML and design files need the enforced worker boundary described in the stream plans: a same UID, filtered environment, read-only roots and host networking are not isolation, direct-host mode does not inherit container restrictions, and missing mandatory enforcement makes a module unavailable rather than unrestricted.

The single source tree owns one Go module and one Bun package at the root. `cmd/pixie/` owns the native TUI/host launcher, `cmd/pixie-web/` owns the controller executable, and `cmd/internal/` holds launcher-only helpers. `internal/` owns the Go application packages; `schema/`, `piprotocol/` and `src/shared/` own the protocol schema and the generated Go and TypeScript catalogs; `src/assistant/` owns Pi interaction; `scripts/` owns build and gate tooling; `webui/` owns the frontend and Go UI embedding. Do not reintroduce removed source roots, nested modules, or another module's internal imports.

Use lowercase kebab-case source/component/script filenames, conventional lowercase entrypoints and Go underscore/_test.go naming. Prefer simple one- or two-word filenames; add a longer qualifier only when it disambiguates a distinct responsibility. Tests are split under `tests/` by area (`tests/assistant`, `tests/shared`, `tests/build`, `tests/go`, `tests/webui`). Keep private Go unit tests beside their implementation, cross-package Go tests under `tests/go`, and shared Go fixtures under `tests/internal` so production cannot import them. Production artifacts contain only required runtime/assets/notices, not source/tests/secrets or unused workers.

## Validation and commits

The project is in an experimental development phase. Write simple, effective and useful tests that cover observable behavior and realistic failure modes; do not rebuild an exhaustive evidence matrix. The release-evidence matrix under `scripts` is frozen, so do not add rows to it. Run narrow checks during development and cross-boundary gates before cutover.

Current root commands include lint/typecheck/test/build/check:deps/check:filenames/check:docs/Mewa validation; Go checks run against the single root module. Documentation-only changes require link/path/consistency checks, not claims of runtime testing. Review dependencies, generated files, stale references and migrations before committing.

Make coherent outcome-specific commits. Do not amend/squash/rewrite history without request. Use Miłosz Kolber <143708325+miloszkolber@users.noreply.github.com> where author selection is supported.

Local implementation/tests/commits follow the requested task. Remote pushes/merges, upstream submissions, initial publication-policy enablement and live/native-state changes require separate authorization. After an explicitly approved continuous-release policy is enabled, eligible main commits publish under that policy; live deployment remains separate. Inspect indirect publication effects before pushing. This roadmap documentation is not authorization to deploy or release its implementation.
