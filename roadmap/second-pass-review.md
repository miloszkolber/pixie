# Second-pass review

Baseline: main at `71590cac48925b31b9d5d3c7d1746ee94b351772`. That commit adds a newer, unnumbered roadmap; runtime sources remain at the first review's baseline. This pass uses the newer main tree, not the older `docs/implementation-roadmap` branch, and preserves its accepted plans.

This was a static source/contract review. The application, native bridges, systemd services, rendering workers and final binaries were not executed here. First-pass green CI and prior Openfig experiments are not new test results. The selected-chat/empty-panel screenshot still requires reproduction. The findings below supplement F01–F18 in [repository-review.md](repository-review.md).

## Additional findings

### F19 — Assistant publication assertions reference absent package files

**P1, confirmed source inconsistency.** `.github/workflows/npm-publish.yml` checks `scripts/apply-patches.mjs` and `patches/pi-subagent-3.0.1-bun-rpc-entry.patch`. The current assistant package carries `patches/apply-patches.mjs` and its SDK export patch; the subagent patch is workspace-owned. The packed-file guard does not match current distribution. This is not a claim that a release was attempted during this review.

Carry the legacy packaging regression until legacy retirement, then remove obsolete npm publication under PKG-02. Do not port stale artifact assertions into the Go pipeline. Test final archives/manifest contents and the commit-named image as one release set. [Workflow](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/.github/workflows/npm-publish.yml), [package](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/package.json).

### F20 — Optional administration is hardcoded as core compatibility

**P1, confirmed migration blocker.** `PiClient.initialize` requires both sessions and providers, sets Administration and several operation flags true, and `CallPiUntilDone` gates all Pi-specific methods on Administration. A vanilla RPC-backed service without the old provider API can therefore be rejected even when chat works.

API-01/API-02 must replace these assumptions with per-operation contextual capabilities and a core-only compatibility profile. Test native model selection/readable sessions with web login/MCP administration absent. Do not fix only shell.svelte. [Controller](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/package/internal/controller/pi_client.go).

### F21 — Ephemeral combined-mode transport conflicts with deletion recovery

**P1, confirmed existing binding plus target-design conflict.** The existing deletion binding hashes logical agent, endpoint, secret and connection policy. The previous full-host plan proposed a new private endpoint/secret each boot. With that formula, restart changes recovery identity even for the same native installation.

The new [durable authority contract](contracts.md#identity-and-durable-authority) separates paired authority from dialing details and requires explicit versioned migration. Keep old records blocked when their prior binding cannot be verified. Do not remove the authentication condition, silently discard the journal or replay against a new host. API-03/MIG-01 test restart, credential rotation, mode switching and foreign authority. [Binding](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/package/internal/controller/pi_client.go), [journal](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/package/internal/controller/session_deletions.go).

### F22 — Global thinking preference rejects a native level

**P2, confirmed capability mismatch.** `providers.ts` accepts default thinking levels only through xhigh. The inspected Pi 0.85.1 RPC documents max for supporting models. Session choices can be dynamic while the separate global preference path rejects the same level.

Use native supported levels/validation, not a duplicated six-item enum or an inferred ranking that silently lowers intent. FIX-06/FC10/FC19 test supported max, unsupported model values and future advertised values. [Preferences](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/providers.ts), [native levels](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/docs/rpc.md).

### F23 — Agent editing lacks stale-form/external-writer preconditions

**P2, confirmed missing precondition; no overwrite reproduced.** The controller serializes its own agent mutations, but requests do not carry an expected content revision. The host lists/reads a source and then replaces it; creation uses an existence check followed by replacement. That controller mutex does not coordinate an external Pi/editor write or prevent a stale browser form overwriting newer content.

FIX-07/FC23 add expected revision, native-path identity/no-follow checks, exclusive creation and conflict responses. Test a concurrent external edit and two stale client forms. Preserve unspecified frontmatter; do not mistake controller serialization for filesystem compare-and-set. [Controller](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/package/internal/controller/pi_agents.go), [host authoring](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/agents.ts).

### F24 — Raw RPC is not the current UI bridge's full observable surface

**P1, confirmed native implementation gap.** Pi 0.85.1 rpc-mode.ts makes setWorkingMessage a no-op and getEditorText return an empty string. Its select/input/confirm abort/timeout cleanup does not output a request-specific cancellation frame. Forwarding native frames alone cannot establish the existing custom bridge's precise lifecycle/working-hint behavior.

FC13–FC16 and BRIDGE-03 distinguish supported plain RPC, exact native lifecycle additions and existing terminal-only limitations. Public span hooks must be matched by actual identity, not timing. If the required public hook is absent, the precise fidelity row stays blocked until a supported bridge/upstream change or explicit approved reduction. Do not use a simulated event fixture to claim the native feature exists. [Native RPC implementation](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/src/modes/rpc/rpc-mode.ts), [current bridge](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/extensions/ui-bridge.ts).

### F25 — Ungrouped sessions require storage changes, not just sidebar filters

**P1, confirmed target migration gap.** Existing queue/deletion records carry required ProjectID; deletion validation rejects an empty project. The requested ungrouped-session model cannot be implemented solely by hiding a project label.

STATE-01/MIG-01 introduce authority/session associations with optional project metadata, preserve native cwd and keep filesystem admission independent. Convert durable records without losing uncertain dispatch or deletion claims. Schedules remain project-scoped. Do not invent a hidden all-files project to satisfy old validators. [Queues](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/package/internal/controller/session_queues.go), [deletions](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/package/internal/controller/session_deletions.go).

### F26 — The assistant's bind option is not loopback-enforced

**P1, confirmed configuration/documentation mismatch.** Production main accepts --host and forwards it to Bun. The service authenticates and refuses browser Origin, but it does not constrain that bind to loopback. A default loopback address is not a prohibition on remote binding.

FIX-08/GO-01 enforce the target literal-loopback-only host API and validate ports/arguments before startup. Remote UI uses the controller's separate authenticated policy. Keep the current behavior described accurately until changed. [Entrypoint](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/main.ts), [server](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/server.ts).

### F27 — A bounded abort is not a bounded service shutdown

**P1, confirmed uncovered shutdown paths; no hang reproduced.** sessions.ts bounds native abort, but construction/opening, extension shutdown and capability closure can still wait. server.close also awaits all in-flight operations. There is no enclosing service-wide deadline in those paths.

FIX-09/GO-07/BUILD-05 enforce the 25-second application drain and 30-second service ceiling across all subsystems, with process-tree fallback and explicit uncertain outcomes. Tests include hanging extension teardown/construction and administrative I/O, not only a stalled provider. [Sessions](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/sessions.ts), [server](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/server.ts).

### F28 — Existing MCP administration uses SDK session access absent from plain RPC

**P1, confirmed porting dependency, not evidence of a failing native tool.** Some operations in mcp-connections.ts locate the proxy through `ctx.session.agent.state.tools` and execute it directly. An ordinary native extension context does not automatically expose the old assistant's AgentSession object. Registering through a public adapter event does not establish support for every administration operation.

BRIDGE-02/FC26 must map every operation to a supported adapter API. Native MCP tools remain owned by the native client. No second Go client, private deep imports or generated tool prompts may fill the gap invisibly. [MCP connection bridge](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/assistant/src/extensions/mcp-connections.ts), [native extension context](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/src/core/extensions/types.ts).

## Plan corrections made in this pass

The release plan now defines one sha-<12> identity across Git tags, GitHub Releases, both builds and multi-architecture Docker. It defines candidate staging, complete-set promotion, collision/immutable retry behavior, full revision/digest verification, concurrent latest promotion and standing authorization after initial policy approval. Existing diagnostic BuildInfo accepts non-semver text; do not invent a need to compare commit hashes as versions. [Build metadata source](https://github.com/miloszkolber/pixie/blob/71590cac48925b31b9d5d3c7d1746ee94b351772/package/internal/diagnostics/build.go).

Feature coverage is now explicit in 33 rows with vanilla/admin/MCP/module profiles, bridge delivery/authority, unsupported public API handling and mandatory legacy-retirement gates. The prior phrase about Go parity is replaced by that executable acceptance policy.

Shared contracts now resolve previously open implementation choices: protocol v2, epoch/authority identities, outbox/Stop behavior, pending UI semantics, ungrouped metadata conversion, first-run state, exact config precedence, process/I/O bounds, five-mode geometry and worker/job defaults. Native whole-history response allocation remains an upstream boundary; Go chunking is not presented as solving it.

Canvas and Design retain the reviewed product scope. Concrete initial renderer/parser budgets and containment prerequisites are now centralized. Required containment cannot be deferred by claiming same-UID process separation. Actual frame-renderer availability and exact native RPC lifecycle hooks remain explicit external/API gates, not claims this review can prove away.

The three superseded planning files under docs/ are removed, not replaced with stubs. Live incoming references are updated; historical pinned URLs remain evidence. Runtime documentation stays under docs/ because it serves a different purpose. Root guidance is reconciled with the canonical roadmap instead of leaving the old SDK-only instructions in force.

## Review coverage and remaining evidence

Second-pass source reads included current roadmap files, provider/agent administration, controller capability/deletion binding, queue/deletion schemas, MCP bridge, packaging assertions, root docs, native RPC implementation/public exports/types and the requested release reference. Runtime sources were unchanged by the intervening main roadmap commit.

This does not certify absence of all bugs. It gives each known discrepancy a source, owner, test and cutover consequence. Required evidence still includes independent Pi profiles, bridge feasibility, the screenshot reproduction, actual service/worker behavior, all four binary archives and both image platforms. Record those under execution.md when run.

## Sources

Pinned source links accompany each finding; [sources.md](sources.md) retains first-pass/draft evidence. The requested [llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases) were checked: their build-number tags illustrate source-bound releases but are not a hash scheme. Pixie's hash scheme is an explicit project decision. [GitHub immutable release documentation](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases) and [draft-first publication guidance](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository) support staging all assets before immutable publication.
