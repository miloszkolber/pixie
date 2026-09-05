import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";

// Optional Pi-native Signet memory profile (`--extensions ...,signet`).
//
// The published `@signetai/connector-pi` package ships an installer, not an
// importable factory: `PiConnector.install()` writes the upstream-managed file
// extension `<agentDir>/extensions/signet-pi.js` (marker
// `SIGNET_MANAGED_PI_EXTENSION`), and Pi's resource loader discovers that
// directory automatically for every session. Importing the bundle here would
// register the same tools and daemon lifecycle hooks twice, so this bridge
// intentionally does not import or copy upstream code: the managed file stays
// the single writer, and this profile only advertises an additive capability
// marker so operators can verify the profile loaded. It adds no tools,
// prompts, or interception.
//
// Behavior when the profile is enabled (verified against 0.157.3):
// - Tools `signet_recall`, `signet_source_search`, `signet_session_search`
//   and `signet_remember` appear once the operator installs Signet
//   (`signet setup`); without the managed file no memory tools are added.
// - The daemon lifecycle (session-start, user-prompt-submit, session-end and
//   compaction hooks) is fail-open: an unreachable daemon logs a warning and
//   the session still attaches, while tools answer `daemon_offline`.
// - Auto-recall arrives as hidden custom messages
//   (`<signet-memory source="auto-recall">`, `display: false`); the Pixie
//   projection drops non-user/assistant roles, so recalled context reaches the
//   model through the `context` hook without leaking into the transcript.
// - The daemon endpoint and config stay operator-owned external service
//   state: `SIGNET_DAEMON_URL` (default `http://127.0.0.1:3850`),
//   `~/.pi/agent/extensions/signet.json` (`{enabled}`) and per-session
//   `SIGNET_ENABLED=false`. Pixie never reads or writes them.
// Deprecated-pending-parity: the MCP `signet` connection configured in
// Settings stays the writer until the parity deletion gate in
// docs/roadmap.md is met. Until then both paths may be enabled for
// evaluation; afterwards the MCP path is removed and the daemon remains an
// external service (never a Pixie-managed MCP connection).
export default function signetExtension(pi: ExtensionAPI): void {
	registerCapability(pi, { id: "signet", version: 1, operations: {} });
}
