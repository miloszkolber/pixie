# Repository review

Baseline: `miloszkolber/pixie` at `f63d0d5bcb7f6e342058734b2e741ad2619d8867`. Review date: 8 September 2026. [Sources](sources.md) contain pinned repository references and external contracts.

## Verification scope

The review covered assistant entrypoints, session implementation and transport; controller connection and durable queues; MCP registry; workspace shell/state; Mewa integration; representative tests; deployment, documentation and workflows. It was not an independent line-by-line audit of every file.

GitHub Actions run `34226327666` reported success for source validation, browser acceptance, native SDK-host compatibility on amd64/arm64 and image publication. Those results were inspected, not rerun in this review. The supplied blank-content screenshot was not reproduced. Security findings below identify exposure, not demonstrated exploitation. The Openfig draft's timing and renderer experiments are inherited research, not new test executions.

P1 means resolve or explicitly accept before the relevant cutover; P2 means foundation work. These are planning priorities, not vulnerability scores.

## Retain

Keep the Go controller boundary, native session identity/persistence, bounded transport, reconnect generations, pending tools/dialogs, explicit uncertain delivery, scoped authorization, read-only project inspection, Mewa integrity checks and existing acceptance coverage. The browser fixture already tests streaming after view closure, transcript deduplication, keyboard/narrow layouts and Mewa cleanup. Rewrite owners, not all working components.

## Findings

### F01 — Host-installed Pi is not the runtime authority

**P1; confirmed architecture mismatch.** assistant/package.json installs pinned SDK dependencies, sessions.ts creates SDK sessions, and server.ts reports that embedded SDK version. Sharing the user's state directory does not select the user's executable. SDK embedding is not inherently wrong, but conflicts with the requested installation-parity model. Launch the selected Pi through native RPC and test outside the checkout. [Assistant sources](sources.md#assistant-and-pi).

### F02 — Production restart has no termination callback

**P1; confirmed source defect.** runtime.restart closes peers and calls optional options.onRestart. main.ts does not supply that callback and the server supplies no default despite its comment. The injected-callback test does not cover the production entrypoint. The request can succeed without ending the process, leaving restartPending set. Share bounded shutdown/exit with ordinary termination and test the executable and new boot identity. [Assistant sources](sources.md#assistant-and-pi).

### F03 — Failed module persistence changes effective state

**P1; confirmed source defect.** Registry.SetEnabled changes r.enabled before persist.Write. Failure leaves memory, disk and runtime inconsistent; routes and health read the changed map. Persist a candidate map before publishing committed configuration, and keep runtime readiness distinct. Exercise write failure and restart. [Registry source](sources.md#modules-and-deployment).

### F04 — Duplicate enable restarts Browser

**P1; confirmed source defect.** SetEnabled always invokes startLocked; the enabled path constructs a replacement and shuts down the existing Browser. Add an unchanged-value no-op and a separate explicit Restart operation. Verify the same active Browser handle survives a retried enable. Test shared-storage lifecycle overlap. [Registry source](sources.md#modules-and-deployment).

### F05 — Method reference and validation are not an exact contract

**P1; confirmed gap.** docs/pi-protocol.md mixes operation names that do not match host dispatchers: implemented examples include session.cancel, pi.session.steer and session.configure. Number(request.id) also accepts some strings/booleans before integer validation. Inventory the actual callers/dispatchers, separate controller and host surfaces, and validate typed envelopes without coercion. Preserve unknown native payload fields independently of envelope strictness. [Assistant sources](sources.md#assistant-and-pi).

### F06 — One content slot mixes left and right navigation

**P1; confirmed target gap.** project-work-area.svelte selects one ContentTab for chat, file, diff or Browser. Files replace the conversation; the sidebar is hard-coded to Files/Changes. Replace this ownership model with independent primary/secondary selections and six slots. Styling the tab strip does not implement the target. [Workspace sources](sources.md#workspace-and-mewa).

### F07 — Duplicate create and the empty screenshot need separate tests

**P1; missing local guard confirmed; screenshot cause unverified.** The view create handler has no local in-flight guard. Repeated activation can issue multiple creates. However, openChatSession normally activates the returned session. Do not claim the screenshot proves activation is absent or that every repeated Chat title is accidental. Add a guarded action; capture route, IDs, persisted-state version and request generation to reproduce restoration/stale-selection failures. A selected item must resolve to its own loading/content/error/missing state. [Workspace sources](sources.md#workspace-and-mewa).

### F08 — Two visual foundations coexist

**P1; confirmed ownership gap.** Mewa is loaded alongside Pixie's independent palette, generated colors, generated typography and structural tokens. Pixie describes rounded defaults and its own spacing scale; Mewa specifies square geometry and library primitives. Correct Button mappings already exist. No computed-style audit was run, so do not invent a specific cascade failure. Retain correct wrappers/integrity checks, migrate foundations to Mewa and test composed browser styles. [Workspace sources](sources.md#workspace-and-mewa).

### F09 — Provider/connection state replaces the workspace

**P2; confirmed UX gap.** shell.svelte gates the workspace through global availability and launches Settings as a modal. Keep navigation, safe readable content and recovery reachable; disable unsupported operations locally. Move Settings to the primary area. Distinguish stale cached data from authoritative current state. [Workspace sources](sources.md#workspace-and-mewa).

### F10 — UI translation omits native editor text

**P2; confirmed parity gap.** The custom bridge marks composer APIs unsupported while native RPC documents set_editor_text. Preserve working dialogs/widgets and add session-owned editor proposals with newer-draft conflict handling. Native confirmation uses confirmed, not the current bridge's generic value shape. Arbitrary TUI factories remain unsupported rather than fabricated as HTML. [Assistant sources](sources.md#assistant-and-pi).

### F11 — The registry remains Browser-specific

**P2; confirmed target gap.** Validation, health, lifecycle, catalog and routes accept only Browser, and UI paths hard-code it. Introduce a bounded compile-time registry plus frontend contribution contract. Browser, Canvas and Design must share it; a simple MCP connection is not automatically a rich UI. No arbitrary remote code or marketplace is required. [Registry source](sources.md#modules-and-deployment).

### F12 — Browser shares controller authority

**P1; confirmed exposure, no exploit demonstrated.** The documented Chromium configuration disables its sandbox; Browser/controller share UID 1000; Compose uses host networking; Browser state sits beneath controller data. Sanitized environment/HOME/TMP values do not enforce file or network isolation. Preserve artifact checks and credential filtering, state limitations accurately, and test a stronger optional deployment before claiming containment. Pi's intentional host-tool authority is a different boundary. [Deployment sources](sources.md#modules-and-deployment).

### F13 — Hardening claims exceed supplied Compose flags

**P2; confirmed mismatch.** Compose sets UID, read-only root and tmpfs, but omits capability-drop, no-new-privileges, memory and PID limits mentioned around the deployment. Add/test intended flags with realistic Browser budgets or remove the claim. Do not apply Browser restrictions blindly to host Pi. [Deployment sources](sources.md#modules-and-deployment).

### F14 — Publication is coupled to routine validation

**P1; confirmed workflow behavior; external protections not audited.** Container builds publish on non-PR events, including main pushes and schedule runs, and can promote latest. Separate CI artifacts from approved publication. Enforce the agreed approval policy in workflows rather than relying solely on prose. An assistant binary release must not inherit automatic publication accidentally. [Workflow sources](sources.md#documentation-and-validation).

### F15 — SDK-host CI is not host-executable compatibility

**P1; confirmed test-scope gap.** Current official-Pi jobs install the workspace and exercise its native SDK host on two architectures. Retain them, but add released-artifact tests against independent installations, service PATH/HOME, custom directories, absent extensions and incompatible versions. Existing success is not proof of arbitrary Pi compatibility. [Workflow sources](sources.md#documentation-and-validation).

### F16 — History is eagerly materialized before chunking

**P2; confirmed optimization opportunity, not measured failure.** Large histories can be chunked, but are assembled and serialized in full first to determine size. Use bounded iteration/encoding and test single oversized entries as well as many messages. Preserve snapshot ordering. Measure simultaneous attaches and image-heavy histories, including Pi child memory. [Assistant sources](sources.md#assistant-and-pi).

### F17 — Durable delivery needs one owner

**P1; migration concern, not proof of current duplicate execution.** The controller has durable follow-ups and delivery-uncertain state; native RPC also provides queues. A naive adapter can enqueue twice or settle on acceptance. Define each delivery stage, preserve uncertainty after dispatch without acknowledgment, and test Stop semantics. Do not promise exactly-once tool execution across crashes. [Queue and RPC sources](sources.md#assistant-and-pi).

### F18 — Documentation contains obsolete paths and operator history

**P2; confirmed defects.** Agent instructions refer to absent pi/ and pixie/ source roots. Deployment includes machine paths/backups, a Browser mount absent from Compose, and --build for a service without build configuration. Security's benchmark passes an entire dotenv file as a bearer and describes interactive App HTML despite roadmap exclusions. Replace these with current, tested instructions. Keep audit evidence here rather than in quick-start prose. [Documentation sources](sources.md#documentation-and-validation).

### F19 — npm pack checks reference absent package paths

**P1 for a legacy npm release; confirmed source mismatch, not a reproduced workflow failure.** npm-publish.yml expects scripts/apply-patches.mjs and patches/pi-subagent-3.0.1-bun-rpc-entry.patch, while the assistant ships patches/apply-patches.mjs and an SDK export patch; the subagent patch is workspace-owned. Its manual path stamps a development version that a later pack guard rejects. Reconcile legacy dry-run checks if that release path is retained; retire it deliberately after Go cutover. Main container CI does not establish npm release readiness. [Workflow sources](sources.md#documentation-and-validation).

## Draft review decisions

Canvas's one-session scope, full-document CAS writes, immutable versions and screenshot feedback are retained. Its raw-HTML serving proposal does not establish network denial: default-src alone also prevents intended inline code unless a complete policy is defined, while allow-scripts plus CSP is not an OS network boundary. A bare iframe cannot send an arbitrary bearer header, a screenshot URL needs separate authentication, and a shared MCP token does not authenticate a claimed session ID. [08-canvas.md](08-canvas.md) specifies scoped credentials, isolated render jobs, version-bound images and a safe live viewer instead.

Openfig's parser-first/read-only scope is retained, including the explicitly instance-wide single-document slot and user-only upload/removal. Its paths and two-module assumption are updated for assistant/ + package/ and Browser + Canvas + Design. A saved cover remains distinct from a rendered frame. The worker needs actual filesystem/network/memory enclosure, not only a process or V8 heap limit. [09-openfig.md](09-openfig.md) preserves the tool contracts, budgets, normalization, shared focus, removal and separate renderer gate. Draft experiment numbers are not release capacity guarantees.

## Sequencing

Start FIX-01–04 and the F07 reproduction while agreeing contracts and auditing Mewa. Then deliver native Go chat and Chat + File vertical slices, continuity, Browser contributions, deployment and cutover. Only afterward implement Canvas and finally Design. Keep unresolved evidence explicit; do not replace a known limitation with an unsupported claim.
