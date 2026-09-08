# Shared implementation contracts

Read before parallel implementation. This file owns identity, transport, state transitions, migration and initial bounds. [Feature coverage](feature-coverage.md) owns retained functionality; [cross-boundary acceptance](acceptance.md) defines additional tests. Defaults are implementation inputs, not measured capacity. Reviews are evidence, not competing specifications.

## Authority and ownership

Pi owns native execution, transcripts, settings, providers, credentials, trust and resources. The assistant supervises selected Pi processes and projects supported APIs. The controller owns projects, grouping/archive, schedules, pre-handoff outbox and application metadata. The UI owns local drafts, selections and presentation, never accepted-run lifetime.

One serialized owner handles a session's child, native I/O, admission and sequence. Several clients subscribe to one owner. Native MCP remains inside Pi; no workspace module starts a second native MCP client or agent loop. The explicitly enabled generic bridge uses public APIs from that selected installation, not a bundled SDK.

Native settings change through supported native APIs and explicit user actions. Bounded agent Markdown authoring is a separate user-authorized feature, not permission to rewrite native JSON/transcripts in Go. Follow native locking for settings. A Pixie lock/revision check does not compel an unrelated editor/TUI to cooperate: prevent stale-form writes, revalidate immediately before publication, and state remaining external-writer limitations. Atomic replacement alone is not content compare-and-set or no-clobber creation.

## Identity and durable authority

| Identity | Lifetime and role |
| --- | --- |
| sourceCommit / releaseId | Full source SHA / sha-<12>; build provenance, not an ordered schema version |
| hostIdentity | Stable random installation identity for one canonical native agent directory; shared by both binaries |
| authorityBindingId | Persisted explicit controller pairing with that host/native storage; destructive recovery authority |
| bootId | Fresh assistant-engine start identity |
| childGeneration | Fresh managed execution-context identity on start/replacement or successful native session ownership transfer |
| nativeSessionId / entry / leaf / tool-call IDs | Native identities retained without renaming or numerical coercion |
| sessionKey | Opaque paired-host/native-session/file association; no first-match behavior for copied/duplicate native IDs |
| browserRequestId | Browser protocol's string correlation/replay ID, scoped by client/connection policy |
| hostRequestId | Host v2 positive safe integer, scoped to that host transport connection |
| nativeRequestId | Native RPC's separate optional string correlation ID; generate one for calls requiring correlation |
| mutationId / deliveryId | Stable retry/delivery identity with payload fingerprint; not a transport request ID |
| module generation / document / revision | Durable resource identity, independent of visible selection |
| selection revision / draft revision | Shared-focus or per-client edit conflict detection, not authorization |

Maintain explicit request-ID maps at each adapter. Do not convert browser string `001` into host integer 1 or pass host IDs to native callbacks as durable identity. Reconnect invalidates transport mappings, not the mutation ledger. Classify browser replay/ack/resume separately from host responses. URL schema v2, browser protocol, host protocol v2, native RPC and MCP negotiate independently.

Replace the current endpoint/secret-derived deletion binding with a v2 binding to authorityBindingId, hostIdentity, verified native storage and session-file association. Live recovery still needs an authenticated connection to the paired authority. A new port, bootId or rotated credential is not a new native session; a claimed old hostIdentity alone is not proof of pairing.

The embedded composition admits only its locally constructed host and supplies private dialing credentials internally. External pairing and verified secret rotation are explicit. A changed host/native storage requires re-pairing. Migrate old deletion claims only while their previous authenticated configuration and exact session association are verifiable. Otherwise retain the tombstone as recovery-blocked; never discard it or replay a delete against a new endpoint.

Keep one assistant identity/lock per canonical agent directory across both binaries. New assistant metadata uses `$XDG_STATE_HOME/pixie/assistant/<agent-dir-key>` (default `~/.local/state/pixie/assistant/<key>`). Explicit migration preserves the old `<agentDir>/pixie` identity/metadata without relocating native files. Revalidate directory identity after native first-run creation. File incarnation checks detect replacement; normal native appends must not create a new sessionKey on every message.

## HTTP authority and routing

Validate request authority before serving API/WS/module/file/static routes. Derive an allowlist from configured listener/public origins, not from the requesting Host. Local defaults admit documented literal loopback/localhost forms at the actual listener port; normalize default ports and IPv6 consistently on both sides. An arbitrary DNS hostname resolving to loopback is not automatically admitted.

Remote UI requires the declared public origin and authentication. Configure trusted proxy peers and host rewrites explicitly; arbitrary Forwarded/X-Forwarded-* headers cannot change accepted scheme, host or secure-cookie policy. Match Host independently of Origin. Browser-origin/CSRF/cookie checks remain additional controls. Explicitly authenticated service-to-service requests may omit Origin under their distinct role/route policy; they do not bypass Host or resource authority.

One top-level router consumes registered module route ownership. Reserve core routes such as /ws, /auth, /mcp/objective and management namespaces. Reject duplicate/overlapping registrations. Register module MCP, management and artifact surfaces through the same definition instead of adding Browser/Canvas/Design branches. Unknown /mcp/* and /api/* paths return non-success API errors, never the SPA document. The static fallback is only for frontend navigation. Test the real assembled HTTP handler.

A session ID, project field, MCP transport-session identifier, document ID or URL route is not permission. Resolve resource scope from the authenticated principal and paired context before expensive work. Keep human management authority distinct from model read/tool authority. Secrets never enter prompts, URLs, argv, layout state or unredacted diagnostics.

## Host protocol

The rewritten host uses protocolVersion 2. Retain the explicit v1 adapter during migration; do not keep v1 while silently changing completion/capability semantics. Negotiate one host contract per connection and update schemas, dispatchers, bindings, callers and tests together. Browser protocol 88 is the reviewed baseline, not an alias for either host version.

Keep authenticated loopback /pi WebSocket with request/result/error envelopes. Require v2 hello before other calls; report release/source, hostIdentity, bootId, native version/executable and contextual operation sets without credentials. Core compatibility requires native sessions/catalog/read/prompt lifecycle, not global provider administration or MCP. Replace broad Administration gates and hardcoded operation flags.

Host requests require object envelope, positive safe-integer ID, nonempty method and object params. Invalid JSON closes 1007; invalid envelope/handshake or duplicate in-flight host ID closes 1008; oversized input closes 1009; exceeded output budget closes 1013. Well-formed unknown methods receive method-not-found. Resource/revision conflicts, unavailable capabilities, delivery uncertainty and persistence uncertainty have explicit typed errors.

Do not apply host-ID rules to the browser/native protocols. A retry uses a new transport ID plus the original mutationId and fingerprint. Different content under the same mutationId conflicts. Lost transport response does not authorize an unrecorded fresh operation.

API-01 authors the exhaustive catalog in package/contracts from actual browser/controller/host dispatchers and callers, including methods missing from constant lists. Record schemas, operation owner, profile, native/bridge route, effects, authorization, timeout, completion point and FC row. Generate/check bindings without making assistant compilation depend on frontend assets. Preserve bounded unknown native payload fields independently of strict Pixie envelope validation.

## Native framing and events

One reader and serialized writer per child correlate native calls. LF delimits JSONL; optional preceding CR is accepted, Unicode separators inside strings are not delimiters. Bound records and writes; never truncate JSON, interleave records or treat logs as events. Drain stderr independently. A slow client must not block native stdout indefinitely.

Wrap projected events with sessionKey, bootId, childGeneration and monotonic sequence. Final native messages are authoritative; partial blocks use native content indexes/tool IDs. Preserve images, visible custom entries, summaries, errors and unknown usage; hidden context stays hidden. Partial tool arguments do not prove tool execution.

Snapshot/checkpoint and subsequent buffered events share an owner. Several native queries are not an atomic snapshot: reconcile leaf/history against intervening events before ready. Test initialization, compaction, fork/clone and concurrent attach. Native whole-history allocation is an upstream constraint; use bounded read-only disk indexing for old history and supported native incremental queries where available. Oversized records produce a limit/degraded result without deleting or repairing source.

Apply aggregate serialized-byte admission to reading, queued requests, replay and outbound buffers as well as per-frame/count limits. Reserve incrementally before accumulating large bodies, release on every terminal path, and bound decoded structures separately. Do not read/parse an entire large message and only then check the concurrent-request cap.

Reserve a small independently admitted control lane for Stop, UI cancellation and service draining. Large history/data work cannot consume it. This does not bypass authentication/schema checks or interleave a partial native JSONL record. Bound slow/incomplete readers and stalled writers; if a partially dispatched native request cannot be recovered, preserve its uncertain outcome and tear down safely instead of resending. Control admission is a bounded-latency property, not a promise to bypass network head-of-line blocking instantly.

## Prompt, outbox and Stop state machines

A delivery transitions prepared -> dispatching -> accepted -> settled, or explicit rejected/uncertain/interrupted outcomes. Persist its dispatch claim before sending. Native prompt success means accepted/queued/handled, not a complete turn. Native failure after acceptance is an event/result state, not a second acceptance reply.

The controller owns runnable work before native handoff; Pi owns it afterward. An item cannot be runnable in both places. Losing dispatch acknowledgment is uncertain. Retrying a known mutation returns its status; reconnect/restart must not resend it. Retain attachment and delivery identity, not only text. Explicit user resolution of uncertainty does not imply exactly-once external effects.

Settlement follows native agent_settled and tested command-specific behavior, not the first agent_end or a quiet timer. No-LLM commands and extension-triggered work have their own traces. Unknown settlement blocks automatic follow-up dispatch. There is no blanket two-minute limit on a valid coding run.

| Action | Effect |
| --- | --- |
| Hide/collapse/focus | Presentation/subscriptions only; retain draft, session and accepted work |
| Close secondary resource | Clear right selection and apply that module's explicit lease/resource policy; never Stop Pi implicitly |
| Close conversation view | Clear selection, retain native session and active runtime |
| Stop | Freeze controller dispatch first, clear native continuation and cancel pending UI, request native abort, then verify generation quiescence |
| Clear queued items | Explicitly remove selected not-yet-dispatched items; never claim accepted native work was removed |
| Archive | Metadata only; require unsettled work to be explicitly stopped first |
| Delete | Confirm native deletion and Canvas cleanup, persist tombstone, then teardown/remove under verified identity |
| Release idle runtime | Verify settled/no pending work or liveness pins, terminate managed child and free residence while retaining history, draft, selection and metadata |
| Release to TUI | Same verified idle termination plus an explicit handoff/resume instruction; independent TUI still does not share a live writer |

Stop retains unsent outbox items paused. Restoring native queue text creates an explicit draft proposal, never automatic submission. Resume/discard is a user decision. If detached work or queued continuation cannot be verified cancelled through the supported profile, finish Stop by terminating that managed generation within the shutdown bounds. Report forced termination and interrupted/uncertain external effects; killing a local process does not undo a remote job or tool side effect.

The service reports stopping, graceful/forced stopped and any uncertainty distinctly. A forwarded abort/UI response is not evidence that native code accepted it. Prevent late old-generation callbacks from restoring activity after teardown. At the finite resident cap expose Release idle runtime for eligible residents; do not make Close delete sessions, silently evict unknown background work or require restarting the whole service to use session 17.

All application subprocess wrappers, including Git and Browser helpers, bound descendant termination and inherited-pipe draining. A context deadline alone is insufficient. Use a managed group and finite wait/drain escalation with tests; inspect the final Docker entrypoint's reaping rather than assuming host systemd applies there. Never terminate independent user processes.

## Persistence outcomes

The persistence contract distinguishes known-uncommitted, installed/committed, and durability/outcome-uncertain. A function may return an error after rename made a primary visible. Do not infer unchanged disk from err != nil or promise rollback by preserving only the old in-memory map.

Validate/reserve/stage first. Publish the primary at the declared commit point, complete required file/directory synchronization, then acknowledge success. Return enough internal outcome information for callers to distinguish a known pre-publication failure from an installed-but-unconfirmed outcome. After the latter, retain mutation identity and reconcile the validated primary before accepting dependent mutations or triggering unsafe effects. Report uncertainty until durability/state is established. Do not overwrite a visible candidate with an old backup to manufacture a failed-no-change result.

Module disable/deletion revokes new authority before unsafe continuation once publication is known or uncertain; enable must not start a new privileged runtime on an unconfirmed commit. Reconcile persisted intent separately from actual readiness. Queues/schedules do not dispatch an uncertain claim; tombstones never resurrect through fallback. Crash recovery cannot make an old execution ledger authoritative over later effects. Test errors at staging, backup rename, primary rename, directory sync and reply loss.

## Session catalog, grouping and metadata migration

Use authority/session associations with optional projectId and native cwd. Migrate project-required queue/deletion records; do not invent a hidden all-files project. Schedules remain project-scoped. Read-only discovery outside admitted roots does not authorize Files/Git or launch native extensions. New conversations remain drafts until native persistence; no synthetic headers. Fork/clone ownership transfers only after native success, preserving independently reopenable source.

Inventory all controller config/projects/session associations, objective/task/queue/schedule/deletion ledgers, old assistant archive/parent/identity metadata, MCP memberships, Browser leases and browser drafts/layout. Native versus legacy Pixie MCP schemas must be detected explicitly. Unknown source files remain untouched. Give each conversion schema versions, verified pairing/path mapping, repeatable phases and rollback evidence.

Migrate under stopped admission with restrictive backups and a durable receipt. Multi-file conversion needs explicit checkpoints, not a claim that one rename makes it atomic. Reconcile post-backup dispatch/deletion effects before rollback; old binary plus old JSON is not sufficient. Missing/corrupt authority fails its mutations closed while safe diagnostics/readable areas remain available. Topology switching/uninstall does not move or delete native Pi state or authored module documents.

## Read-only Git inspection

Read-only includes no repository-configured code execution, network fetch, or mutation of admitted/native state. Disabling hooks, external diff and textconv does not disable clean/process filters. Do not inherit arbitrary user Git environment or accept executable/config paths from a browser request.

Use a verified non-executing path for commit/index metadata and bounded raw worktree comparison. Audit every Git command against local/included config, attributes, worktree/submodule links and object lookup. A raw-byte view may differ from LFS/clean/encoding conversions; label that difference or make the affected conversion-dependent view unavailable. Do not silently claim native conversion fidelity or execute a helper in the controller to obtain it. A fully converted view needs an independently contained tested implementation or separately approved limitation.

Preserve multi-repository discovery, commit/branch/review identity and ordinary diffs. Root-relative file checks, output bounds and process deadlines remain necessary even when conversions are disabled. No new IDE, Git mutation controls or automatic worktrees. FIX-11 acceptance exercises the real endpoint with harmless configured filter markers.

## UI navigation and persistence

One route driver and reducer own primary/secondary selection and layout. Use `#/v2/chats/<sessionKey>`, archive/session, schedules/schedule and settings/section routes. Encode bounded namespaced secondary identities/context as query data, never secrets/content. Hash-route v2 does not negotiate any wire protocol. Back/forward changes selection without reissuing mutations.

Remember last valid selections per area/context. Settings hides incompatible session inspectors and restores only valid context on return. Same-project files may survive session switches; session-scoped modules may not change owner invisibly. Design remains labelled instance-wide. A selected missing/unauthorized/unavailable item gets its own state, not an unrelated project-empty view. A late create may add a session to the catalog but cannot steal focus after newer navigation.

Runtime, pending dialogs and drafts live outside Svelte component lifetime. Persist bounded layout preferences, not full transcripts or response credentials. Each browser owns its draft/revision keyed by sessionKey; native editor text applies automatically only to an unchanged empty originating draft. Otherwise offer explicit insert/replace, never auto-send. Native editor() dialogs have separate draft/expiry state.

Use 48 px rails/headers, 256 px sidebars bounded 200–400 px, 360 px content minima and initial 50/50 split. Collapse right sidebar then left before single-content focus when space is insufficient. Responsive collapse does not overwrite user preference. Narrow drawers preserve the same selection model and focus restoration. Grouped Chats shows five recent items plus selected/running sessions, with paging for more. Migrate old tabs without keeping a second active state machine.

On deployment update, compare browser protocol/capabilities independently of build hash and host v2. Unsupported peers stop new mutations and present explicit refresh/recovery. Preserve drafts/selection before reload; unavailable storage must not silently discard unsaved input. Reconcile acknowledged/uncertain operations with their original mutation IDs, never resend as fresh work. Old lazy-asset 404s need an actionable recovery state rather than blank content or an infinite reload loop. Test both topologies and keep retained old assets bounded.

## Configuration and initial bounds

Precedence remains explicit CLI > documented environment > explicit JSON config > defaults. Normalize once; full-host embeds the shared assistant section and controller-only rejects local-assistant execution settings. Read-only doctor reports redacted values without installation, model calls, native writes or project-extension loading. Active probes are explicitly opt-in.

Resolve explicit Pi or service PATH to an absolute executable. Preserve native HOME/PATH/cwd/agentDir/provider/proxy/tool environment after removing Pixie service credentials. Native argv is an operator array; reject reserved mode/session/resume/continue/print/no-session/cwd controls including aliases/equals forms. No arbitrary startup argv from model/web calls. Scoped native integration credentials use the private bridge channel.

| Bound | Initial contract |
| --- | --- |
| Host/native record and browser/host frame | 32 MiB serialized UTF-8 each; not an allocation budget |
| Ordinary in-flight host requests | 128/connection, 256/engine, additionally constrained by aggregate bytes |
| Aggregate buffered transport data | 64 MiB per controller process and per assistant engine initially, across ordinary input/output/replay; account copies and bound decoded structures separately |
| Reserved control admission | 8 small operations/engine, at most 64 KiB each and 1 MiB reserved serialized storage; normal traffic cannot consume it |
| Managed children / launching / actively dispatched turns | 16 / 4 / 8, reserved before startup |
| Automatic idle eviction | Disabled until generic liveness is proved; explicit eligible-runtime release provided |
| Hello / ordinary admin / auth interaction | 10s / 30s / 10 minutes; auth scoped to initiating client |
| Native abort grace / TERM-to-KILL | 10s / 2s, within overall service drain |
| Application drain / systemd stop | 25s / 30s, including creation, admin, extensions and pipe cleanup |
| Pending UI / default lifetime | 16/session / 30 minutes or shorter native deadline |
| Passive UI | 16 status and 16 widget keys; 64 updates/s; clears/settlement cannot be silently dropped |
| Image input | At most 8; 4.5 MiB base64 per image, 24 MiB aggregate base64, plus whole-frame and decoded validation |
| Text input | Initial 4 MiB UTF-8 target; compare legacy exposed behavior/attachment limits first; any reduction needs FC04 approval, never truncation |
| Diagnostic ring | 64 KiB per child/worker, redacted |
| Catalog/history page | 100 entries and explicit serialized byte cap/cursor; count alone is insufficient |
| Generic persisted JSON | Keep existing 16 MiB ceiling for metadata/ledgers |

Serialized admission budgets are not total-RSS guarantees: track decoded structures, retained projections, image pixels and native process memory separately. Preserve current per-text-attachment and aggregate resource limits through the same cross-boundary inventory rather than replacing them with the prompt cap. Base64 length is not decoded-image memory. Publish effective units/bounds and test escaping, Unicode and boundary values before changing a supported input envelope.

Design's normalized index has a separate 64 MiB artifact limit. Stage it as an immutable bounded file/index artifact, validate through a dedicated path and publish small metadata last. Do not pass it to generic persist.Write, inline it in a host/browser frame, or raise all shared caps. Worker messages carry validated operation/artifact identity, counts and hashes, never arbitrary output paths. Queries remain paged/bounded and index retention/decoded-cache memory are independently limited.

## Worker boundaries and module defaults

Native Pi retains its intentional host-user authority. Worker controls apply to untrusted application processing, not to Pi tools. Same-UID subprocess separation, changed HOME or read-only project mounts alone are not containment.

During migration the existing Docker Browser profile may remain with its explicit non-isolation warning. It is not contained-browser evidence and must not be silently reused under the Pi owner's direct-host account. Core Browser acceptance must name/test the actual profile in each deployment; a trusted-only exception needs explicit recorded approval. No exception is preapproved here. Canvas/Design always require their contained profile, regardless of the older Browser posture. Basic chat requires none of these optional workers.

The initial contained Linux candidate uses private mount/PID/user namespaces and delegated cgroup-v2 limits; a tested bubblewrap launcher may supply namespaces. Verify host support and place the process under its limits before any untrusted input executes. Docker needs tested delegation or a separately provisioned restricted worker service. No privileged controller, Docker socket or automatic host-policy changes. Missing requirements make processing unavailable with diagnostics; retained metadata/removal remain accessible.

Mount only verified runtime/libraries/approved fonts and immutable input read-only, with a job-private bounded writable temp/output area. No broad HOME, controller/Pi/project data, user D-Bus/runtime sockets, sibling jobs or writable cgroup tree. Preserve only required IPC; verify unintended inherited descriptors are closed. Browser has its declared browsing egress policy; Canvas/Design have no external/host network. Any local asset server stays inside the job enclosure without service credentials.

Enforce scratch bytes and inode bounds as well as memory/swap, CPU, PIDs, output and wall time. A delegated cgroup alone does not limit ordinary disk files. Use bounded tmpfs or a quota-backed job area; reserve output/staging space before admission. Worker OOM/cleanup cannot take down the controller/Pi or consume a shared sibling allowance. Verify failure before start, during transfer and after cancellation; kill/reap matching jobs and reject late artifacts.

| Resource | Canvas | Design |
| --- | --- | --- |
| Active / queued heavy jobs | 1 / 2 globally; coalesce stale automatic refresh, explicit overload is busy | 1 / 1; second active/pending upload conflicts |
| Worker memory / PIDs | 1 GiB / 256 | 512 MiB / 64 |
| Wall time | 30s | 30s parse; 30s render |
| Viewport/pixels | 1280x800 DPR 1; max 2048/dimension and 4,194,304 pixels | Same checked image/render bound |
| Input | 512 KiB HTML/write | 50 MiB source; 256 MiB declared ZIP expansion; 4,096 archive entries; actual expansion bounded independently |
| Structured output | Selector 512 characters, 4,096 matches, 64 KiB returned text/DOM | 100,000 nodes/depth 128; 64 MiB stored index; query 100 nodes/256 KiB |
| Image content | 2 MiB compressed image bytes before base64, decoded pixels bounded separately | Same; explicit cover/frame kind |
| Storage | 64 MiB global authored revisions/cache; evict regenerable cache only or reject | Retain source; derived preview cache 128 MiB; no source eviction |
| Scratch | 128 MiB / 8,192 inodes per job initially, included in reservation and measured working set | 384 MiB / 8,192 inodes per job initially; memory-backed scratch counts against worker memory |
| Diagnostics | 64 KiB | 64 KiB |

Validate these starting budgets with representative/hostile fixtures; do not call them measured capacity. Adjust documented scratch/working-set budgets together when justified rather than defeating the memory limit. Whole-controller limits are not a substitute for worker-only bounds. Unknown/invalid restrictive module configuration does not fall back to permissive defaults.

Canvas captures version/generation at admission; raster-first UI executes no generated scripts in the user's browser. Native-session deletion includes Canvas tombstoning; archive retains it. Design's instance-wide source is independent of chat/project deletion. Interrupted uploads release their reservation and retain bounded operation status; after complete transfer, report an operation ID so status/cancel/retry is explicit. No stale job, backup or remove/recreate race can restore a revoked generation.

## Required cross-boundary tests

Run [acceptance.md](acceptance.md) together with FC01–FC33 and the feature-specific suites. Pin old/new protocol fixtures and test actual selected Pi, assembled HTTP router, final service/container entrypoints and released archives. Compilation, simulated native events and review probes are different evidence. Record unresolved public API/renderer boundaries with owner, reproduction and release consequence; do not infer them from the assistant's implementation language.
