import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import upstreamAskUserQuestion from "@juicesharp/rpiv-ask-user-question";
import { registerCapability } from "../capabilities.ts";

// Optional Pi-native question tool. The upstream factory is used unchanged:
// it registers the single `ask_user_question` tool, which walks the
// questionnaire through the generic UI bridge (`ui-bridge.ts`) when
// `ctx.mode` is `"rpc"` (sequential select/input dialogs) and emits the
// public `rpiv:ask-user:prompt` / `rpiv:ask-user:blocked` events around the
// wait. This bridge only advertises an additive capability marker so
// operators can verify the profile loaded. It adds no tools, prompts, or
// interception. Enable with e.g. `--extensions mcp,agents,rpiv-ask`.
// This is the single model-facing question tool; Pixie's duplicate
// application-level `ask_user_question` was removed after this profile's
// parity gate passed.
export default function rpivAskExtension(pi: ExtensionAPI): void {
	upstreamAskUserQuestion(pi);
	registerCapability(pi, { id: "rpiv-ask", version: 1, operations: {} });
}
