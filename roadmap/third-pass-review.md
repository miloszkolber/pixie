# Third-pass review

Baseline: `eacfac43e81e04e798f4e8fd43af7d77315ad6ff` on `docs/roadmap-second-pass`, reviewed 8 September 2026. Main remained at `71590cac48925b31b9d5d3c7d1746ee94b351772` during source inspection. This supplements F01–F28; it does not replace their evidence or mark their fixes complete.

The pass followed controller HTTP/WebSocket admission, Git command execution, persistence commit points, module routing and the revised implementation contracts. Two isolated Git probes and three origin-predicate cases were executed. They are described below. The complete application, Pi/bridge, browser automation, systemd units, containers and future renderer were not run.

## F29 — Default origin validation trusts a client-supplied Host

**P1; predicate behavior reproduced, browser exploitation not tested.** Without `PublicOrigin`, `AuthConfig.ExpectedOrigin` constructs the expected origin from `request.Host`. `IsExpectedOrigin` then accepts the matching Origin. The WebSocket handler uses that decision and performs no cookie authentication when controller authentication is disabled. A syntactically valid hostname is not an allowlist of the local service's authorities.

A copied-function Go probe accepted `Host: attacker.example:7312` with matching Origin even with a loopback remote address. A configured public origin rejected it. Another case rejected equivalent default-port forms (`localhost:80` versus `http://localhost`), because only one side is normalized. These are server-policy observations, not a claim that a DNS-rebinding attack succeeds in every browser or deployment.

FIX-10 introduces one configured authority policy before route dispatch. Default local access admits only the documented loopback/localhost authorities and actual listener port. Remote public origins and proxy host rewrites are explicit configuration. Normalize both sides consistently, reject unapproved Host values independently of Origin, and do not trust forwarded headers from arbitrary peers. Preserve authenticated service-to-service calls without a browser Origin under their separate role policy.

Sources: [auth.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/auth.go), [websocket.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/websocket.go), [server.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/server.go).

## F30 — Read-only Git inspection can execute a clean filter

**P1; current command policy reproduced.** `changedArgs` disables external diff and textconv, and `runGit` disables hooks/fsmonitor/global attributes. Those settings do not disable repository clean/process filters. A disposable repository with a tracked text file, `.gitattributes` and a configured clean filter created a marker outside the repository while running the same diff arguments/environment.

No network or real user state was used. The clean filter was deliberately configured in the local fixture; a remote repository's `.gitattributes` alone does not install that `.git/config` command. The problem is that inspecting an admitted existing repository can execute its configured code under the controller account, despite the read-only product boundary.

FIX-11 must use a verified non-executing inspection path. Commit/index object reads and bounded raw worktree reads can provide raw inspection without invoking conversion helpers. Do not fix this by adding another unverified Git flag or executing a filter in the unrestricted controller. Mark conversion-dependent views explicitly when raw bytes differ from native clean-filter semantics; do not claim identical LFS/encoding behavior without tests. Full conversion fidelity needs a separately contained implementation or approved limitation, not hidden execution.

Sources: [git_exec.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/workspace/git_exec.go), [git_diff.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/workspace/git_diff.go), [Git attributes](https://git-scm.com/docs/gitattributes).

## F31 — The Git timeout does not bound inherited-pipe cleanup

**P1; unchanged helper reproduced.** `runGit` uses `exec.CommandContext` without a process-group cleanup policy or `WaitDelay`. Killing Git does not necessarily close pipes held by a spawned child. The unmodified helper, verified by its Git blob hash, returned after about 2.007 seconds with a 100 ms parent deadline when the fixture's filter ran `sleep 2; cat`.

FIX-12 bounds both managed descendants and pipe draining for controller subprocesses, including Git. A context deadline or output-limit cancellation alone is not the whole shutdown contract. Use a managed process group, bounded escalation and finite pipe-wait policy; verify the final container's reaping behavior as well. Do not kill unrelated user processes or describe `WaitDelay` alone as descendant termination.

Sources: [git_exec.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/workspace/git_exec.go), [Go os/exec](https://pkg.go.dev/os/exec).

## F32 — A persistence error can happen after the primary changed

**P1; confirmed source ordering and plan overstatement, no filesystem failure injected here.** `persist.Write` replaces the primary and then opens/synchronizes its directory. A later failure returns an error even though the new primary is visible. The previous blanket requirement that every failed save leaves memory, disk and runtime unchanged is not implementable by merely moving `persist.Write` before the memory assignment.

FIX-13 defines pre-publication failure separately from an installed-but-not-confirmed-durable outcome. Preserve old committed state only when non-publication is known. After an ambiguous/post-rename failure, retain mutation identity, report persistence uncertainty, re-read/reconcile the validated primary and block unsafe follow-on effects. No success acknowledgment before required durability. Do not retry an execution, resurrect a tombstone or overwrite a newer primary from an old backup.

Tests must inject errors before and after backup replacement, primary replacement, directory synchronization and response delivery. The same rule applies to module settings, schedules, outbox, Canvas revisions and Design slot pointers.

Source: [persist/store.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/persist/store.go).

## F33 — Generalizing the registry alone does not expose new module routes

**P1 for module delivery; confirmed integration gap.** The top-level `HTTPHandler.ServeHTTP` sends only Browser MCP paths and two catalog/status paths to the registry. A registered `/mcp/canvas` or `/mcp/design` would still miss that branch. Extensionless unknown paths otherwise fall through to the SPA document.

ROUTE-01 makes the top-level HTTP router consume registered route ownership, including management/artifact surfaces, without one branch per module. Reserve core routes, reject overlapping registrations and unknown API/MCP paths, and retain authentication before expensive work. Test actual HTTP dispatch, not just registry methods. An HTTP 200 containing index.html is not an initialized MCP server.

Source: [server.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/server.go).

## F34 — The shared request-ID description conflated different boundaries

**P1 for protocol migration; plan defect.** The prior identity table called request IDs positive safe integers without limiting that rule to host v2. The browser currently uses string IDs, acknowledgments/resume and replay fingerprints; native RPC has its own optional string correlation IDs. Translating all three to the host representation would break replay or accidentally reuse identities.

API-02 keeps distinct browserRequestId, hostRequestId and nativeRequestId types and mappings. Durable mutation/delivery IDs survive reconnect; transport correlations do not. Browser protocol compatibility is negotiated independently of host v2 and URL schema v2. Test strings such as `001`, reused IDs on a new connection and conflicting mutation payloads without coercion.

Sources: [browser transport](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/websocket.go), [native RPC](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/docs/rpc.md), baseline contracts.md.

## F35 — The proposed limits did not compose across the pipeline

**P1 for resource/input compatibility; source and plan mismatch, no OOM reproduced.** The proposed 24 MiB per-image limit disagreed with the controller's 4.5 MiB individual and 24 MiB aggregate base64 limits. A 64 MiB Design index cannot use the generic 16 MiB JSON store unchanged or be sent in a 32 MiB host frame. Per-frame and request-count limits also do not establish a process-wide memory budget; the current WebSocket handler materializes/parses a message before its in-flight admission check.

LIMIT-01 preserves named per-image/aggregate/wire units, adds aggregate admission accounting and a reserved small control path, and distinguishes browser payloads from large internal index artifacts. Retain the 16 MiB generic store limit; use a dedicated bounded index artifact path for Design. Do not raise every transport/storage cap to 64 MiB. Source-native whole-response allocation remains a separate upstream constraint.

Sources: [domain.ts](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/contracts/src/domain.ts), [session_actions.go](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/session_actions.go), [WebSocket admission](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/websocket.go), [JSON store](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/persist/store.go).

## F36 — Stop and idle release needed separate, observable end states

**P1 for lifecycle acceptance; plan gap.** A native abort is not evidence that arbitrary detached extension work has ended. With automatic idle eviction disabled and a finite resident cap, the UI also needs an ordinary idle-runtime release action rather than relying only on closing a view or a TUI handoff.

LIFE-01 defines Stop as pausing dispatch/continuation and reaching a verified quiescent generation, with managed-child termination when remaining work cannot be verified cancelled. Report forced termination and uncertain external effects. Never present forwarded cancellation as proof that an external operation was undone. Provide Release idle runtime at the resident limit without deleting or archiving the session, and distinguish it from Release to TUI. Unknown background liveness cannot be relabelled idle to admit another child.

Sources: contracts.md and assistant-go.md at the review baseline; [native RPC implementation](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/src/modes/rpc/rpc-mode.ts). Exact extension-specific cancellation remains a live conformance test.

## F37 — Worker requirements had inconsistent Browser and resource scope

**P1 for an enabled affected deployment; plan ambiguity.** Some files described stronger Browser isolation as optional while the common contract appeared to require the same enclosure for every module. Namespace/cgroup labels also left scratch-disk/inode limits, pre-exec placement and controller-versus-worker termination implicit.

The contract now distinguishes the explicitly disclosed existing Docker Browser profile from contained processing. The existing profile is not called isolated; it is not silently reused under the Pi owner's direct-host account. A supported direct-host Browser profile must establish its actual execution boundary. Canvas/Design always require their tested enclosure; neither may weaken it to match the older Browser profile.

SEC-02 verifies resource placement before executing untrusted input, network/filesystem exposure, bounded scratch/output/inodes, process cleanup and unavailable-profile diagnostics. Its prerequisites remain optional for core chat. Scope containment to untrusted workers, never accidentally to native Pi's intended tools. A cooperative namespace launcher is not proof that the target OS permits the configuration.

Sources: baseline contracts.md, extensions.md and security-and-validation.md; [bubblewrap security scope](https://github.com/containers/bubblewrap/security), [systemd resource controls](https://www.freedesktop.org/software/systemd/man/systemd.resource-control.html).

## F38 — Release upgrades lacked a browser-state transition test

**P2; target interaction gap, not a reproduced application failure.** New server protocol/assets can meet old open browser tabs during either deployment's upgrade. A commit-based build ID alone does not say whether those tabs can mutate safely, and a blind reload can lose unsent text or attempt to replay an ambiguous request.

UI-07 tests old/new frontend-server combinations independently of host v1/v2. Preserve local drafts and selection before offering reload; never replay a mutation as fresh work just because the bundle changed. Unsupported peers get an explicit refresh/compatibility state. A lazy-loaded old asset returning 404 gets recovery, not an empty main panel. Keep credentials/transcripts out of layout/upgrade metadata.

Sources: [browser protocol](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/contracts/src/ws-protocol.ts), [static asset serving](https://github.com/miloszkolber/pixie/blob/eacfac43e81e04e798f4e8fd43af7d77315ad6ff/package/internal/controller/server.go), baseline builds-and-releases.md and workspace-ui.md.

## Executed probes

| Probe | Actual result | Scope |
| --- | --- | --- |
| Matching unapproved Host/Origin | Accepted by the copied current predicate | Go unit harness with four source functions and minimal scaffolding; no browser/DNS exploit |
| Same request with configured local PublicOrigin | Rejected | Control case in the same harness |
| Explicit default Host port versus normalized Origin | Equivalent origins rejected | Current predicate normalization case |
| Current Git command/options/environment and harmless clean filter | Exit 0; marker created | Disposable local repository, Git 2.47.3; not complete application execution |
| Unmodified runGit with 100 ms parent deadline and two-second filter | Returned after 2.007 seconds with timeout outcome | Go source-unit harness; original blob `d58b280d5277ffb6928b89d836d5e73314a9a386` verified before execution |

These probes confirm existing behavior; their passing assertions are not fixes. The later acceptance tests invert the relevant unsafe expectations. No source, dependency, workflow, live service or native user state was changed by running them. No released-binary/systemd/renderer result is implied.

## Integration and remaining evidence

Requirements now live in contracts.md and extensions.md; [acceptance.md](acceptance.md) maps cross-boundary cases to existing feature rows and new execution tasks. Read reviews for evidence, not as later overrides of those contracts. Both host builds, commit-named Docker releases, the six-column layout and final Canvas/Openfig sequence are unchanged.

Full integration evidence is still required for native bridge APIs, the supplied empty-content screenshot, actual worker/platform enforcement, release artifacts and Openfig frame fidelity. This pass identifies additional issues and testable resolutions; it does not certify the absence of other bugs.
