# Repository review

This is the single review record for the roadmap. It consolidates the repository passes, the additional findings from both roadmap branches, and the Canvas/Openfig draft review. Requirements live in the implementation plans; only [execution.md](execution.md) records completion. None of the findings below is marked fixed by this documentation merge.

## Scope and evidence

Runtime baseline: `f63d0d5bcb7f6e342058734b2e741ad2619d8867`. Main commit `71590cac48925b31b9d5d3c7d1746ee94b351772` added the original roadmap directly to main. The merged inputs are `05d84209e0e61d225bfb797b8b8a1b00e0712bff` and `8db480ebff076f1dbdee3158c4f42c093638d227`, including its parent `eacfac43e81e04e798f4e8fd43af7d77315ad6ff`. The runtime sources were unchanged by those planning revisions except source comments.

Review covered assistant entrypoints/session/provider/resource/agent integration, host and browser protocols, controller lifecycle/queues/deletion authority, HTTP admission/routing, Git execution, persistence, modules, UI state/styles/adapters, deployment and representative tests/workflows. It was not a line-by-line audit of every file. The supplied screenshot and five wireframes informed the workspace requirements.

The initial review inspected reported successful CI at run `34226327666`; it did not rerun that suite. The third pass recorded isolated origin/Git probes listed below. Those probes were not rerun for this merge. Complete application, independent Pi/bridge, final artifacts, systemd, worker containment and Openfig renderer verification remain implementation work. The blank-content screenshot has not been reproduced. Source findings do not establish an exploit or operational failure unless the evidence explicitly says so.

P1 means a relevant release/cutover blocker; P2 means foundation work. These are planning priorities, not vulnerability scores. Recheck findings against the implementation checkout. Preserve the existing native session format, scoped dialogs, reconnect generations, uncertain-delivery ledger, read-only path checks, Mewa integrity and useful acceptance tests.

## Findings

<a id="f01"></a>
### F01 — Installed Pi is not the runtime authority

**P1; confirmed architecture mismatch.** The current assistant installs a pinned SDK and creates SDK sessions directly. Sharing agentDir does not select the operator's installed executable/runtime. SDK embedding is not inherently wrong, but does not meet the requested plug-and-play host ownership.

Use the selected executable and the same Go engine in both distributions. Preserve every retained feature through the native/bridge profiles in [feature-coverage.md](feature-coverage.md); a core-chat port with unavailable administration is not full parity. **Work:** GO-01–GO-09, BRIDGE-01–BRIDGE-03, COVERAGE-01. [Sources](sources.md#assistant-and-native-pi).

<a id="f02"></a>
### F02 — Production restart does not terminate the service

**P1; confirmed defect.** `runtime.restart` closes peers and calls optional `options.onRestart?.()`. Production main provides no callback and the server installs no default, although the test injects one. Restart can disconnect clients without exiting, leaving restartPending set.

Implement bounded shutdown through the executable/composition root. Verify a new PID and boot identity, including the complete combined service rather than a library-level exit. **Work:** FIX-01, BUILD-05. [Sources](sources.md#assistant-and-native-pi).

<a id="f03"></a>
### F03 — Module enablement changes before persistence succeeds

**P1; confirmed defect.** `Registry.SetEnabled` mutates its enabled map before `persist.Write`. A failure can leave catalog/routes, disk and the active service inconsistent.

Stage a candidate and distinguish desired state from readiness. Preserve prior state only for a known pre-publication failure; post-rename uncertainty follows F32 rather than a blanket failed-means-unchanged assertion. **Work:** FIX-02, FIX-13, EXT-02. [Registry](sources.md#extensions-and-deployment).

<a id="f04"></a>
### F04 — Repeated enablement restarts Browser

**P1; confirmed defect.** The enabled path constructs a new service and closes the previous one without an unchanged-value guard. Retrying enablement can destroy the active handle.

Unchanged enablement must be a no-op. Restart is a separate action, with safe shared-storage startup/shutdown ordering. **Work:** FIX-03, EXT-02. [Registry](sources.md#extensions-and-deployment).

<a id="f05"></a>
### F05 — Protocol reference and request validation disagree

**P1; confirmed gap.** The host reference lists methods/families inconsistent with the dispatcher. The server coerces IDs with `Number(request.id)`, admitting some non-number values under a numeric-ID contract.

Inventory every boundary and actual caller. Validate host envelopes strictly, preserve unknown native payloads, and make new semantics an explicit version change. Do not apply host numeric-ID validation to browser/native IDs; see F34. **Work:** FIX-05, API-01, API-02. [Host and documentation](sources.md#documentation-and-validation).

<a id="f06"></a>
### F06 — One content slot owns both navigation sides

**P1; confirmed design gap.** Chats, files, diffs and Browser compete for one active ContentTab in project-work-area. Opening a file replaces the conversation; the right side is hard-coded to Files/Changes.

Use independent primary/secondary selections and six stable slots. Restyling the same ownership model cannot satisfy the wireframes. **Work:** STATE-01, UI-01–UI-06. [UI sources](sources.md#workspace-and-mewa).

<a id="f07"></a>
### F07 — Duplicate-create admission and the empty screenshot are separate issues

**P1; missing guard confirmed, screenshot cause unverified.** The create handler has no local in-flight guard. Repeated activation can issue repeated requests, but repeated Chat labels alone do not prove accidental creation or execution.

`openChatSession` normally activates its result. Capture route/selection/restoration/request generations before diagnosing the blank main panel. Add guarded creation and stale-response rejection; every selection resolves to its own loading/content/error state. **Work:** FIX-04, UI-03, UI-06. [UI sources](sources.md#workspace-and-mewa).

<a id="f08"></a>
### F08 — Two visual foundation systems coexist

**P1; confirmed ownership gap.** Mewa styles coexist with independent Pixie palette, typography and structural tokens. Some existing wrappers already match Mewa and should remain. No computed-style audit proved a particular cascade collision.

Give Mewa shared foundation ownership and Pixie product geometry/composition. Retire old generators with their consumers, not through escalating specificity or another wholesale framework replacement. **Work:** MEWA-01–MEWA-03. [Sources](sources.md#workspace-and-mewa).

<a id="f09"></a>
### F09 — Global availability replaces the workspace

**P2; confirmed UX gap.** Provider/connection gating can replace the entire shell, while Settings is a modal.

Keep navigation, diagnostics and safely readable content available; disable only unavailable actions and label stale data. Put Settings into the primary-area model. **Work:** UI-04, UI-06, API-01. [Shell](sources.md#workspace-and-mewa).

<a id="f10"></a>
### F10 — Supported native editor-text behavior is missing

**P2; confirmed capability gap.** The existing custom bridge marks composer APIs unsupported, while inspected native RPC includes `set_editor_text`.

Implement session/client/revision-aware insertion with explicit draft-conflict handling. This does not imply synchronous getEditorText, terminal component factories or automatic submission. Native confirmation responses use `confirmed`, not a generic value. **Work:** GO-06, EXT-01, FC13–FC16. [UI/RPC sources](sources.md#assistant-and-native-pi).

<a id="f11"></a>
### F11 — The module foundation is Browser-specific

**P2; confirmed design gap.** Backend validation, catalog, health/lifecycle and frontend integration contain Browser-specific branches.

Generalize once through trusted backend/UI contributions, including sidebar-only and viewer-only fixtures. Rich local adapters are distinct from generic MCP content; no remote-code marketplace is required. Route ownership must also be generalized, as F33 explains. **Work:** MODULE-01, EXT-02, EXT-03, ROUTE-01. [Sources](sources.md#extensions-and-deployment).

<a id="f12"></a>
### F12 — Browser shares controller security context

**P1; confirmed exposure, no exploit demonstrated.** Browser/controller share UID, host networking and accessible storage; Chromium's sandbox is disabled. Filtered environment and private HOME/TMP are not filesystem/network isolation.

Document actual profiles and verify worker containment end to end. Do not transfer this posture silently to a same-user direct-host worker. Native Pi's intentional tool authority is a different boundary. **Work:** SEC-01, SEC-02, EXT-03. [Deployment sources](sources.md#extensions-and-deployment).

<a id="f13"></a>
### F13 — Hardening claims exceed the supplied Compose flags

**P2; confirmed mismatch.** Non-root/read-only/tmpfs settings exist, but the supplied deployment does not express every discussed capability, privilege, memory or PID restriction.

Test the intended flags or remove the claims. Runtime configuration, not image comments, determines enforcement; native Pi tools must not inherit worker-only restrictions. **Work:** SEC-01, DOC-02. [Compose/security](sources.md#extensions-and-deployment).

<a id="f14"></a>
### F14 — Routine CI can publish, and release completeness needed one owner

**P1; confirmed workflow behavior and resolved planning gap.** Existing non-PR events, including main pushes and scheduled jobs, can publish images/promote latest. The prior plans also differed on required image outputs, triggers and release naming.

The canonical build plan requires both host variants on both architectures and a matching two-platform Docker image under one sha-12 source identity. It defines frozen inputs, complete-set staging, immutable retries, collision handling, partial-publication recovery and non-regressing latest. Publication follows the explicitly enabled main policy; PR/scheduled validation does not publish. Repository protections beyond inspected configuration remain unverified. **Work:** REL-01–REL-04, PKG-02/03. [Workflows](sources.md#documentation-and-validation).

<a id="f15"></a>
### F15 — Existing compatibility tests do not prove independent-installation parity

**P1; confirmed test gap.** Workspace-installed SDK-host tests on two architectures do not prove final binaries against independently installed Pi with different PATH, configuration, extensions and distribution surfaces.

Retain those tests and add independent Vanilla/admin/MCP profiles, unsupported versions, separate TUI sessions and extracted final artifacts in both topologies. **Work:** GO-01–GO-09, BRIDGE tasks, COVERAGE-01, PKG-03. [CI](sources.md#documentation-and-validation).

<a id="f16"></a>
### F16 — History is chunked after eager materialization

**P2; confirmed optimization opportunity, not an observed memory failure.** History is assembled and serialized to decide chunking. Large records/images and concurrent attaches remain allocation risks.

Use bounded iteration, incremental encoding and consistent snapshot checkpoints. Native whole-response allocation is a separate upstream constraint that Go chunking cannot remove. Measure total process memory; do not claim all history currently occupies one wire frame. **Work:** GO-02, GO-04, LIMIT-01, PERF-01. [Session/transport](sources.md#assistant-and-native-pi).

<a id="f17"></a>
### F17 — Native and durable queues create a handoff hazard

**P1; confirmed porting boundary, not a current duplicate-execution claim.** Pixie has a durable outbox and uncertainty state; native RPC separately has steering/follow-up queues. A naive port can make one item runnable twice or treat acceptance as completion.

One owner controls each stage. Preserve dispatch identity and uncertain outcomes; test retry, compaction, settlement and Stop. Neither reconnection nor topology switching implies exactly-once external effects. **Work:** GO-05, API-02, LIFE-01. [Queue sources](sources.md#controller).

<a id="f18"></a>
### F18 — Documentation had stale paths, duplicate plans and operational history

**P2; confirmed defects.** Root/docs references used absent source roots; deployment included host-specific history, a nonexistent Browser mount and a build command without Compose build configuration. A benchmark read an entire dotenv file as a bearer. Some security/App HTML prose exceeded actual behavior.

Keep shipped behavior in docs and implementation work here. The superseded docs roadmap/MCP drafts remain deleted. The single review file replaces the separate pass/draft records while retaining their evidence. Recheck current operating examples as code changes. **Work:** DOC-01/02. [Documentation sources](sources.md#documentation-and-validation).

<a id="f19"></a>
### F19 — Legacy npm publication asserts absent package files

**P1; confirmed source inconsistency, release not rerun.** The npm workflow expects `scripts/apply-patches.mjs` and a subagent patch absent from the current assistant package. Its development-version packing/guard path also needs reconciliation. The package uses `patches/apply-patches.mjs`; the subagent patch is workspace-owned.

Repair the legacy path only when needed before retirement, then retire new npm publication at Go cutover without altering old releases. Test actual packed contents and final new archives. **Work:** PKG-02. [Workflow](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/.github/workflows/npm-publish.yml), [package](sources.md#assistant-and-native-pi).

<a id="f20"></a>
### F20 — Optional administration is hardcoded as core compatibility

**P1; confirmed migration blocker.** `PiClient.initialize` requires sessions and providers, sets broad operation flags, and Pi-specific calls pass through an Administration gate. The basic model picker also calls provider administration. A usable native-RPC host without the old provider surface can be rejected.

Negotiate contextual operation sets and derive core model selection from native available-model/current-state APIs. Unavailable login/default administration must not invalidate chat or catalog access. **Work:** API-01/02, GO-03, FC01/10/17. [Controller](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/pi_client.go), [model administration](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/pi_admin.go).

<a id="f21"></a>
### F21 — Ephemeral transport conflicts with durable deletion recovery

**P1; confirmed binding/target conflict.** The existing deletion binding hashes logical agent, endpoint, secret and policy. An embedded endpoint or token changing at restart changes that binding even for the same native installation.

Pair durable authority separately from dialing details. Migrate only verifiable legacy claims; otherwise retain recovery-blocked tombstones. Test credential rotation, copied directories, boot changes and both topology directions without weakening live authentication. **Work:** API-03, MIG-01, PKG-03. [Binding](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/pi_client.go), [journal](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/session_deletions.go).

<a id="f22"></a>
### F22 — Global thinking validation omits a supported native level

**P2; confirmed mismatch.** The global preference path accepts values only through xhigh; inspected Pi 0.85.1 documents max for supporting models. Dynamic session selection does not repair the separate default path.

Use native-supported validation and preserve default/session/schedule distinctions. Do not rank unfamiliar advertised values into an arbitrary lower level. **Work:** FIX-06, FC10/19. [Preferences](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/providers.ts), [RPC](sources.md#assistant-and-native-pi).

<a id="f23"></a>
### F23 — Agent edits lack stale-form and external-writer preconditions

**P2; confirmed missing preconditions, overwrite not reproduced.** A controller mutex serializes its own edits, not external writers. The host checks existence before replacement and lacks expected-content revisions for update/delete. Realpath-then-open also needs adversarial replacement testing; no path escape was demonstrated.

Add exclusive create, revisions/conflicts and rooted file-identity checks while preserving unknown frontmatter. State external-writer limits honestly: an unrelated editor need not honor Pixie's lock. **Work:** FIX-07, FC23. [Authoring](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/agents.ts), [controller](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/pi_agents.go).

<a id="f24"></a>
### F24 — Raw RPC is not the full observable UI bridge

**P1; confirmed native API gap.** Inspected RPC makes setWorkingMessage a no-op and getEditorText return an empty string. Select/input/confirm abort/timeout cleanup does not emit a request-specific cancellation frame. Public UI span IDs cannot be assumed to equal RPC dialog IDs.

Verify exact supported hooks or a narrow upstream addition. Do not associate by timing/title or call a forwarded answer accepted by native code. Retained working hints/cancellation are separate from terminal factories already unsupported. No TUI-only fallback is preapproved as parity. **Work:** BRIDGE-03, FC13–FC16. [Native implementation](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/src/modes/rpc/rpc-mode.ts), [current bridge](sources.md#assistant-and-native-pi).

<a id="f25"></a>
### F25 — Ungrouped sessions require durable-schema changes

**P1; confirmed target gap.** Queue/deletion records require ProjectID, and deletion validation rejects an empty project. Sidebar filtering cannot implement genuinely optional grouping. Pixie identity/archive/parent/MCP sidecars also live partly beneath agentDir and must not be mistaken for disposable native state.

Migrate to paired authority/session associations with optional project metadata. Preserve cwd, uncertain claims, archive/parent links and schema-sensitive legacy MCP membership without moving native files. Schedules stay project-scoped. **Work:** STATE-01, MIG-01; [migration procedure](migration.md). [Queues](sources.md#controller), [deletions](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/session_deletions.go).

<a id="f26"></a>
### F26 — Assistant binding is not loopback-enforced

**P1; confirmed configuration/documentation mismatch.** Production accepts --host and forwards it to Bun. Authentication and Origin rejection exist, but default loopback is not enforcement of loopback-only binding.

Validate literal loopback/port/arguments before startup. Remote UI follows the controller's separate authority/authentication policy. **Work:** FIX-08, GO-01. [Entrypoints](sources.md#assistant-and-native-pi).

<a id="f27"></a>
### F27 — Bounded abort is not bounded service shutdown

**P1; confirmed uncovered paths, hang not reproduced.** Native abort has a bound, but construction/opening, extension/capability teardown and server in-flight waits can exceed it.

Enclose all subsystems in the application drain deadline, with managed descendant/pipe cleanup and explicit interrupted/uncertain outcomes. Test hung construction, admin I/O and teardown as well as provider stalls. **Work:** FIX-09, GO-07, BUILD-05. [Session/server sources](sources.md#assistant-and-native-pi).

<a id="f28"></a>
### F28 — MCP/administration depends on SDK context absent from plain RPC

**P1; confirmed porting dependency, native-tool failure not demonstrated.** Existing MCP operations use synchronous event-bus responses, live registration/dispose handles and, for some calls, `ctx.session.agent.state.tools`. A normal public ExtensionContext does not expose the entire AgentSession/ModelRuntime. A public package export alone does not prove it can be used from every installed distribution/context.

Prove bridge import origin and every needed API early. Keep live handles bridge-owned, use supported adapter operations and native auth/settings implementations, and record exact missing interfaces when blocked. No replacement SDK, private deep imports, second MCP client or model-facing admin prompts. **Work:** BRIDGE-01/02, FC17–FC28. [MCP bridge](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/extensions/mcp-admin-bridge.ts), [connections](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/extensions/mcp-connections.ts), [public context](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/src/core/extensions/types.ts).

<a id="f29"></a>
### F29 — Default origin validation trusts client-supplied Host

**P1; predicate behavior reproduced, browser exploitation not tested.** Without PublicOrigin, ExpectedOrigin constructs its value from request.Host and accepts a matching Origin. WebSocket admission uses that predicate; cookies are conditional on authentication configuration. A valid hostname is not an approved service authority.

The recorded copied-function probe accepted an arbitrary matching hostname; configured PublicOrigin rejected it. It also rejected equivalent default-port forms because normalization differed. Use a configured Host allowlist, separate normalized Origin checks and explicit proxy policy. **Work:** FIX-10, X01. [Auth](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/auth.go), [WebSocket](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/websocket.go).

<a id="f30"></a>
### F30 — Read-only Git inspection can execute a clean filter

**P1; command-policy behavior reproduced.** Disabling external diff/textconv/hooks/fsmonitor/global attributes does not disable repository clean/process filters. A disposable locally configured clean filter created a harmless marker outside its repository under the same command/options/environment.

A remote .gitattributes alone does not configure the executable. The concern is inspection of an admitted existing repository under controller authority. Use verified non-executing object/raw worktree inspection. Label conversion-dependent differences rather than hiding execution or claiming unsupported LFS/encoding fidelity. **Work:** FIX-11, X02, FC30. [Git wrapper](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/workspace/git_exec.go), [diff](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/workspace/git_diff.go), [Git attributes](https://git-scm.com/docs/gitattributes).

<a id="f31"></a>
### F31 — Git timeout does not bound inherited-pipe cleanup

**P1; unmodified helper behavior reproduced.** CommandContext without a descendant and finite pipe-wait policy can leave Run waiting on a child's inherited output pipe. The recorded helper probe returned after about 2.007 seconds despite a 100 ms parent deadline when its filter ran `sleep 2; cat`.

Bound managed groups, escalation and pipe draining. WaitDelay alone is not descendant termination; context cancellation alone is not complete cleanup. Verify the final container's effective reaping behavior. **Work:** FIX-12, BUILD-05, X03. [Wrapper](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/workspace/git_exec.go), [os/exec](https://pkg.go.dev/os/exec).

<a id="f32"></a>
### F32 — Persistence can fail after the primary changes

**P1; confirmed source ordering and earlier plan overstatement, no filesystem failure injected.** persist.Write replaces the primary before opening/synchronizing its directory. A later error can mean a visible new primary with unconfirmed durability, not an unchanged save.

Distinguish known-uncommitted from installed/uncertain outcomes. Retain mutation identity, reconcile the validated primary and block unsafe follow-on effects until resolved. Never replay an execution or restore an old tombstone/ledger to manufacture rollback. Test each publication/sync/reply failure boundary. **Work:** FIX-13, MIG-01, X04. [Persistence](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/persist/store.go).

<a id="f33"></a>
### F33 — Generalizing the registry does not expose module routes

**P1 for module delivery; confirmed integration gap.** HTTPHandler forwards Browser MCP and selected catalog/status routes explicitly. Canvas/Design registrations alone miss that branch, while unknown extensionless paths can fall through to index.html.

Drive top-level MCP/management/artifact routing from registered ownership, reserve core paths, reject overlaps and return non-success for unknown API/MCP paths. Test the assembled HTTP handler, not only the registry. **Work:** ROUTE-01, X05. [Router](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/server.go).

<a id="f34"></a>
### F34 — Request-ID wording conflated protocol boundaries

**P1 for migration; plan defect corrected in contracts.** Browser IDs are strings with ack/resume/replay policy; host v2 IDs are positive safe integers; native correlations are separate optional strings. A universal numeric rule would break identity or replay.

Maintain explicit adapter maps; transport reconnect does not reset durable mutation identity. Negotiate browser, host, native, MCP and URL schemas separately. **Work:** API-02, X06. [Browser protocol](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/websocket.go), [native RPC](sources.md#assistant-and-native-pi).

<a id="f35"></a>
### F35 — Proposed limits did not compose across layers

**P1; source/plan mismatch, no OOM reproduced.** An earlier 24 MiB per-image proposal conflicted with 4.5 MiB individual and 24 MiB aggregate base64 limits. A 64 MiB Design index cannot use the 16 MiB generic store or a 32 MiB host frame. Per-frame/request-count caps also do not bound aggregate decoded memory; current WebSocket parsing precedes in-flight admission.

Compose byte/count/pixel limits, reserve a small control lane, account for aggregate retained buffers and give Design a dedicated bounded index artifact path. Do not raise every shared limit or claim Go controls native whole-response allocation. **Work:** LIMIT-01, X07–X09. [Domain](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/contracts/src/domain.ts), [input](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/session_actions.go), [store](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/persist/store.go).

<a id="f36"></a>
### F36 — Stop and idle release needed observable end states

**P1; lifecycle plan gap.** Native abort does not prove all detached work ended. With finite residency and automatic eviction disabled, users need a safe capacity-release action rather than only view closure or TUI handoff.

Stop pauses dispatch and verifies generation quiescence, terminating the managed generation when cancellation cannot be established. Report forced termination/uncertain external effects. Release idle runtime preserves history/draft/selection and is distinct from Release to TUI. Unknown background work is not idle. **Work:** LIFE-01, GO-05/07, X10/X11. [Native lifecycle](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/src/modes/rpc/rpc-mode.ts).

<a id="f37"></a>
### F37 — Worker profiles and resource scope were inconsistent

**P1 for an enabled affected deployment; plan ambiguity.** Some wording treated stronger Browser isolation as optional while the shared contract implied the same enclosure everywhere. Pre-exec placement, scratch bytes/inodes and controller-versus-worker termination needed explicit requirements.

Disclose the existing Docker Browser profile as non-isolated. Do not silently reuse it directly under the Pi owner. Direct-host Browser needs a tested supported profile; Canvas/Design always require enforced containment. Test resources before untrusted execution, filesystem/network/descriptor exposure and complete cleanup. These controls do not constrain native Pi tools. **Work:** SEC-01/02, X12/X13. [Bubblewrap scope](https://github.com/containers/bubblewrap/security), [systemd controls](https://www.freedesktop.org/software/systemd/man/systemd.resource-control.html).

<a id="f38"></a>
### F38 — Browser upgrades need state-transition acceptance

**P2; target interaction gap, not reproduced application failure.** Old tabs and lazy assets can meet a new server. Build hashes do not define protocol compatibility; blind reload can lose drafts or replay uncertain work.

Negotiate the browser contract separately, stop unsupported mutations, preserve drafts/selection before refresh and recover from missing old assets without blank panels or reload loops. Keep original mutation IDs. **Work:** UI-07, PKG-03, X14. [Browser contracts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/contracts/src/ws-protocol.ts), [assets](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/server.go).

<a id="f39"></a>
### F39 — Invalid Browser configuration can discard restrictive overrides

**P1 when enabled; source finding retained from the other branch, no abuse reproduced.** Browser configuration parsing has a default fallback after an invalid override. That can discard valid restrictive settings mixed with the invalid input.

Invalid configuration must make the affected module unavailable, not start permissive defaults. Keep core/sibling modules and retained-data management usable. Test mixed valid restrictive and invalid values. **Work:** EXT-02, SEC-01. [Registry](sources.md#extensions-and-deployment).

<a id="f40"></a>
### F40 — Final Docker entrypoint bypasses the earlier init

**P1 for lifecycle acceptance; confirmed packaging observation, orphaning not reproduced.** The final ENTRYPOINT replaces an earlier tini entrypoint. Installing/inheriting tini does not establish final-image reaping.

Test the effective final command, SIGTERM, managed descendants and subreaping on both architectures. Keep Docker controller-only; host systemd tests do not prove container lifecycle. **Work:** FIX-12, BUILD-05, PKG-03, X03. [Dockerfile](sources.md#extensions-and-deployment).

<a id="f41"></a>
### F41 — The method constant list omits schedule methods

**P2; confirmed catalog coverage gap, not absent scheduling.** Schedule methods exist in WsMethodMap and the controller but not WS_METHODS. Generating the inventory from that constant alone loses retained behavior.

Inventory dispatchers, typed maps, callers and non-WebSocket surfaces together; preserve existing schedule runner/UI tests. Every used method needs a handler, schema and coverage row. **Work:** API-01, FC29. [Contract](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/contracts/src/ws-protocol.ts), [dispatcher](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/handler.go).

## Recorded isolated probes

These are the third review's recorded results, not new runs or production fixes from this merge.

| Probe | Recorded result | Scope |
| --- | --- | --- |
| Matching unapproved Host/Origin | Accepted by current predicate | Copied Go functions/minimal harness; no browser/DNS exploit |
| Same request with configured local PublicOrigin | Rejected | Control in the same harness |
| Equivalent default-port Host/Origin | Rejected | Normalization mismatch |
| Git policy with harmless configured clean filter | Exit 0 and marker created | Disposable repository; Git 2.47.3; no network/user state |
| Unmodified runGit, 100 ms deadline, two-second filter | Returned after about 2.007 s with timeout outcome | Source-unit harness; blob d58b280d5277ffb6928b89d836d5e73314a9a386 verified |

Implementation tests must exercise the real endpoints/wrappers and invert unsafe expectations. Neither these probes nor reported old CI establish native bridge, service, sandbox or renderer acceptance.

## Canvas and Openfig draft review

The original inputs remain at commit-pinned links in [sources.md](sources.md#canvas-and-openfig-drafts). The reviewed plans are [canvas.md](canvas.md) and [openfig.md](openfig.md). Canvas remains penultimate and Openfig last; neither introduces a mandatory Pi extension, another registry or a new assistant architecture.

### Canvas

Retain disabled-by-default canvas/pixie-canvas at /mcp/canvas; one document per native chat; six tools; full HTML optimistic revisions; mutation retry identity; explicit removal; bounded storage, DOM/text and actual screenshot images; offline Mewa guidance and human-visible iteration. Budgets are governed by contracts.md, not prior benchmark claims.

A shared module bearer plus model-supplied sessionId is insufficient. Use server-issued session authority through the generic native MCP integration. Separate agent-browser sessions alone do not isolate UID/files/network. Give the enclosed renderer immutable authorized input through private IPC, not application cookies or broad authorization headers.

CSP and iframe attributes are defense in depth, not an egress boundary; iframe attributes also do not accompany top-level screenshot navigation. Raster-first live viewing avoids executing generated scripts on the application or user's browser origin. This is live HTML drafting/screenshot iteration, not a promise of direct DOM interaction. Any richer interactive view needs equivalent proven controls.

Reserve quota before committing; key jobs/artifacts by generation/version/viewport/renderer/assets. Account for wire expansion and decoded pixels separately. Disable revokes execution/reads while retaining documents; authorized removal still works. Tombstones prevent stale render/retry/restart resurrection. Reuse the common module routes/UI slots/worker foundation, not Browser's privileged state or user cookies.

### Openfig

Retain disabled-by-default design/pixie-design at /mcp/design; one labelled instance-wide document; source retained until human removal; conflict on a second pending/active upload; read-only model tools; human upload/remove/shared-focus actions; offline headless inspection through public openfig-core. Current chat/project context does not imply private scope.

Use package/internal/design, package/design-worker and package/webui/src/design. The optional application-side worker does not become a requirement for core chat or assistant-only builds. Pin and reproduce the released parser with licensed real fixtures. The inspected parser expands archives/chunks synchronously and compiles the uploaded schema; upload metadata and V8 heap tuning are not hard process bounds.

Store normalized bounded structure with verified sibling order, graph validity, coordinates and direct-versus-unresolved text. Private browsing does not change shared agent focus; explicit shared focus uses a revision. Reindexing validates selection and cannot overwrite a new document generation.

Label the saved cover Document thumbnail. It is not a selected-frame render. The inspected CLI export map did not expose its internal Design rasterizers; do not deep-import private paths, copy a scene engine or silently substitute a cover. Structure and real upstream frame rendering have separate evidence gates, including offline licensed fonts/assets, bounds, cancellation and visual comparisons.

### Prior Openfig experiments

The original draft records core 0.4.1 at `f9f10d0fc7e6ad3dd7ce94fe3e3da3ffb5eec5d6` and CLI 0.6.0 at `0d74102f0cba4139ca14ba2e0f31664139744154`. The package/parser sources were inspected during earlier consolidation. Execution figures below are attributed draft observations, not rerun or capacity/fidelity guarantees.

| Fixture | Source bytes | Reported structure | Saved cover bytes |
| --- | --- | --- | --- |
| basic-shapes.fig | 49,363 | 11 nodes, 2 FRAME nodes, one user page | 20,937 |
| medium-complex.fig | 870,716 | 211 nodes, 12 FRAME nodes, 17 TEXT nodes, three user pages | 108,184 |

The draft reports roughly 63 ms per single parseFig call under Node 24.18.1, excluding import/I/O. Neither fixture exercised embedded images; FRAME counts are not artboard counts. Reported unpacked package sizes of about 488 KB/core and 7.0 MB/CLI are not installed runtime costs.

Other recorded leads: blocked ordinary deep imports, an internal SVG followed by missing WASM, a successful CLI command with zero Design-frame output, implicit font-download concerns and unresolved fixture/font redistribution rights. Reproduce relevant cases against released public artifacts. They establish neither that rendering is impossible nor permission to copy private renderers/fonts.

## Reconciliation of the alternate review IDs

F01–F38 retain the later branch's identifiers. The alternate branch's overlapping R2 findings map below; its three distinct observations are F39–F41. This table preserves source traceability, not a second progress ledger.

| Alternate ID | Consolidated finding |
| --- | --- |
| R2-01 | F14 |
| R2-02 | F20 |
| R2-03 / R2-04 | F28 |
| R2-05 | F23 |
| R2-06 | F22 |
| R2-07 | F41 |
| R2-08 | F21 / F25 |
| R2-09 | F39 |
| R2-10 | F40 |
| R2-11 | F19 |
| R2-12 | F01 / F24 / feature-coverage.md |
| R2-13 | F18 |

The resolved plan uses the later host-v2, FC01–FC33, X01–X14, private inherited bridge-channel, configuration-precedence and continuous-release contracts. It carries forward the alternate branch's detailed migration/rollback procedure in migration.md, adapted to the same identity/storage/persistence rules. It does not keep a parallel compatibility matrix or conflicting manual-only release policy.

## Remaining implementation evidence

Independent Pi distributions and public bridge APIs; the supplied blank-content reproduction; actual contained worker/platform behavior; all final host/Docker artifacts, services and upgrade paths; and real Openfig frame fidelity remain required. A documented resolution is not an implemented fix. Track every outcome, test and blocker in execution.md and retain the original native state during verification.
