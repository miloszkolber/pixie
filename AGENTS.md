# Pixie

Pixie is a lightweight web workspace for Pi.

## Read first

Read README.md and the relevant docs/architecture.md, docs/pi.md, docs/security.md, docs/deployment.md and docs/development.md before changing their behavior. For roadmap implementation, start with [roadmap/README.md](roadmap/README.md) and [roadmap/execution.md](roadmap/execution.md), then the assigned numbered plan.

Current behavior is documented under docs/. The canonical implementation plan, review and status live under roadmap/. Runtime prompt text can remain beside its integration. Give each fact one home. Keep human documentation short, factual and present-tense; do not copy future behavior into it before implementation or add operator-history narratives.

## Implementation direction

The current assistant uses the pinned Pi SDK under Bun. Its replacement is a Go engine that launches the selected host Pi executable through native RPC. Every release must include two builds: pixie-assistant for a Dockerized interface, and a complete host pixie binary with the same assistant engine, controller and embedded web UI. Both target supported amd64/arm64 hosts. See roadmap/02-assistant-go.md and roadmap/07-build-release.md for shared implementation and both systemd paths.

Treat the existing SDK/runtime as implementation to migrate, not a permanent requirement. Do not build another agent loop, provider policy or Pi MCP client in Go. The all-in-one host path must not require a separate assistant installation/service; external-assistant Docker mode must not start Pi inside the image.

The workspace target is the six-slot layout in roadmap/03-workspace-ui.md. Primary and secondary selections are independent. Mewa owns shared visual foundations; Pixie owns composition and feature state. Canvas and Design are final optional stages, not prerequisites of core chat.

Preserve retained behavior and recovery safeguards until their tested replacement is ready. Classify dormant methods/UI hooks before changing them. Do not remove a feature or regression merely because it complicates the rewrite. Coordinate shared contracts and state through roadmap/execution.md.

## Pi and product boundaries

- Pi owns execution, transcripts, providers, credentials, models, settings, tools and extensions. Reuse native resources without a mandatory bundle, replacement prompts, tool interception or a new permission-management system.
- Sharing an installation is not live attachment to an arbitrary running TUI. Separate processes must not write one session concurrently; use distinct sessions or explicit idle handoff.
- A project has one admitted directory root and may contain zero, one or several Git repositories. Projects organize sessions; the target also supports flat/ungrouped presentation. Discovery is not filesystem admission.
- Keep Files/Git read-only: bounded tree, source/Markdown/image previews, repository status and readable diffs. No editor, Monaco, LSP, debugger, terminal or automatic worktree manager. Agents change files/Git through Pi.
- Preserve multi-image turns, native UI questions, goals/tasks, plans, defined-agent authoring and optional delegation, web access, local models, Signet and provider configuration through explicit capability dispositions.
- Pixie owns schedules and their durable ledger. Expose schedule navigation/details, not another scheduler, recipe framework or Automation settings subsystem.
- Native Pi extensions, native MCP connections and Pixie workspace modules are separate systems. Advertise supported complete operation sets. Optional failures remain local.

## Runtime and trust

The current controller and Browser publisher share package/go.mod and the default container image. The target additionally offers their direct-host composition with the shared assistant. Preserve explicit protocol/authentication boundaries in both; internal combined-mode credentials must not leak to native children or browsers.

Session-scoped objectives/questions/schedules and module calls must validate real authority, not a caller-provided project/session ID. Browser tools publish through the MCP module registry; optional Canvas and Design consume that same foundation. Do not install native skills/packages silently or place secrets in model-visible instructions.

Current Browser children share controller filesystem/UID and host networking in the supplied deployment. Environment filtering and directory conventions are not isolation. Direct-host mode does not inherit container restrictions. Untrusted Canvas/Design processing needs the enforced worker boundary in the roadmap; missing isolation cannot silently fall back to unrestricted execution.

Keep artifacts/frames/history bounded, private credentials protected, project reads revalidated and ambiguous dispatch explicit. Do not retry work automatically after an uncertain acceptance. API resource checks protect Pixie; they do not replace Pi's native tool policy.

## Source and engineering

Current source paths are assistant/ for the host integration and package/ for controller, modules, contracts, webui and integration tests. Shared Bun tooling/lock is at the repository root. The Go plan retains separate assistant/go.mod and package/go.mod, with a public assistant/host facade; do not introduce obsolete pi/ or pixie/ source roots.

Add abstractions only for retained behavior or accepted roadmap contracts. Keep dependencies, protocol fields, scripts, docs and generated artifacts aligned. Production images/archives contain only their required runtime/assets/licenses, not source, tests, compilers, unused runtimes or credentials. Optional worker dependencies are explicit, not silently bundled into assistant-only.

Use lowercase kebab-case source/test/component/script filenames. Conventional index.ts/main.ts remain unchanged. Types and component exports can be PascalCase. Go uses conventional lowercase/underscore names and _test.go. Preserve existing filename checks while adapting their intended scope.

## Verification

Run the narrowest relevant checks during development and cross-boundary tests before integration. Keep existing tests under package/tests; colocate new Go unit tests with assistant code where needed. Add regressions for persistence, concurrency, authorization, protocol, filesystem, fragile UI and resource boundaries, not copied constants or trivial forwarding.

Before committing, review stale imports, lock entries, contracts, generated files, scripts and docs. Root Bun checks include lint, typecheck, test, build, check:deps, check:filenames and Mewa validation. Current Go checks run from package/; add explicit assistant-module and both-release-variant checks when introduced. Do not claim an SDK-host fixture proves independently installed Pi compatibility.

Use real native/artifact/browser fixtures at release gates. Documentation-only edits require link/path/content checks, not claims of runtime execution. Report actual results separately from planned tests.

## Commits and approvals

Keep separate coherent commits with outcome-specific imperative subjects. Do not amend, squash or rewrite history without an explicit request. Use the canonical identity Miłosz Kolber <143708325+miloszkolber@users.noreply.github.com> where the execution tool permits author selection.

An implementation request permits ordinary local coding, tests and commits. Remote pushes/merges, upstream submissions, tags/releases, package/image publication, release-workflow dispatch, live service changes and native-state relocation/deletion need separate authorization. A push approval must cover publication it triggers. Follow roadmap/execution.md; the documentation commit installing the roadmap is not approval to deploy or publish its implementation.
