# Roadmap

- Additional native Pi extension UI interfaces.
- A dedicated schedules interface.
- Deployment-host performance measurements on both supported Linux architectures.
- Pi-native simplification parity gate (met): the custom `plans` (`update_plan`/`plans.read`) and `web` (`web_fetch`) host extensions are deleted after the `rpiv-todo`/`rpiv-web` profiles demonstrated equivalent plan display, search/fetch behavior, SSRF posture, session switching, and reload recovery; legacy `pixie-plan` transcript entries still render.
- Pi-native question parity gate (met): Pixie's application-level `ask_user_question` tool is deleted; `rpiv-ask` demonstrated equivalent answers (including the exact-option answer contract), cancellation and error shapes, session-scoped single-use dialogs, timeout/abort handling, reload recovery, and question presentation. Upstream `ask_user_question` is the single question tool.
- Pi-native Signet parity gate (met, 2026-09-06): the managed extension demonstrated live recall through the running daemon (`signet_recall` returned 3 memories) plus the four memory tools with fail-open behavior and no MCP dependency. The MCP `signet` connection, Signet controller settings, `signet.status` and the Signet settings screen were removed; the daemon stays an operator-owned external service.
- Pi-native subagent parity gate (met, 2026-09-06): the `pi-subagent` profile demonstrated equivalent user/project discovery with trust and project-over-user override, model frontmatter with per-call override, thinking, tool allowlist, no-tools, fresh/cloned initial context, ephemeral/named persistent sessions with continuation, parallel calls, cancellation, usage, child metadata, depth/cycle guards, and structured errors. A live child run (`explore` agent, local gemma-4-12b, 39 s, correct result) also settled the earlier Bun spawn-target concern on this host. Pixie keeps its agent Markdown CRUD, `pi.sources.*` operations, editor and `@agent` presentation; only the execution path was replaced.
- Pi-native MCP client gate (met with the permitted local patch): both profile names use the pinned upstream adapter, and the custom transport is removed. A 113-line upstreamable patch supplies raw resource and App-origin tool APIs without changing model visibility or adding a protocol engine. Retained App authority, metadata/images, cancellation, native projection/reload, legacy configuration, profile aliases and idle cleanup are covered by SDK/host/controller tests. The old package/file entry remains a compatibility shim. See [MCP client verification](mcp-client-verification.md) for reproducible checks, measurements and deployment limitations.
- MCP publisher merge gate (met, 2026-09-06): the in-process publisher demonstrated equivalent Browser publication (same tools and resources, storage isolation, sandboxing, and bearer auth), persist-store enablement with Tools UI parity (Enabled, Status, Endpoint), deprecated-environment fallback behavior, and dual-run catalog equivalence. The separate `pixie-mcp` process, container and controller gateway were deleted; the main Pixie image is browser-capable and publishes Browser itself on :7312. Browser stays the only module.
- Browser engine gate: Obscura was evaluated from its published CDP interface and rejected as the preferred backend (upstream documents Target, Page, Runtime, DOM, Network, Fetch, IO, Storage, Input, and LP domains with no Accessibility domain, which agent-browser snapshots and element refs require). Chromium stays the default and preferred engine with Obscura available for experimentation only. There is no Tools UI engine toggle yet.
- Subagent fallback order if `mjakl/pi-subagent` cannot reach parity unchanged: first a thin Pixie-side bridge that keeps the upstream tool unchanged (projection and event adaptation in Pixie only), then `nicobailon/pi-subagents`, then a custom implementation. Background/detached runs, steering, workflows, councils, missions, worktrees and external runners stay out of scope.

Current behavior is documented in the [README](../README.md).

## Extension UI bridge parity

Generic hardening toward the reference web client, adapted to Pixie's split host/controller/browser processes. No reference code is imported, proxied, or depended on.

| Pattern | Pixie state |
| --- | --- |
| Server owns session, subscriptions, pending UI, run, cancel, binding, idle | Host `Sessions` owns `AgentSession`, the bridge, run IDs, and cancel; the controller owns projection, pending dialogs, and eviction; the browser only projects |
| Binding supplies the UI implementation once per session | `bindExtensions({ uiContext, mode: "rpc" })`; no post-load extension patching |
| Blocking UI replays to late subscribers; vanishes only on answered/cancelled/timed-out/stopped/destroyed | Host re-publishes on `session.load`, snapshots carry `pendingDialogs`, controller deduplicates by request ID |
| Stop unwinds blocked UI on all paths | Prompt cancel, archive, delete, and shutdown dismiss modals and settle host calls |
| First `agent_end` is never final | Prompt RPC settlement is authoritative; stale `agent_start` and late chunks are dropped when no run is open |
| Reconciliation against server state on reconnect | Snapshot carries pending tools and dialogs; the browser rehydrates open chats on reconnect |
| Fork is a new independent session; branches stay in-session | `session.fork` copies history into a fresh session file without mutating the source; `navigate_tree` branches are not exposed |
| Per-session liveness delays idle cleanup only | Generic `RegisterSessionLiveness` guards eviction; never schedules work |
| Status, widget, title, working-message projection; terminal members no-op | `ui_status`, `ui_widget`, `ui_title`, `ui_working` events; editor text, `custom` factories, and TUI chrome stay unprojected |
| Subagents through normal tooling; headless children | Tool start/update/end with child metadata; no nested top-level modals |

Omitted with reasons: periodic 15s state polling (the event stream plus reconnect rehydration already reconciles, so polling adds no signal); widget visuals (transport and per-key state exist, but a widget rail is new product surface); editor-text push (it would clobber the composer); in-session branch navigation UI (Pi semantics preserved, fork covers the new-chat case); `custom` component rendering (needs a TUI; upstream questionnaires already fall back to the select/input walker).

## Live host verification (2026-09-06; model turns on local llama.cpp/gemma-4-12b unless noted)

Against `pixie-pi-host.service` (Pi 0.85.1, host Bun, all seven upstream profiles enabled) and the Compose stack:

- `runtime.hello` reports protocol 1 with all 13 capability markers; `session.create`/`session.list` round-trip with cross-connection persistence.
- Browser over the custom MCP path: `mcp.attach` ok, `open`/`snapshot`/`screenshot`/`close` all completed on `https://example.com` with stable element refs (`ref=e1`, `ref=e2`) and a screenshot artifact URL; `plans.read` returns entries on a live session.
- Session lifecycle: `fork` yields a new independent session id, `session.load` reloads with messages, `archive` and `session.delete` succeed; all probe sessions removed afterwards.
- Remaining live gaps: full-scale compaction (probe transcripts stay below the compaction threshold; settled-semantics events verified live instead), production OAuth keyring validation, and interactive browser/image acceptance beyond the automated Chromium suite.
- Model-turn results, local `llama.cpp`/`gemma-4-12b` via the new `llama` profile (configure `provider` → `llama.cpp`, `model` → `gemma-4-12b`):
  - Todo: create, list with ids, update status, `blockedBy` dependency, complete, delete, and final filtered list all executed live across turns with state persisting (`CREATED`, count `1`, `dep-child` remaining). Full CRUD + dependencies green.
  - Questions: `ask_user_question` → `pixie:ui:request` (`select` with numbered options) → `session.uiResponse` → model answered `GOT:Red`. Answer contract verified against upstream `parseIndex`: the browser must return the exact offered option line (leading digits), since anything outside the list is a dismissal by design (`User declined...`). Pixie's modal already returns the exact option string.
  - Web: public `web_fetch` green (`FETCHED: Example Domain...`). Custom `web_fetch` fetched loopback `http://127.0.0.1:7312/` successfully (real Pixie HTML in the tool result: **no SSRF guard**); upstream `web_fetch` refused the same URL (`Refusing to fetch private/loopback address: 127.0.0.1`). Decisive web-gate comparison for removing the custom extension.
  - Subagent: live `subagent` child (`explore` agent) completed in 39 s with the correct result (`CHILD-DONE 14`); lifecycle events observed as `agent_start` → … → single `agent_end` → `agent_settled` → `run_end`. No Bun spawn failure on this host.
  - MCP adapter: the earlier model-driven "server currently unavailable" result does not establish an upstream transport bug. Local authenticated SDK fixtures execute successfully using `{connect: "server"}`, `{describe: "server_tool"}` and `{server: "server", tool: "tool", args: {...}}`. The remaining raw resource/Apps contract gap is described in the MCP parity gate above.
  - Compaction: `/compact` on small sessions correctly refuses (`Nothing to compact (session too small)`); full compaction needs a larger transcript than probe scale allows.
- Free-tier verdict (2026-09-06, opencode `muse-spark-1.2-contributor-free` as configured default): one turn executed the upstream `todo` tool live (`toolResult "Created #1: live-parity-probe (pending)"`, transcript persisted), then stalled mid-stream for 24+ minutes; a trivial follow-up prompt stayed unsettled after 3+ minutes. The free tier from this host is too slow/flaky for gate evidence; use a responsive provider for the remaining live checks.
- Robustness finding (fixed): when a provider stream stalls, `session.cancel` hung at `await s.abort()` and `session.load` on the affected session hung with it. `AgentSession.abort()` is now bounded (10 s): Stop always resolves with `{ok:true, aborted}`, pending dialogs are still torn down, a `run_abort_timeout` diagnostic event carries the run id, and a late `run_end` still reconciles by run id. Covered by `sessions.test.ts` (stall simulation + teardown bound + clean-abort path). Restarting the host remains the recovery for a wedged provider stream itself.

## Pi-native simplification definition of done

Reconstructed from the simplification plan; profiles are added, deletions wait on their gates.

- [x] `rpiv-todo` profile with dual-read plan projection.
- [x] `rpiv-web` profile with search/fetch behavior and SSRF posture.
- [x] `rpiv-ask` profile with the generic Pi UI bridge.
- [x] `signet` Pi profile with fail-open lifecycle hooks and hidden auto-recall.
- [x] `pi-subagent` profile with the Pixie-side bridge and projections.
- [x] `pi-mcp-adapter` profile with proxy-only Browser runtime registration.
- [x] In-process MCP publisher running in the main Pixie process (Stage F merge: separate host deleted).
- [x] agent-browser verified behind Browser MCP on Chromium (Stage G acceptance).
- [x] Browser engine preference in Pixie app state, defaulting to chromium.
- [x] Obscura evaluated over CDP (rejected as preferred: no Accessibility domain upstream, Chromium stays default and preferred).
- [x] Custom `plans` (`update_plan`/`plans.read`/`pixie:plan`) and `web` (`web_fetch`) extensions deleted (parity gate met); legacy `pixie-plan` transcript entries still render.
- [x] Application-level `ask_user_question` model tool deleted (parity gate met); upstream `ask_user_question` is the single question tool with the generic UI bridge.
- [x] `PIXIE_MCP_MODULES`/`PIXIE_MCP_DISABLED_MODULES` retired: persisted Tools UI state is authoritative, legacy vars are ignored with a startup warning.
- [x] Todo plan projection gated on successful, finished results (failed/partial snapshots never replace the plan).
- [x] App views open/close through the in-process Browser module on an isolated loopback sandbox origin; external `PIXIE_BROWSER_URL` proxying unchanged.
- [x] Browser child processes carry no controller secrets (scoped HOME/TMPDIR, regression-tested).
- [x] Custom `delegate`/`list_agents` execution deleted (parity gate met).
- [x] Custom universal MCP transport deleted, with the permitted upstreamable host API patch and compatibility entry described above.
- [x] MCP `signet` connection, Signet settings and `signet.status` deleted (parity gate met).
- [x] Separate `pixie-mcp` process, container, gateway and `PIXIE_MCP_URL` deleted; the controller publishes Browser in-process (merge gate met).
- [x] Separate `pixie-mcp` process and container deleted with obsolete MCP environment, capability contracts, cross-container coordination, duplicate health/lifecycle, and temp migration code (merge gate met; `PIXIE_MCP_TOKEN` remains for in-process publisher auth and adapter registration).
- [x] Full baseline/parity suite green with consolidated configuration docs (bun suite 339 pass / 1 skip / 0 fail plus all Go packages ok under the pinned Bun 1.4.0; the container default Bun 1.3.14 shows pre-existing SSR-harness failures, so use the pinned toolchain).
