import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import upstreamTodo from "@juicesharp/rpiv-todo";
import { registerCapability } from "../capabilities.ts";

// Optional Pi-native replacement for the custom plans extension (plans.ts).
// The upstream factory is used unchanged: it registers the `todo` tool and
// `/todos` command, and replays per-session state from the session branch.
// This bridge only advertises an additive capability marker so operators can
// verify the profile loaded. It adds no tools, prompts, or interception.
// Deprecated-pending-parity: custom update_plan/plans.read remain the writer.
export default function rpivTodoExtension(pi: ExtensionAPI): void {
	upstreamTodo(pi);
	registerCapability(pi, { id: "rpiv-todo", version: 1, operations: {} });
}
