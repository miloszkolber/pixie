import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import upstreamWebTools from "@juicesharp/rpiv-web-tools";
import { registerCapability } from "../capabilities.ts";

// Optional Pi-native replacement for the custom web extension (web.ts).
// The upstream factory is used unchanged: it registers `web_search` and
// `web_fetch` (SearXNG self-hosted default, SSRF guard, spill-to-file).
// This bridge only advertises an additive capability marker so operators can
// verify the profile loaded. It adds no tools, prompts, or interception.
// Enable either `web` or `rpiv-web`, not both: both register `web_fetch`.
// Deprecated-pending-parity: the custom web_fetch remains the default.
export default function rpivWebExtension(pi: ExtensionAPI): void {
	upstreamWebTools(pi);
	registerCapability(pi, { id: "rpiv-web", version: 1, operations: {} });
}
