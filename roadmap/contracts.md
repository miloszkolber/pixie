# Shared implementation contracts

Read before parallel implementation. This file owns target identity, transport, state, migration and initial operational defaults. Feature plans supply behavior; [feature-coverage.md](feature-coverage.md) decides whether migration is complete. These defaults are implementation inputs, not measured performance claims.

## Authority and ownership

Pi is the only execution engine and native transcript/settings authority. The assistant supervises selected Pi processes and projects their public APIs. The controller owns projects, archive/grouping, schedules, its durable outbox and authorized application metadata. The UI owns local drafts, selections, view state and presentation, not execution lifetime.

One serialized owner handles each session's process, native I/O, command admission and event sequence. One controller outbox owns a message before native handoff. Multiple UI clients subscribe to the same runtime; they do not start duplicate owners. Native extensions/MCP clients execute inside Pi. A workspace module cannot become another Pi MCP client or agent orchestrator.

Native credentials/settings are changed only through supported native APIs and explicit authorized user actions. The optional bridge uses the selected installation's public APIs. Intentional bounded authoring of native agent Markdown files is a separate file-edit feature, not permission to rewrite transcripts or generic native JSON in Go.

## Identity and durable authority

Keep these identities separate:

| Identity | Lifetime and role |
| --- | --- |
| `sourceCommit` / `releaseId` | Build provenance; full SHA / sha-<12>. Never a session capability version. |
| `hostIdentity` | Random stable installation identity for one canonical Pi agent directory; shared by both binaries. |
| `authorityBindingId` | Controller's persisted, explicitly paired trust record for that host and native storage scope. Governs destructive recovery. |
| `bootId` | Random per assistant-engine start. Not persisted as the installation identity. |
| `childGeneration` | New identity whenever a Pi child is started/replaced, including fork/clone ownership transfer. |
| `nativeSessionId`, native entry/leaf IDs | Pi-owned identities preserved without renaming. |
| `sessionKey` | Opaque controller/assistant reference binding host, native ID and canonical file identity. Disambiguates copied/duplicate native IDs. |
| `requestId` | One transport connection's positive safe-integer correlation ID. |
| `mutationId` / `deliveryId` | Stable application retry identity and native-delivery identity; neither is a transport request ID. |
| module document/version/generation | Authoritative resource identity independent of view selection. |
| selection revision / draft revision | Client/shared-focus conflict detection, not resource authorization. |

The current `PiClient.deletionAgentBinding` hashes endpoint and secret. Do not carry that formula unchanged into an ephemeral full-host listener. Introduce version-2 recovery binding to `authorityBindingId`, hostIdentity, native storage identity and the recorded session file identity. Live requests still require an authenticated channel to that paired authority. A bootId, endpoint change, or authentication credential is not a new native session.

For full-host mode the composition root admits only the exact locally constructed host instance and supplies its private transport out of band. For external-assistant mode pairing is explicit and persisted; a new hostIdentity/storage scope requires re-pairing. Secret rotation for an already verified binding is explicit and tested. Do not automatically trust a peer because it claims the old hostIdentity.

Migrate legacy deletion bindings only while their old authenticated configuration is verifiable and exact host/session/file identities agree. Otherwise retain the record as `recovery-blocked`, keep the local deletion tombstone, and require explicit reconciliation. Never discard the journal or retry against an arbitrary new endpoint to hide a mismatch. Test ephemeral-port changes, restarts, secret rotation, copied agent directories and Docker/full-host switches.

The assistant's identity/lock are shared across both entrypoints and keyed by the canonical agent-directory path. Store new assistant-owned metadata under `$XDG_STATE_HOME/pixie/assistant/<agent-dir-key>` (default `~/.local/state/pixie/assistant/<key>`). Preserve the existing identity during an explicit migration from native `<agentDir>/pixie`; do not relocate native files. Resolve directory symlinks and revalidate identity after native first-run creation; merely listing a fresh installation does not create or write native configuration.

## Host protocol

Use host `protocolVersion: 2` for the rewritten contract. Its acceptance semantics, capability model and epoch-scoped events differ from version 1. Native Pi's JSONL protocol is a separate boundary. Keep the legacy v1 adapter during migration only; a controller negotiates one contract, never mixes reply semantics. Update contracts, dispatchers, generated bindings, tests and docs in the same integration.

Keep `/pi` authenticated WebSocket and the existing request/result/error envelope. A v2 hello is required before other methods and reports releaseId, sourceCommit, hostIdentity, bootId, native executable/version, available operation sets and UI/administration profile. Never return credential material or unrestricted native configuration.

Requests are strict JSON objects with numeric positive safe-integer id, a non-empty method and object params. Do not coerce strings/booleans to IDs. Invalid JSON closes 1007; invalid envelope/handshake closes 1008; oversized messages close 1009; exceeded send-buffer budget closes 1013. Unknown well-formed methods receive method-not-found. Resource conflicts, stale revisions and unavailable capabilities have typed errors, not successful empty arrays.

For v2, a duplicate in-flight request ID closes 1008 rather than sending two conflicting outcomes for one ID. This is an intentional v2 change; preserve v1 behavior only in its compatibility adapter. An ordinary completed operation can be retried with a new requestId and the same mutationId. Validate payload fingerprint; same mutationId with different input is a conflict.

Advertise operation sets in context, not a single `Administration` boolean. Core compatibility requires sessions/catalog/read/prompt lifecycle only. Provider login, global preferences, inventory/configuration, native MCP administration and optional modules negotiate independently. Rewrite `PiClient.initialize`, its operation projection, `CallPiUntilDone` and UI availability checks accordingly. No hardcoded true operation flags from the mere presence of `sessions`.

Author the method inventory in `package/contracts` before implementation. For every current browser, controller and assistant method record caller, owning service, request/result schema, native/bridge route, required capability, idempotency and coverage ID. Check generated Go/TS bindings and exhaustive dispatch coverage in CI. Keep unknown native payload fields as raw data; strict Pixie envelopes do not mean discarding unknown native entries.

## Native framing and events

One reader and serialized writer per child. Stdout is native protocol; stderr is independently drained bounded diagnostics. Split on LF only, accept an optional preceding CR and preserve Unicode line separators inside strings. Reject oversize whole records; never truncate JSON or treat log pollution as an event. Reader progress must not depend on a browser consuming output.

Wrap projected events with sessionKey, bootId, childGeneration and monotonically increasing sequence. Scope every checkpoint to those fields. Native request IDs, tool-call IDs, entry IDs and content indexes keep their own meaning.

Final native message bodies are authoritative. Partial text/thinking/tool arguments are projections. Tool execution is not established by incomplete tool-call arguments. Apply final reconciliation without duplicating streamed tails. Preserve images, visible custom entries, summaries, usage and hidden-record exclusion.

Capture a snapshot and its event checkpoint under one owner; buffer only subsequent events for that attachment. A bootstrap assembled from several native queries is not automatically atomic: reconcile active leaf/history against events before declaring ready. Race-test initialization, fork/clone, compaction, concurrent attach and events during history loading.

Native get_messages/get_entries can materialize large responses. Go-side chunking cannot remove that upstream allocation. Use bounded read-only on-disk indexing for catalog/older history, and get_entries cursors where appropriate for incremental resident updates. If a native record exceeds the supported bound, return a specific limit/degraded-state error and preserve its source file; never promise unbounded history or silently skip the record. Do not break one native entry into invented messages.

## Prompt, outbox and Stop state machines

Use `prepared -> dispatching -> accepted -> settled` for a delivery, with explicit `rejected`, `uncertain` and `interrupted` outcomes. Persist a dispatch claim before sending. Native prompt success acknowledges acceptance/queueing/handling, not complete agent settlement. Record native failure after acceptance through events, not a second acceptance reply.

One mutation/delivery can be native-runnable only once. Once handed off, it is no longer a runnable controller outbox item. Losing the channel during dispatch is uncertain; reconnect or service restart never resends automatically. A repeated mutation ID returns its known status/result. A user may explicitly resolve uncertainty after inspecting the transcript; confirmation is not a claim of exactly-once execution.

Use native agent_settled and known command completion semantics, not the first agent_end or a quiet timer. Commands that perform no LLM turn need a tested handled result; extension-triggered/background work cannot be assumed complete because prompt returned. Failure to determine settlement is visible and blocks automatic follow-up dispatch for that delivery. Capture actual native traces for each command family.

Pi owns native steer/follow_up/clear_queue modes. The controller's durable outbox owns only not-yet-handed-off work. Keep source text, file/image attachment references and mutation identity on outbox items; repeated identical text is not a deduplication key.

Define UI actions explicitly:

| Action | Effect |
| --- | --- |
| Hide/collapse/focus | Presentation/subscriptions only; preserve session, draft, outbox and accepted work. |
| Close secondary resource | Clear right selection and apply that module's resource policy; does not stop Pi. |
| Close conversation view | Clear selection, retain native session and its runtime while work is active. |
| Stop | Stop current generation and native continuations; freeze controller dispatch first, clear native continuation before abort, preserve unsent durable items as paused. Never automatically resume after Stop. |
| Clear queued messages | Explicitly remove selected not-yet-dispatched items; do not report native-dispatched work as removed. |
| Archive | Metadata only. Reject while active work is unsettled unless the user first explicitly stops it. |
| Delete | Confirm native file deletion and owned Canvas cleanup; durably tombstone before teardown/removal; preserve unrelated sessions, Design and projects. |
| Release to TUI | Require idle/settled and no pending dialogs/outbox dispatch, terminate the managed child, release ownership, then provide native resume information. |

Stop releases pending native dialogs first, awaits bounded native abort, then reports whether termination was graceful or forced. A request timeout is not proof a tool's external side effects were cancelled. No blanket two-minute timeout on a valid coding run.

## Session catalog, grouping and metadata migration

The authoritative session association is `(authorityBindingId, sessionKey)` with optional projectId and native cwd. Replace project-required queue/deletion/session keys with that association. Do not fabricate a hidden project or admit an entire filesystem to make ungrouped sessions pass old validators. Schedules remain explicitly project-scoped.

Metadata-only discovery works outside admitted roots; files/Git remain unavailable until independent root admission. A read-only catalog scan handles incomplete final JSONL lines, external replacement, duplicate IDs, missing files and unknown entries. It does not launch native extensions, run provider checks or rewrite transcripts.

A new conversation is a draft until Pi persists native identity. Reconcile exactly once after persistence; never synthesize a header. Clone current position and fork earlier user message are distinct native operations. After native identity changes, transfer the child association and reopen the original independently when selected again.

Migration inventory must include controller config/projects/session associations, queues, schedule execution claims, deletion journal, assistant catalog/archive/parent metadata, MCP memberships, browser panel ownership, local layout and drafts. Give each schema a version, source/destination ownership, repeatable conversion and rollback test. Preserve unknown fields where the source contract permits them; never copy raw secrets into controller or browser stores.

Convert project-keyed records to session associations only with verified host/native identities; preserve projectId as optional presentation metadata. Import archive/parent metadata from the old assistant catalog without moving native files. Tombstones and execution/deletion claims cannot fall back to older backups. On corruption, fail the affected mutation/runner closed while preserving diagnostics and unrelated readable state. Do not label unreadable data empty.

A migration is a maintenance operation with admission stopped, backup and explicit rollback policy. Mode switching does not move Pi state. Uninstall is not consent to delete native or authored module data.

## UI navigation and persistence

Use one route driver and schema version for layout. Keep session runtime/drafts independent of Svelte component lifetime. Left and right selections are separate discriminated unions with instance/project/session context. All server operations reauthorize those fields; routes are not authority.

Use hash routes to work identically in embedded/static deployments: `#/v2/chats/<sessionKey>`, `#/v2/archive/<sessionKey>`, `#/v2/schedules/<scheduleId>`, `#/v2/settings/<sectionId>`. Serialize optional secondary selection as encoded, namespaced query parameters containing opaque resource IDs and context, never credentials or raw content. The router validates the complete schema, size and context before requesting data. Back/forward changes selection without reissuing mutations.

Remember the last valid primary selection per area and right selection per compatible context. Entering Settings hides a stale session inspector; returning restores the prior valid session. Same-project files may survive session changes; session-scoped modules cannot. Instance-scoped Design remains explicitly instance-scoped. Missing/unauthorized resources show their own recovery state, not an unrelated project home.

Persist layout/schema/width preferences locally, bounded by these defaults: 48px rails, 48px aligned headers, 256px sidebars (200–400px), 360px minimum content panes, initial 50/50 split. Auto-collapse the right sidebar then left sidebar when minimum content widths do not fit; after both are collapsed use single-content focus. Preserve the user's saved widths/collapse choices and restore them on widening. Narrow navigation uses accessible drawers. Do not persist auto-collapse as a user choice.

Grouped Chats initially show five recent sessions per project and always include the selected/running session; Show more is paged. Flat/ungrouped views use the same catalog. Closing is not archiving. Migrate valid legacy tabs into independent selections, preserve drafts and ignore invalid obsolete keys. Do not keep the old tab state as a second active state machine.

Each browser owns its local draft; use a revision and sessionKey. Native set_editor_text is applied automatically only to an empty unchanged originating draft; otherwise show an explicit insert/replace action. It never submits text. Another browser's unsent draft is not implicitly synchronized or overwritten. Native editor() dialogs have separate draft/expiry ownership.

## Configuration and initial bounds

Precedence: explicit CLI flags, documented environment values, explicit JSON config, built-in defaults. Full-host config embeds the same assistant section; controller-only rejects local-assistant execution settings. Read-only doctor reports resolved redacted configuration and missing dependencies without prompting, downloading or loading project extensions. An active probe is a separate opt-in command with declared effects.

The selected Pi executable is an absolute resolved file, configured first or found on the service PATH; aliases/login-shell initialization do not apply. piArgs is an operator-controlled argv array, never a shell command. Reserve mode/session/resume/continue/print/no-session/cwd behavior for the supervisor, including aliases and --flag=value forms. Do not accept arbitrary startup argv from an agent or web request.

Preserve intended native HOME, PATH, agentDir, proxy/provider/tool variables and cwd. Remove Pixie service/control credentials before exec. Module-specific credentials reach only the explicitly enabled native integration through its private channel. Existing native user extensions remain operator-owned, not filtered away to simplify tests.

| Bound | Initial value / behavior |
| --- | --- |
| Host/native JSON record, host request or send buffer | 32 MiB each; validate actual encoded bytes |
| In-flight host requests | 128 per connection, 256 per engine; excess is explicit busy |
| Managed Pi children / launching / actively dispatched turns | 16 / 4 / 8; count before startup, never evict active work to admit another |
| Automatic idle eviction | Disabled initially; unknown detached extension work must not be mistaken for idle. Explicit runtime release is available; enable safe timed eviction only behind tested liveness support. |
| Hello / ordinary administrative request | 10s / 30s; long-running accepted turns use events, not this deadline |
| OAuth/API-key interaction | 10 minutes, cancellable and bound to requesting client |
| Native abort grace / TERM-to-KILL fallback | 10s / 2s; all within service drain deadline |
| Application drain / systemd stop | 25s / 30s |
| Pending UI requests / default lifetime | 16 per session / 30 minutes or shorter native deadline |
| Passive UI keys / update rate | 16 status + 16 widgets; 64 updates/s; removals/settlement are never silently rate-dropped |
| Native input | At most 8 images; 4 MiB text; 24 MiB base64 per image, also subject to the aggregate 32 MiB encoded request. Reject before dispatch; never truncate a prompt. |
| Diagnostic retention | 64 KiB bounded ring per child/worker; redacted, no transcript/secrets by default |
| Catalog/history page | 100 entries plus explicit byte limit/cursor; never allocate an unbounded response because count is small |

Publish effective limits in diagnostics. Test byte boundaries with escaping/base64 and Unicode. Amend defaults only with measured evidence and matching contract/test updates; changing a supported input limit is a disclosed behavior change, not cleanup.

## Worker boundaries and module defaults

Native Pi retains user authority. Untrusted Browser pages, Canvas HTML and Design files use a separate optional worker boundary. The initial Linux launcher profile is a private mount/PID/user namespace enclosure with a delegated cgroup-v2 resource limit. A tested bubblewrap-based launcher can supply the namespace portion; namespaces alone are not a memory cap. Do not grant privileged daemon sockets or require a privileged main controller.

Declare the exact supported launcher/runtime paths and pinned dependency set in packaging. Full-host uses a configured user-level enclosure; Docker needs explicitly supported delegation/enclosure or a separately configured restricted worker service. If the required boundary cannot be established, the module is unavailable and doctor explains which prerequisite failed. Do not silently run unrestricted on the host or substitute a whole-controller memory limit for worker-only containment. Establish this profile in SEC-02 before CAN/FIG implementation; test actual flags on both architectures.

Worker input contains only immutable authorized job data. Mount executable/runtime libraries and approved fonts read-only, input read-only and one bounded private output/temp area writable. No Pi HOME/auth, projects, application databases, broad host mounts or service bearer. Browser may use the explicitly configured browsing network policy; Canvas/Design have no external/host network. For their optional asset server use only a job-local namespace listener with no parent credentials. IPC carries bounded job/output messages, not an arbitrary command interface.

| Resource | Canvas default | Design default |
| --- | --- | --- |
| Active heavy jobs / queued jobs | 1 / 2 globally; coalesce obsolete automatic previews, explicit jobs get busy rather than unbounded queue | 1 / 1; second source upload conflicts |
| Worker memory / process ceiling | 1 GiB / 256 | 512 MiB / 64 |
| Worker wall deadline | 30s | 30s parse; 30s optional render |
| Viewport / pixel cap | 1280x800, DPR 1; at most 2048 per dimension and 4,194,304 pixels | Same pre-allocation render/image pixel cap |
| Input | 512 KiB HTML/write | 50 MiB source; 256 MiB declared ZIP expansion; 4,096 entries |
| Structured output | Selector at most 512 characters, 4,096 matched nodes, 64 KiB returned text/DOM with truncation | 100,000 indexed nodes, depth 128, 64 MiB index; query pages 100 nodes, 256 KiB response |
| Image content | 2 MiB encoded image bytes before MCP base64 | Same; label cover versus rendered frame |
| Storage | 64 MiB globally, including authored revisions/cache; evict caches only, otherwise reject new write | Retain source; derived preview cache 128 MiB; no automatic source eviction |
| Diagnostics | 64 KiB | 64 KiB |

Module plans' initial budgets refer to this table. These are enforceable starting defaults, not successful capacity measurements. Hard worker failure must leave the controller, assistant and other modules usable. Validate actual archive/chunk expansion and output, not only metadata. Do not rely on V8 heap settings as total-memory enforcement.

Canvas captures version at admission; raster-first live viewing executes no generated script in the user's browser. Delete native session includes explicit Canvas tombstoning; archive retains it. Design source remains instance-wide until human removal, independent of chat/project deletion. Cancelled/incomplete uploads do not claim the persistent slot: keep a bounded upload-operation status, cancel on interrupted transfer; after complete transfer/index admission report its operation ID and allow explicit cancellation/status lookup without duplicate upload.

## Required cross-boundary tests

Pin schemas/fixtures for old and new host contracts. Test exact identity and capability transitions, duplicate/lost requests, Stop/native continuation, pending UI invalidation, readonly discovery, ungrouped metadata migration, recovery-blocked deletes, both composition modes and all release identities. Run the covered native APIs on independent Pi, not only mocks.

No blanket assertion of completeness can replace these gates. Unknown upstream behavior has an owner, reproduction and explicit release consequence in feature-coverage.md; it is never silently inferred from source-language compatibility.
