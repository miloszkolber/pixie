# Pixie

Pixie is a focused Web UI for Pi.

Read these files before changing the product:

1. `README.md` defines the product surface.
2. `docs/architecture.md` defines process, source and state boundaries.
3. `docs/pi.md` defines the native Pi SDK and host protocol.
4. `docs/security.md` defines authority and credential boundaries.
5. `docs/deployment.md` defines host Pi and container deployment.
6. `docs/development.md` defines verification and performance checks.
7. `docs/roadmap.md` contains candidate future improvements.

Product and engineering documentation belongs under `docs/`. Runtime prompt text may remain beside the small integration that owns it.

Keep documentation short and present-tense: describe the product and direct setup. Give each fact one home and link to it. Omit migration narratives and historical negatives; put proposed work in `docs/roadmap.md`.

## Maintenance priority

Preserve the focused current baseline. Keep features, dependencies, tests, protocol methods, state, and documents aligned with it. Prefer a focused vertical slice over a generic workbench. Record accepted future improvements in `docs/roadmap.md`.

## Pi boundary

- Use the unmodified pinned Pi SDK through the host service. Keep the application and MCP host in separate containers.
- Keep Pi authoritative for transcripts, providers, credentials, models, core tools and native settings.
- Run the unmodified SDK from source with Bun. Do not add standalone Pi binary packaging.
- Optional extensions add capabilities. Do not introduce permission management, tool interception, replacement system prompts or restrictive execution modes.
- Detect supported capability versions and complete operation sets before exposing optional features.
- Retain Pixie's project ownership, durable queues, schedules, goals/tasks, questions, Browser isolation and transcript presentation.

- Pixie owns schedules and their execution ledger. Keep recipes and the Automation settings screen out of the product.

## Product boundary

- A project has exactly one admitted directory root. It may contain zero, one, or several Git repositories.
- Group persistent Pi sessions by project, not by one required Git repository or worktree.
- Keep Git observational in the UI: discovery, branch/HEAD, status, changed files, and readable diffs. Agents change Git through Pi tools.
- Keep a bounded read-only file tree and Shiki source preview. Do not add editing, Monaco, LSP, debugger, or collaborative IDE behavior.
- Keep goals/tasks, defined agents and delegation, multi-image turns, bounded browser QA, web access, optional Signet, and Pi provider configuration.
- Keep the Web UI focused. Do not add a TUI or Web UI terminal.

## Runtime boundaries

- Pi runs on the host as the user's authenticated loopback service. The default deployment uses two host-networked containers: the application and the `pixie-mcp` host, which embeds the Browser module from the same Go module.
- Objective updates use session-scoped MCP on the application listener. Browser tools and essential instructions use the MCP host's `/browser` module; it publishes a catalog at `/v1/mcp/modules` and keeps Browser HTTP/artifact compatibility routes available. Both MCP surfaces can serve trusted external host-network services.
- Pixie configures discovered MCP modules through Pi administration. The Browser connection is named `pixie-browser`. The universal Pi MCP client extension must not assume this service or any deployment address. Do not install host skills or put secrets in model-visible instructions.
- The browser has its own state mount, without project, application-state or Pi-configuration mounts. Its sessions still share one UID and filesystem; host networking is not network isolation.

## Engineering approach

- Keep the documented behavior intact. Add an abstraction only when retained behavior requires it.
- Keep frontend contracts as small as the documented surface permits.
- Classify dormant protocol methods and UI hooks before changing them. Wire retained functionality; do not remove it merely as cleanup.
- Keep final Docker images non-root and read-only, with no source, tests, compilers, caches or unused native runtimes. The application is assembled without a shell or package manager; the browser retains the slim runtime Chromium needs.

## Source naming

- Source directories and source, test, component, and script filenames use lowercase kebab-case.
- Conventional lowercase entry and barrel names such as `index.ts` and `main.ts` remain unchanged. Svelte components, types, and exports may use PascalCase inside files.
- Go files use conventional lowercase names, including underscores and the required `_test.go` suffix.
- `bun run check:filenames` enforces this convention across the application.

## Verification

- Run the narrowest relevant check during development.
- Keep all tests under `pixie/tests/`.
- Add small regression tests for observable contracts and realistic failure modes at persistence, concurrency, authorization, protocol, filesystem, performance and fragile UI boundaries.
- Do not test copied types, constants, trivial forwarding or implementation details.
- Use broader integration/image checks for changes crossing Pi, browser, or persistence boundaries.
- Before committing, review stale imports, protocol fields, scripts, documentation, generated files, and dependency-lock entries.

## Git history

- Keep changes in separate, coherent commits. Do not squash or amend commits, or rewrite existing history, unless the user explicitly asks.
- Write outcome-specific imperative subjects. Avoid vague `update`, `cleanup`, or `fixes` subjects and commits that mix unrelated work.
- Use the canonical identity `Miłosz Kolber <143708325+miloszkolber@users.noreply.github.com>`.

## Current stack

The controller and embedded Browser module share one Go module, with separate application and MCP-host executables and images. The frontend uses TypeScript and Svelte 5, a small framework-neutral external store, Mewa UI foundations pinned from GitHub Releases, and Bun for compilation, development, and tests. The controller uses a small native Pi event adapter and the pinned Coder WebSocket library. The host service uses the pinned Pi SDK and Bun. Treat these as current implementation choices, not permanent product scope.
