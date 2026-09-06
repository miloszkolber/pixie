# Roadmap

- Additional native Pi extension UI interfaces.
- A dedicated schedules interface.
- Deployment-host performance measurements on both supported Linux architectures.
- Pi-native simplification parity gate: remove the custom `plans` (`update_plan`, `plans.read`, `pixie-plan`, `pixie:plan`) and `web` (`web_fetch`) host extensions once the `rpiv-todo`/`rpiv-web` profiles demonstrate equivalent plan display, search/fetch behavior, SSRF posture, session switching, and reload/compaction recovery.
- Pi-native question parity gate: remove Pixie's application-level `ask_user_question` tool once the `rpiv-ask` profile demonstrates equivalent answers, cancellation and error shapes, session-scoped single-use dialogs with no cross-session answers, timeout and abort handling, session reload recovery, and question presentation for option, custom and multi-select answers. Until then the application-level tool stays; enabling `rpiv-ask` before the gate passes surfaces both tools, so use it only for parity evaluation.
- Pi-native Signet parity gate: remove the MCP `signet` connection (controller settings, session attach, status) once the `signet` profile demonstrates equivalent recall, source and session search, remember round-trip, daemon fail-open on attach and on every tool, hidden auto-recall with no transcript leakage, and session attach with no MCP dependency. Until then the MCP connection stays; enabling both paths is for parity evaluation only.
- Pi-native subagent parity gate: remove the custom `delegate`/`list_agents` execution once the `pi-subagent` profile demonstrates equivalent user/project discovery with trust and project-over-user override, model frontmatter with per-call override, thinking, tool allowlist, no-tools, fresh/cloned initial context, ephemeral/named persistent sessions with continuation, parallel calls, cancellation, usage, child metadata, depth/cycle guards, and structured errors. Live child runs additionally require a Pi-CLI-compatible spawn target: the upstream runner re-invokes the parent entrypoint, which fails fast under the Bun host. Pixie keeps its agent Markdown CRUD, `pi.sources.*` operations, editor and `@agent` presentation; only the execution path is replaced. Until then `delegate` stays; enabling `pi-subagent` before the gate passes surfaces both tools, so use it only for parity evaluation.
- Pi-native MCP parity gate: remove the custom universal MCP client (`@pixie/pi-mcp`, per-tool `<conn>__<tool>` surface) once the `pi-mcp-adapter` profile demonstrates equivalent stdio, Streamable HTTP and SSE transports, lazy discovery with cached schemas, reconnect, cancellation, structured and image content, resources, OAuth/bearer auth, proxy-only Browser runtime registration (first-wins, fail-closed), MCP Apps with inline fallback, runtime registration, and status projection (connected, cached, failed, needs-auth, not-connected, disabled), with Signet independent of MCP and qualitatively comparable startup and reconnect overhead. Until then the custom client stays; enabling `pi-mcp-adapter` before the gate passes surfaces both the per-tool surface and the single `mcp` proxy tool, so use it only for parity evaluation.
- MCP publisher merge gate: remove the separate `pixie-mcp` process and container once the in-process publisher demonstrates equivalent Browser publication (same tools and resources, storage isolation, sandboxing, and bearer auth), persist-store enablement with Tools UI parity (Enabled, Status, Endpoint), deprecated-environment fallback behavior, and dual-run catalog equivalence. Until then both publishers run; Browser stays the only module on either.
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
- Not yet proven live (each needs responsive model inference): todo display/persistence equivalence across settled turns, web_search/fetch behavior, question answer shapes, subagent child runs (plus the known Bun spawn-target gap), Signet recall (managed extension absent, daemon degraded), and compaction/reload recovery with real turns.
- Model-turn results, local `llama.cpp`/`gemma-4-12b` via the new `llama` profile (configure `provider` → `llama.cpp`, `model` → `gemma-4-12b`):
  - Todo: create, list with ids, update status, `blockedBy` dependency, complete, delete, and final filtered list all executed live across turns with state persisting (`CREATED`, count `1`, `dep-child` remaining). Full CRUD + dependencies green.
  - Questions: `ask_user_question` → `pixie:ui:request` (`select` with numbered options) → `session.uiResponse` → model answered `GOT:Red`. Answer contract verified against upstream `parseIndex`: the browser must return the exact offered option line (leading digits), since anything outside the list is a dismissal by design (`User declined...`). Pixie's modal already returns the exact option string.
  - Web: public `web_fetch` green (`FETCHED: Example Domain...`). Custom `web_fetch` fetched loopback `http://127.0.0.1:7312/` successfully (real Pixie HTML in the tool result: **no SSRF guard**); upstream `web_fetch` refused the same URL (`Refusing to fetch private/loopback address: 127.0.0.1`). Decisive web-gate comparison for removing the custom extension.
  - Subagent: live `subagent` child (`explore` agent) completed in 39 s with the correct result (`CHILD-DONE 14`); lifecycle events observed as `agent_start` → … → single `agent_end` → `agent_settled` → `run_end`. No Bun spawn failure on this host.
  - MCP adapter: `adapter.status` live (engine 2.32.1, per-session registration correctly scoped — unregistered servers report "not found"); `mcp connect` to runtime-registered `pixie-browser` succeeds, but proxied `describe`/`call` fail with "server currently unavailable" while the custom MCP path automates the same Browser fine. The custom client stays the writer; the adapter gap needs protocol-level debugging before its gate passes.
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
- [x] In-process MCP publisher registry running dual with the separate host.
- [x] agent-browser verified behind Browser MCP on Chromium (Stage G acceptance).
- [x] Browser engine preference in Pixie app state, defaulting to chromium.
- [x] Obscura evaluated over CDP (rejected as preferred: no Accessibility domain upstream, Chromium stays default and preferred).
- [ ] Custom `plans`/`web` extensions deleted (parity gate above pending).
- [ ] Application-level `ask_user_question` deleted (parity gate above pending).
- [ ] Custom `delegate`/`list_agents` execution deleted (parity gate above pending).
- [ ] Custom universal MCP client deleted (parity gate above pending).
- [ ] MCP `signet` connection deleted (parity gate above pending).
- [ ] Separate `pixie-mcp` process and container deleted with obsolete MCP environment, capability contracts, cross-container coordination, duplicate health/lifecycle, and temp migration code (merge gate above pending).
- [x] Full baseline/parity suite green with consolidated configuration docs (bun suite 339 pass / 1 skip / 0 fail plus all Go packages ok under the pinned Bun 1.4.0; the container default Bun 1.3.14 shows pre-existing SSR-harness failures, so use the pinned toolchain).
