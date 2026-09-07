# Roadmap

## Product direction

Pi is the center of gravity. Pixie is a powerful web interface and a small application layer around it, not another agent runtime. Plug-and-play means that a user can connect Pixie to a clean or existing Pi environment with minimal setup and immediately use that environment's native behavior. It does not require the server component to be a Pi extension.

- Pi owns execution, native session files, tools, providers, credentials, models, thinking, extension loading, and native settings.
- Pixie owns projects, read-only file presentation, session/UI metadata, durable queues, schedules and their ledger, goals/tasks, Browser publication, and web presentation of native events.
- The bridge is invisible to the model: no replacement prompts, tool interception, new model tools merely for transport, or changes to an extension's normal results. Pixie-owned application tools remain explicit optional functionality, not prerequisites for connecting the UI.
- User-installed packages, local file extensions, and built-ins are discoverable without package-name conditionals. Bespoke tool cards are optional frontend enhancements, never conditions for backend execution.
- Generic `ExtensionUIContext` requests and native tool updates are the compatibility boundary. Arbitrary terminal component factories are not automatically browser components.
- Use `pixie-assistant` consistently as the integration name. Docker stays the primary application deployment. Document non-Docker execution as a secondary path. No required checkout path or prescribed optional extension bundle.
- Reproducibility guides manifests, tests, packaging and documentation. It does not require an overlay installer, a second settings file, or forced configuration management.
- Do not preserve development-era aliases without a demonstrated consumer. Interactive MCP Apps and their patch APIs are out of scope.
- A small Bun SDK service, a native Pi extension, or a thin native-RPC launcher is acceptable. Choose the simplest supported implementation that preserves Pi behavior and gives transparent installation, operation and removal. Retaining a well-bounded SDK service is preferable to forcing it into an extension through private APIs.
- A Go pixie-assistant executable is also acceptable if it demonstrably simplifies installation or supervision. Go cannot directly execute Pi's JavaScript SDK: it would need the native Pi process or a JavaScript runtime. Count those dependencies and process boundaries honestly, and do not port Pi's runtime into Go merely to claim a single binary.
- Powerful application features should remain progressive additions: basic chat must not require Browser, Signet, SearXNG, subagents, todo, schedules or an MCP adapter. Missing optional integrations produce clear unavailable states, not startup failures or silent installation.

## Approval boundaries

All remote publication requires explicit user signoff. Preparing code, workflow changes, packages and local dry runs does not authorize pushing branches or tags, opening an upstream pull request, publishing packages or images, or dispatching a release workflow. An approved branch push can itself trigger publication in the current container workflow, so its effects must be included in the approval request.

The first automated release remains the final roadmap item. Obtain approval for the exact commit, package version, tag, destinations and workflow before triggering it. `@pixie_ai/pixie-assistant@0.1.0` was already manually published and cannot be overwritten. Do not assume the next version number is approved.

Host state relocation and Git history rewriting are separate operations. This document plans them, not authorizes their execution. No migration, history rewrite, push, tag or release is part of the current review. Leave the history squash until immediately before release, after implementation and validation settle.

## Implementation review

The current code has useful native Pi integration. Its standalone service shape is compatible with the vision, but its mandatory extension dependencies, hardcoded loading path and incomplete inventory are not the desired final experience. Earlier completed checklists are not evidence that arbitrary extension discovery, reload, native child configuration or clean-package parity work.

| Area | Current evidence | Planning implication |
| --- | --- | --- |
| Installation | `pi/host/package.json` exposes a Bun executable, not a `pi.extensions` entry. `server.ts` and `sessions.ts` create and own SDK sessions. | This service is a valid starting point. Simplify configuration and dependency ownership before considering another process architecture. |
| Optional packages | `server.ts` imports a fixed set of factories, while `DefaultResourceLoader` also loads normal native resources. Optional packages are direct host dependencies. | Move toward one native loading/configuration path, not more wrappers or mandatory dependencies. |
| Inventory | `capabilities.ts` reports private Pixie markers. The Signet marker can be present without its managed extension. `pi.config.extensions.*` currently administers MCP connections, not Pi packages. | Separate configured packages, actually loaded extensions and usable host interfaces. Do not rename MCP inventory to Extensions. |
| Authoring | `extensions/agents.ts` only manages native Markdown files and mentions. Upstream `pi-subagent` owns execution. Authoring discovery does not apply the same trust/override rules as execution. | Keep the editor as a Pixie feature, but distinguish an editable definition from an executable agent. |
| Questions | The transcript card sends `session.questionReply` with `toolCallId` and a whole answer envelope. The working dialog bridge uses `session.uiReply` with `requestId` and a scalar response. | Replace the stale interaction path with one generic presenter, not an endpoint rename or package-specific answer adapter. |
| Passive UI | Status/title/working/string-widget events exist. Widgets have no visual surface. Composer text and terminal components are not supported. | Add bounded web projections deliberately and label unsupported presentation honestly. |
| Reload | Pi exposes `AgentSession.reload()`, resource/package inventory and native settings APIs. Pixie has no coordinated reload path. SDK reload resets provider state and rebuilds extension runners. | A process restart is not necessarily required, but concurrent-session cleanup must be proven before exposing toggles. |
| Child sessions | The patched subagent runner selects Pi's public RPC entry. It does not inherit the parent's in-memory Pixie factory array. | Validate TUI/web/child configuration parity using native resources, not profile markers or a successful-looking model reply. |
| Local models | The CLI loads Pi's built-in llama extension. The embedded host instead uses an optional SDK deep import in `extensions/llama.ts`. | Keep working local support until the chosen native startup path supplies built-ins. Do not add another provider implementation. |
| Schedules | Project-scoped CRUD, run-now, stop and durable execution state already exist. No dedicated frontend exists. Browser request types omit the backend's optional mutation identity. | Build a focused UI on existing APIs and preserve retry semantics. |

Public SDK surfaces to evaluate include `DefaultPackageManager.listConfiguredPackages()` and `resolve()`, `session.resourceLoader.getExtensions()` with `SourceInfo` and load errors, `SettingsManager` global/project package and path settings, and `AgentSession.reload()`. Ordinary extension contexts do not expose the complete `AgentSession` or resource loader. That is an architectural constraint to test, not a reason to reach into private state.

## Complete implementation sequence

This sequence covers the complete roadmap, not just the first five items. Preserve their scope below, then complete reload, distribution, host-specific work, validation, history preparation and approved publication. Work in independently verifiable slices. Do not rewrite an existing working subsystem merely to fit the conceptual diagram.

### Preparation: prove the boundary, not an extension-shaped implementation

1. Keep the current Bun SDK service as the default candidate. Map session creation, resume, prompt, Stop, forks, inventory, settings, built-ins and reload to supported Pi APIs. Identify host-only semantics that differ from native Pi.
2. Compare native TUI, web and headless child sessions using equivalent configuration and one previously unknown extension. Compare loaded resources, providers, tool definitions and results, prompt modifications, and authoritative session files. Do not run concurrent writers against the same session file.
3. Assess a native extension or a thin native-RPC launcher only where it removes a demonstrated installation or lifecycle problem. The ordinary extension context's limited access to `AgentSession` is not a blocker to retaining the SDK service.
4. Record the minimal runtime dependency and process graph. A packaged Bun executable may simplify distribution but is not a fork of Pi. A Go launcher must justify itself against a Bun entrypoint plus systemd, including its JavaScript runtime requirement.
5. Define supported Pi version compatibility and what happens when the TUI and assistant resolve different Pi versions. Never silently update the user's global Pi or substitute different model/provider settings.
6. Prefer supported APIs and small generally useful upstream contributions over private monkey-patching. Do not publish any contribution without signoff.

Exit evidence: a short architecture decision, supported-operation matrix, smallest viable dependency graph, and a demonstrated native session plus arbitrary extension. No new packaging architecture is required if the current service can satisfy these checks more simply.

### 1. Simplify aliases and permanently name pixie-assistant

Native discovery contracts are defined here before the Extensions screen uses them. Independent frontend work can proceed concurrently once those contracts are stable.

- Rename the internal `pi/host` source directory to `pi/pixie-assistant`, updating imports, workspace paths, test paths, package metadata, workflow staging paths and documentation. Preserve native Pi terminology such as `PI_CODING_AGENT_DIR`; it is not an obsolete product name.
- Use one canonical MCP integration identity. Remove `adapter-evaluation` and redundant profile-name machinery rather than building an Extensions screen around them.
- No `pixie-overlay.json` reader exists in the inspected implementation. Do not invent compatibility for it. Check host-local files before deleting any actual user data.
- Classify each wrapper: native optional package, Pixie-owned authoring/API function, provider bootstrap or unnecessary marker. Remove marker-only package wrappers as native discovery and generic inventory replace them. Remove imports and required dependencies for optional packages when the native loading path is proven.
- Optional package installation and configuration use Pi's own mechanisms. Do not move the hardcoded factory table into a JSON manifest or auto-install a recommended bundle. A list of suggested packages belongs in optional documentation, not the execution path.
- Move agent-definition CRUD into the application-facing assistant API rather than presenting it as a second agent runtime. Preserve user/project files and show execution eligibility separately from editability.
- Remove the specific rpiv event forwarding if no retained UI needs it. Keep only documented package-neutral host contracts and narrowly justified optional service integrations.
- Where an optional integration genuinely needs a package-specific public API, isolate and document that adapter. It must not make the package mandatory or block normal generic rendering. Generic Browser server registration may require MCP adapter support, but basic Pi chat must not.

Acceptance: one MCP runtime, native baseline without optional packages, optional packages loaded through Pi once, no false-positive markers, no model-visible changes just from connecting Pixie, and no obsolete internal `pi-host` identity. Keep published `0.1.0` immutable. The running host unit rename is a separate deployment change.

### 2. Fix question presentation through the generic UI bridge

- Use one pending-request store and one response contract based on the originating session and request ID. Include run/generation checks where needed to reject late replies.
- Let the generic presenter appear as a modal or an active session card. Attach it to a tool only when generic runtime metadata establishes that relationship, otherwise keep it session-level.
- Historical question/tool cards are read-only recaps. Remove their dead whole-questionnaire submission path, `session.questionReply`, and unreachable controller question state once callers are migrated.
- Return the exact offered value for `select`, a boolean for `confirm`, text for `input`/`editor`, and native cancellation values on dismissal. Sequential requests implement questionnaires without rpiv-specific backend translation.

Acceptance: arbitrary extension fixtures exercising all primitives and sequential requests, double submit, stale response, two clients, reconnect, Stop, timeout, fork and session deletion. No unrelated session or tool may receive an answer.

### 3. Extend native UI presentation without recreating the TUI

- Keep the existing select/confirm/input/editor/notify bridge and improve accessibility, mobile layout and focus restoration.
- Expose keyed status and working state, with explicit removal and lifecycle cleanup. Add a small collapsible area for bounded string widgets with native ordering/placement where available.
- Separate extension presentation titles from user-owned saved chat names. Define collision behavior before changing persistent names.
- Evaluate editor-text and paste APIs only with an explicit policy for existing drafts and reconnects. Do not silently overwrite unsent user text.
- Clearly report unsupported custom TUI component factories. Support an extension's native fallback where it provides one, but do not claim every `custom()` call has a fallback or always represents presentation-only behavior.

Acceptance: an unknown extension renders all supported interfaces without adding its name to Pixie. Check unsafe markup, oversized/flooded updates, unsupported components, per-session isolation and state removal after reload/shutdown.

### 4. Add a project-scoped schedules interface

- Reuse the current scheduler and execution ledger. Add list/detail, create/edit, pause/resume, delete confirmation, run-now and stop controls.
- Show timezone, next occurrence, latest outcome, active execution and links to the corresponding Pi sessions. Display interrupted/error states rather than hiding them.
- Carry mutation identities through browser contracts for retryable writes. Use backend validation for cron and timing previews rather than a competing frontend scheduler.
- Reflect current non-overlap and missed-run behavior. Verify paused run-now semantics before documenting them. Make editing/deleting distinct from stopping a running execution.
- Keep the view project-scoped and explain that Pixie must be running for dispatch. Do not revive Automation/recipes, add workflow graphs or a second execution engine.

Acceptance: CRUD and run controls, duplicate network retries, reconnect, restart during execution, persistence failure, DST/timezone behavior, missing project roots and no duplicate dispatch.

### 5. Add a truthful native Extensions screen

- First deliver read-only inventory from native Pi, not `profiles.json`, hardcoded suggestions, tool-name guesses or MCP connection records.
- Distinguish configured packages/resources, loaded extensions in the selected session, and supported web interfaces. Include identity/version when known, local/package source, user/project scope, load errors and contributed tools/commands. Unknown or unavailable metadata stays unknown.
- Include native file extensions and built-ins, not only npm packages. Do not execute new extension code merely to inspect it, leak environment/config secrets, or convert discovery failure into an empty successful list.
- Link to Pi-owned management instructions while mutation is unavailable. Show configured-versus-loaded divergence accurately.
- After the reload gate below passes, add native setting-backed toggles. Do not ship switches that imply a runtime change when only configuration has changed.

Acceptance: unfamiliar package, local extension, built-in, user/project precedence, session-specific differences, missing package, malformed configuration, duplicate tool and load error. Detect actual Signet tools/extension load rather than an unconditional marker.

### 6. Safe native extension configuration and reload

- Add setting-backed extension controls only after read-only inventory and lifecycle tests work. Use native package/settings APIs and show the exact user/project scope being changed. Installation of extension code is an explicit operator action, not a side effect of connecting the UI.
- Prefer reopening the idle session over restarting the service: close and rebuild it from the same native file, which leaves other sessions' runners, providers and dialogs untouched (Pi's own `reload()` resets global provider state and is not used). A read-only preflight refuses when a configured package has no installed path, so activation cannot silently install code.
- Define a busy-session policy: defer changes, or request an explicit stop, rather than destroying work silently. Report saved configuration versus active configuration separately.
- Verify cleanup on disable, removal, failed initialization and repeated reload. A reload can execute arbitrary extension code, so configuration rollback alone does not guarantee runtime rollback.
- Use systemd restart as a documented operational fallback where necessary. Do not introduce a root helper merely to toggle an extension. Prevent child processes from recursively starting server listeners.

Acceptance: repeated reload without duplicate listeners/tools, accurate failure states, preserved user configuration and unrelated active sessions, no stranded dialogs, and honest deferred/restart-required status in the Extensions screen.

### 7. Complete native lifecycle and configuration parity

- Verify built-in providers, including llama.cpp, and native defaults through the selected startup path. Retain the current optional loader only until equivalent built-in loading is proven. Keep endpoint choices with native/provider configuration, not a second Pixie models database.
- Verify parent and child runs against the same native user/project extension configuration. Resolve the Bun child-launch patch for distributable installs: a root workspace patch does not travel automatically into an npm dependency installation. Do not claim packaged subagents work based on checkout-only tests.
- Add real compaction acceptance using a deliberately adequate native transcript and the actual SDK operation. Check todo state and extension context after compaction, restart and switching sessions. A small-session refusal or settled event is not equivalent evidence.
- Test retries, extension-injected continuations, queues and settled semantics with missed events/reconnect. Stop must settle supported running tools and pending UI, while a timeout must not masquerade as a successfully aborted run.
- Verify forks, return to the source, repeated source forks and resume of both children. Keep in-session branches distinct. Assess a small native branch-navigation surface only after the SDK semantics are verified, and keep it a separate UI slice rather than making fork stand in for branch support.
- Test native provider authentication and OAuth flows where supported, including cancellation, secure storage and failure reporting. Record production credential/keyring checks separately from fixture tests and never log secrets.
- Enforce native project trust at session construction: project-local resources load only with no trust-requiring resources or an explicit stored decision (default-deny without a prompt, matching Pi without an interactive UI). Untrusted project extensions never execute while user extensions load; the Extensions screen reports the trust state. Covered by a regression test that fails when the gate is removed.

Acceptance: evidence from real SDK session files and deterministic fixtures, plus targeted live checks. Compare behavior rather than requiring identical text from a nondeterministic model. No optional-package-name changes are needed for the unfamiliar-extension fixture.

### 8. Transparent packaging and deployment

- Make the default installation useful without a checkout or full web-development toolchain. The assistant package should require only its chosen runtime and essential Pi/transport dependencies. Native optional packages remain user-owned.
- Keep Docker primary for the Go application and its UI. Maintain a non-Docker path with configurable user-writable data/static locations, a built UI and any explicitly selected Browser prerequisites. A Go executable is not self-contained if its UI, Pi runtime or Browser assets still require separate delivery.
- Validate whether bundling the web assets into the Go application or shipping a binary-plus-assets archive reduces installation complexity. Select one supported non-Docker form rather than maintaining several speculative variants. Build artifacts in GitHub Actions only after the local package smoke works.
- Provide concise foreground and optional systemd instructions/templates with no fixed checkout path. Explain process ownership, bind address, credentials, logs, upgrade, rollback and removal. Do not install privileged services automatically.
- Test npm tarball contents, executable permissions, version reporting, dependency closure, SDK compatibility, optional-package absence and successful children in an isolated installation. Remove dependency-owned cache files accidentally included in patches or packages. Validate provenance/repository/license metadata.
- Make workflow dry runs non-publishing and validate tags before stamping a version. Account for automatic GHCR publication on branch pushes. Actual registry operations remain behind the final approval gate.

Acceptance: a fresh local installation can start basic chat using an existing Pi configuration without hidden setup, repository imports, hardcoded local paths, forced optional dependencies or unshipped workspace patches. Uninstalling Pixie leaves native Pi state intact.

### 9. Browser security and compatibility

- Preserve Browser as an optional Pixie-published module using agent-browser for automation. Keep module enablement in application state, and distinguish browser availability from basic application readiness.
- Review the deployed filesystem, UID, environment and network boundaries. Sanitized child environment variables and a read-only root filesystem are useful defenses, not isolation from controller files under a shared UID. Do not equate a successful screenshot with a sandbox test.
- Evaluate the smallest practical process/filesystem boundary for Chromium while keeping one Pixie control plane. Document deployment prerequisites and residual risks. Ask before accepting a material security regression or requiring new privileged machinery.
- Test Browser panels, leases, artifact access/cleanup, cancellation, untrusted pages and admitted project boundaries. Do not restore the removed interactive MCP Apps feature.
- Keep Chromium supported. Obscura stays experimental until tested through CDP against representative sites and the required navigation, snapshot/ref, interaction, screenshot and cleanup paths. A missing item in upstream documentation is not proof that an implementation is absent.
- Add a Browser engine selector only if multiple verified usable engines justify one. Otherwise keep the UI honest and small. Do not change the model-facing Browser API by engine.

Acceptance: verified compatibility and explicit security boundaries for the selected deployment. Publish measurements only for configurations actually exercised, not as universal engine claims.

### 10. Host-only configuration, operational validation and measurements

- Carry out the separately approved `/home/core/.pi` relocation described below once entrypoint configuration is stable, using the same state root for interactive Pi, pixie-assistant and native children. This is not a requirement for other users.
- Align the local service name with pixie-assistant during a controlled deployment. Keep this host's llama.cpp/Signet/SearXNG addresses and service configuration in operator-owned files, not universal setup instructions.
- Run local-model acceptance through the final deployment, including native optional extensions, Browser, schedules, reload and restart. Verify liveness, readiness and useful diagnostics without revealing credentials.
- Measure baseline versus optional-extension startup, memory, context overhead, cold/warm MCP calls and Browser task latency on supported Linux x86-64 and arm64 where available. Use bounded fixtures and record machine/runtime/version details. Unavailable platforms remain explicit unverified items.
- Remove duplicate state, unreachable protocol paths, stale wrappers, incorrect instructions and generated scratch artifacts exposed by this work. Bring `AGENTS.md`, README and focused docs into agreement with the final architecture. Keep roadmap entries as proposed work, not permanent completed checklists or test counts.

Acceptance: useful operational recovery, clean relevant validation, reproducible measurements and a short source-plus-deployment architecture review. Do not relax failure gates merely to declare completion.

### 11. Pre-release history cleanup

Leave the squash until implementation, packaging and validation settle. Apply the proposed boundary below only after explicit approval, with a protected backup and unchanged root/final trees. Review retained branches/tags and the canonical remote URL. A rewrite is local preparation, not permission for force-pushing. Revalidate the release candidate after commit identities change.

### 12. Remote publication and first automated release, parked last

Review the exact candidate tree and artifact, npm scope, version, tag, workflow permissions, OIDC trusted publisher and all publication side effects. Obtain explicit user approval before any remote push or workflow dispatch. Verify tag/version agreement, package contents, provenance, version output and fresh installation after publication. Do not republish existing `0.1.0` or silently choose a later version. Manual dispatch must not publish a fallback `0.0.0-dev` accidentally.

## Host-only proposal: use /home/core/.pi directly

The inspected SDK `getAgentDir()` honors `PI_CODING_AGENT_DIR` before its normal `~/.pi/agent` default. A supported host-only override is `PI_CODING_AGENT_DIR=/home/core/.pi`. This does not require changing Pi's universal default or the repository layout.

Read-only inspection found `/home/core/.pi` currently contains `agent/`, whose children include credentials, settings, models store, sessions, agents, extensions, bin and Pixie state. The service runs as `core` from `/repo/pixie` and reads `/home/core/docker/pixie/.pixie`. No files were moved.

Migration plan, after separate confirmation:

1. Confirm the override should apply to both interactive Pi and pixie-assistant on this host. Configure it consistently for the shell, systemd and child launches. Prefer one state root over divergent TUI/web configurations.
2. Enumerate readers and absolute paths in managed extensions, sessions, cached package metadata and service configuration without exposing credentials. Verify which references can survive relocation and which need supported repair.
3. Quiesce Pi sessions, scheduled work and assistant writers, then take a protected backup outside the destination. Build a collision-checked move list including hidden entries and preserve modes/ownership. The destination is the source's parent, so a blind recursive move is unsafe.
4. Move only the agent-directory contents into `/home/core/.pi`, set the environment override, and update this host's operator instructions. Keep universal documentation using Pi's default and an optional override example.
5. Verify TUI/web/child provider and tool inventories, auth use, session resume, Signet, native settings and absence of accidental writes to the old directory. Remove the empty old directory only after checks. Rollback restores both layout and entrypoint configuration with writers stopped.

Do not put credentials or runtime state into the repository. No permanent symlink or dual-read layer is planned unless a demonstrated external consumer requires one.

## Proposed history boundary

The inspected history has 116 commits reachable from `669955a` inclusive. Its parent is `d6efba5`. The proposed result is one new root commit with exactly the tree of `669955a`, followed by the intended later changes, preserving the final tree.

Git cannot retain the literal hash `669955a` after removing its parent: parent identity is part of a commit's hash. Descendant hashes also change. The old published npm artifact remains immutable, and rewriting a branch does not erase other branches, tags, remote objects or clones.

Before execution, confirm this snapshot boundary and which later branches/merges to retain. Create a recoverable bundle outside the repository, inventory refs and published references, perform the rewrite in an isolated local checkout, and verify the root tree and final tree byte-for-byte against the originals. Address temporary backup branches and the old `gooseberry.git` origin URL deliberately, not as incidental cleanup. No history rewrite or force-push is authorized by this plan. Leave this operation for the pre-release stage and request a separate signoff for the remote update.

## Final architecture review gate

Before publication, verify from current source and fresh tests:

- A clean or existing Pi installation can use pixie-assistant with minimal setup through supported APIs, whether the chosen bridge is a service, extension or thin launcher. Its packaging and process model are explicit.
- TUI, web and child sessions use the same native resources and settings without package-specific factories changing their core capabilities.
- Pi's session files and lifecycle remain authoritative. Forks remain independent sessions, while in-session branches retain native semantics and unsupported navigation is labelled honestly.
- Generic UI works for unfamiliar extensions, reconnect restores pending work, and Stop unwinds both dialogs and generation without orphan state.
- Native extension inventory is distinct from MCP connections and Pixie's published Browser module catalog.
- Pixie authoring, schedules and read-only project presentation remain application features, without becoming an orchestration framework.
- No interactive MCP Apps, speculative compatibility aliases, duplicate state machines, hidden provider configuration or forced overlay setup return.
- Security claims match the deployed process model. In particular, shared-UID Chromium with `--no-sandbox` is not isolated from controller files merely because child environment variables are sanitized.
- Documentation and repository guidance agree with actual architecture. Resolve stale `AGENTS.md` claims about separate containers, executable packaging and shell-free images during the owning implementation changes.

Current behavior belongs in [architecture](architecture.md), [Pi integration](pi.md), [extension UI](pi-extensions.md), [security](security.md), [deployment](deployment.md) and [development](development.md). This roadmap records proposed work and approval gates rather than completed migration narratives or permanent test counts.
