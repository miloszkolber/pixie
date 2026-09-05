import { createRequire } from "node:module";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";

// Optional Pi-native subagent profile (`--extensions ...,pi-subagent`). The
// upstream factory is used unchanged: it discovers Markdown agents from the
// user directory (`~/.pi/agent/agents/*.md`, or `$PI_CODING_AGENT_DIR/agents`)
// and the project directory (`.pi/agents/*.md`, only when the project is
// trusted, with project definitions overriding user ones), then registers the
// single `subagent` tool (`calls` array, up to 8 calls, 4 concurrent). Each
// call runs as an isolated `pi` child process with depth and cycle guards.
// This bridge only advertises an additive capability marker so operators can
// verify the profile loaded. It adds no tools, prompts, or interception.
//
// The factory is loaded through `createRequire` instead of a static import:
// the package ships raw TypeScript importing untyped JavaScript helpers, and
// a static import would pull those sources into Pixie's strict typecheck.
// The runtime module is identical either way; only the type visibility
// changes, and the call below passes the live `pi` object through untouched.
//
// The existing `agents` extension (agents.ts) stays the writer for Pixie's
// agent Markdown CRUD (`pi.sources.*`), the `list_agents`/`delegate` tools
// and the Web UI editor: both implementations read the same Markdown files,
// so definitions created in Pixie are visible to `subagent` discovery.
// `delegate` execution is kept intact until the parity gate in
// docs/roadmap.md passes; enabling `pi-subagent` before then surfaces both
// tools, so use it only for parity evaluation.
//
// Projection verdict: the normal Pi `onUpdate` partials plus the final
// `result.details` (`{kind: "pi-subagent", projectAgentsDir, results,
// failed}`) are sufficient for the web-native child-run representation, and
// the controller's generic `tool_call_update` path already carries them to
// the `subagent-card` renderer, which maps each `SingleResult` onto the
// shared child-run shape (agent, call index, prompt, model, thinking,
// session handle, child session ID, state, usage, final output, error/stop
// reason). No host interception is added for this.
//
// Lifecycle gaps that remain upstream (not implemented here): the upstream
// `SingleResult` echoes the effective model but not the thinking level, and
// carries no elapsed time or per-tool child activity (only aggregate usage
// and progress counts). If richer cards need them, the minimal upstreamable
// proposal is versioned bus events `pi-subagent:started`, `pi-subagent:updated`
// and `pi-subagent:finished` with JSON payloads `{version: 1, callIndex,
// agent, session {handle, id} | null, state, model?, thinkingLevel?,
// usage?, elapsedMs?, error?}` transported like the existing
// `rpiv:ask-user:*` events. Until such events exist, elapsed time stays a
// client-side derivation and child tool activity stays unavailable.
export default function piSubagentExtension(pi: ExtensionAPI): void {
	const load = createRequire(import.meta.url)("@mjakl/pi-subagent") as {
		default: (pi: ExtensionAPI) => void;
	};
	load.default(pi);
	registerCapability(pi, { id: "pi-subagent", version: 1, operations: {} });
}
