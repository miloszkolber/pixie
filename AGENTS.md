# Pixie

Pixie is a lightweight web workspace for Pi.

## Read first

Read README.md and the relevant current behavior in docs/architecture.md, docs/pi.md, docs/pi-extensions.md, docs/security.md, docs/deployment.md and docs/development.md. For implementation, start with [roadmap/README.md](roadmap/README.md), [execution](roadmap/execution.md), [contracts](roadmap/contracts.md) and [compatibility](roadmap/compatibility.md), then the assigned plan.

Current behavior belongs in docs/. Accepted future implementation, source review, tasks and evidence belong in roadmap/. Do not recreate docs/roadmap.md or the superseded MCP drafts. Keep documentation short, factual and human-readable; each fact has one home. Comments explain non-obvious constraints, not obvious code. Omit operator-history narratives and do not describe unimplemented features as available.

## Direction and boundaries

The current assistant uses the pinned Pi SDK under Bun. The target is one shared Go assistant engine that launches the selected host Pi through native RPC. It ships as pixie-assistant for the Dockerized interface and inside one full-host pixie binary with controller and embedded UI. The full-host path has one service, not a separately installed assistant. Docker explicitly runs controller-only/external-assistant mode and never launches Pi inside the image.

Every new release after cutover includes both host variants on amd64/arm64 and the matching multi-platform Docker image. GitHub Release/tag, binary version, archive and GHCR tag use the same sha-12 commit identity defined in roadmap/builds-and-releases.md. Full revision/digest checks remain mandatory. No publication from ordinary PR/main/scheduled validation, no partial release, no overwriting prior artifacts.

Pi owns execution, transcripts, providers, credentials, models, settings, native trust, tools and extensions. No replacement agent loop/provider policy, mandatory extension bundle, hidden SDK, tool interception or second Pi MCP client. Retained administration uses supported installed APIs or the explicitly enabled generic bridge. No feature reduction is approved by the Go rewrite; a TUI workaround alone does not complete retained Web UI behavior. Follow all CP rows in roadmap/compatibility.md.

Sharing a Pi installation is not attachment to an arbitrary running TUI. Separate processes must not write one session concurrently. Use separate sessions or explicit idle handoff. Both binaries share scoped installation/session ownership; do not kill independent user processes.

The six-slot workspace has independent left/right selections and visibility. Mewa owns appearance, tokens and control semantics; Pixie owns routes, composition and feature state. Hiding/unmounting a view does not stop an accepted run. Close, archive, delete and Stop are distinct. Project grouping is optional for native session identity; filesystem admission is separate.

Files/Git remain bounded read-only inspection. A project has one admitted root and may contain zero or several repositories. Do not add a terminal, editor, Monaco, LSP, debugger or automatic worktrees. Preserve images, search, goals/tasks/questions, schedules, plans, defined-agent editing and optional delegation/web/local models/Signet through the compatibility contract.

Pixie owns schedules and their durable ledger. Default Stop follows roadmap/contracts.md; uncertain delivery never automatically resends. Native extensions, native MCP connections and workspace modules remain separate. Canvas then Openfig are final optional stages through the same registry, not prerequisites for core chat.

## Trust and state

Authenticate service/context before expensive work; a caller-supplied session/project/resource ID is not authority. Keep service credentials out of model instructions, native stdout, URLs, argv and unredacted logs. Generic bridge IPC is a private allowlisted control channel, not an arbitrary code runner.

Pi intentionally uses its host user's tool authority. Browser's current shared UID/filesystem and host networking are not isolation. Direct-host mode inherits no Docker restrictions. Untrusted Canvas/Design workers need enforced filesystem, egress and resource boundaries; missing enforcement must not fall back to unrestricted execution. Module failures stay local; retained-document status/removal remains possible without the worker.

Follow roadmap/migration.md for exact state inventory, backups/checkpoints, ownership changes and rollback. Pixie-owned sidecars under agentDir are distinct from native state. Do not rewrite Pi JSONL/settings/auth during migration or restore an old execution/deletion ledger over post-backup effects. Unknown files/records survive; corrupt authority fails closed rather than using stale backups.

## Source and tests

Current roots: assistant/ for host integration; package/ for controller, modules, contracts, webui and integration tests; root Bun tooling/lock. Go target retains assistant/go.mod and package/go.mod with public assistant/host composition. Do not introduce obsolete pi/ or nested pixie/ roots or import another module's internal packages.

Use lowercase kebab-case source/component/script filenames, conventional index.ts/main.ts, and conventional lowercase/underscore Go names with _test.go. Types/exports can be PascalCase. Preserve filename/integrity checks. Colocate new Go unit tests where appropriate; retain existing package/tests integrations.

A owns shared schemas, C workspace state, B native I/O/lifecycle, E primitive semantics/registry and G packaging. Coordinate shared lockfile/Dockerfile/CSS/registry edits. Prefer narrow tested slices over new frameworks. Preserve working safeguards and classify dormant protocol/UI hooks before removal.

Run relevant checks during development and cross-boundary checks before integration. Current root Bun commands include lint, typecheck, test, build, check:deps, check:filenames and mewa:check. Current Go checks run from package/; add explicit assistant-module and both-variant checks when introduced. Runtime compatibility needs independently installed Pi and final artifact evidence, not only SDK mocks. Documentation edits require link/path/diff checks, not invented runtime test results.

## Commits and authorization

Keep coherent outcome-specific commits. Do not amend, squash or rewrite history without an explicit request. Use Miłosz Kolber <143708325+miloszkolber@users.noreply.github.com> where the tool supports author selection.

Local coding/tests/commits proceed under an implementation request. Remote pushes/merges, upstream submissions, tags/releases/package/image publication, release-workflow dispatch, live-service changes and native-state relocation/deletion need separate authorization. Push approval covers publication it triggers. A roadmap documentation commit does not authorize deployment of its implementation.
