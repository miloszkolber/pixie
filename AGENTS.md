# Pixie

Pixie is a lightweight web workspace for the user's installed Pi.

## Read first

Read README.md and the current behavior in docs/architecture.md, docs/pi.md, docs/security.md, docs/deployment.md and docs/development.md relevant to the task. For roadmap implementation, start at [roadmap/README.md](roadmap/README.md), [roadmap/execution.md](roadmap/execution.md), [roadmap/contracts.md](roadmap/contracts.md) and [roadmap/feature-coverage.md](roadmap/feature-coverage.md).

The canonical implementation plan and review evidence live only under roadmap/. Current operating documentation remains under docs/. Do not recreate the removed roadmap/MCP draft files or copy future behavior into current docs before it ships. Keep human prose brief, factual and present-tense; one fact has one owner.

## Target and retained behavior

The current assistant uses a pinned SDK under Bun. The target is a shared Go engine supervising selected host Pi through native RPC, used by assistant-only and full-host builds. Core chat needs no native extension. The optional administration bridge must use the selected installation's public APIs and stay explicitly opt-in, not another SDK/agent daemon.

Every release includes pixie-assistant and the complete pixie host binary on amd64/arm64, plus the matching Docker controller image. One source commit produces one sha-<12> identity across Git tag, GitHub Release, archive names, binary metadata and Docker tag; full SHA/digests stay in the manifest. Follow roadmap/builds-and-releases.md. Do not create semantic-version or workflow-counter naming alongside it.

The full-host build contains assistant/controller/UI in one executable/service. Docker explicitly runs controller-only mode and never starts local Pi. Both share implementation, authority and state contracts, not duplicated supervisors.

Pi owns execution, transcripts, native credentials/models/settings/tools/extensions and trust. Do not intercept tools, replace prompts, auto-trust projects, install native packages silently or implement another model/MCP policy. Retained web features need tested coverage; a TUI-only fallback is not parity. Keep legacy selection until coverage gates or explicitly approved reductions permit removal.

Sharing an installation does not attach to an arbitrary running TUI. Use separate sessions or explicit idle handoff with the managed owner terminated. Preserve independent native IDs/branches, unknown source records, ambiguous dispatch and deletion authority.

## Workspace and modules

Use the six slots in roadmap/workspace-ui.md: primary rail/sidebar/view, independent secondary view/sidebar/rail. Primary areas are Chats, Archive, Schedules and Settings. Hide/Close/Archive/Delete/Stop are different actions. View lifetime never owns accepted agent execution.

Projects group sessions but native identity/cwd and filesystem admission are separate. Support ungrouped sessions without a hidden all-files project. Files/Git are read-only inspection: no IDE, Monaco, LSP, terminal or automatic worktree manager.

Mewa owns shared foundations, appearance and control semantics; Pixie owns composition/state. Retain correct adapters/integrity checks and one lifecycle owner per DOM region. Avoid a second generated visual system.

Native extensions, native MCP integration and workspace modules are separate. Browser is the first registered contribution. Canvas and Openfig are the last feature phases, disabled by default; basic chat does not depend on workers. No arbitrary remote frontend code or new MCP Apps framework.

## Trust and source

Native Pi intentionally has host-user authority. Untrusted browser pages/HTML/design files need the optional enforced worker boundary in roadmap/contracts.md. Same UID, filtered environment, read-only roots and host networking are not isolation. Direct-host mode does not inherit container restrictions. Missing mandatory enforcement makes the module unavailable, never unrestricted.

Source paths are assistant/ and package/; shared Bun tooling is at root. Keep the separate Go modules with an assistant/host facade and exact local module replacement. Do not introduce obsolete pi/ or pixie/ source roots or another module's internal imports. Shared schema ownership is package/contracts.

Use lowercase kebab-case source/component/script filenames, conventional lowercase entrypoints and Go underscore/_test.go naming. Existing integration tests stay under package/tests; new Go unit tests may be colocated. Production artifacts contain only required runtime/assets/notices, not source/tests/secrets or unused workers.

## Validation and commits

Run narrow checks during development and cross-boundary gates before cutover. Preserve useful regressions. Test actual independent Pi distributions, native bridge APIs, production UI, both real binary modes, systemd and both OCI platforms. Mocks/compiler success do not establish compatibility or feature parity.

Current root commands include lint/typecheck/test/build/check:deps/check:filenames/Mewa validation; current Go checks run under package/. Add explicit assistant-module and dual-build tests as implementation lands. Documentation-only changes require link/path/consistency checks, not claims of runtime testing. Review dependencies, generated files, stale references and migrations before committing.

Make coherent outcome-specific commits. Do not amend/squash/rewrite history without request. Use Miłosz Kolber <143708325+miloszkolber@users.noreply.github.com> where author selection is supported.

Local implementation/tests/commits follow the requested task. Remote pushes/merges, upstream submissions, initial publication-policy enablement and live/native-state changes require separate authorization. After an explicitly approved continuous-release policy is enabled, eligible main commits publish under that policy; live deployment remains separate. Inspect indirect publication effects before pushing. This roadmap documentation is not authorization to deploy or release its implementation.
