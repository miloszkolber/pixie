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
- Browser engine gate: prefer Obscura over Chromium only once the compatibility suite (`TestLiveObscuraCompatibility` over `obscura serve --port 9222` with `AGENT_BROWSER_CDP`) demonstrates equivalent connect, navigate, snapshot with refs, click, fill, type, keyboard, scroll, wait, forms, login, screenshots, close, and reset behavior with comparable startup, memory, latency, success, render, and JS-compatibility metrics. Until then Chromium stays the default and the fallback; the engine switch never alters the model-facing API. There is no Tools UI engine toggle yet.
- Subagent fallback order if `mjakl/pi-subagent` cannot reach parity unchanged: first a thin Pixie-side bridge that keeps the upstream tool unchanged (projection and event adaptation in Pixie only), then `nicobailon/pi-subagents`, then a custom implementation. Background/detached runs, steering, workflows, councils, missions, worktrees and external runners stay out of scope.

Current behavior is documented in the [README](../README.md).

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
- [ ] Obscura evaluated over CDP (unevaluated: no binary on this host; suite defined, Chromium fallback kept).
- [ ] Custom `plans`/`web` extensions deleted (parity gate above pending).
- [ ] Application-level `ask_user_question` deleted (parity gate above pending).
- [ ] Custom `delegate`/`list_agents` execution deleted (parity gate above pending).
- [ ] Custom universal MCP client deleted (parity gate above pending).
- [ ] MCP `signet` connection deleted (parity gate above pending).
- [ ] Separate `pixie-mcp` process and container deleted with obsolete MCP environment, capability contracts, cross-container coordination, duplicate health/lifecycle, and temp migration code (merge gate above pending).
- [ ] Full baseline/parity suite green with consolidated configuration docs.
