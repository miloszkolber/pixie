import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import upstreamWebTools from "@juicesharp/rpiv-web-tools";
import { registerCapability } from "../capabilities.ts";

// Pi-native web search and fetch. The upstream factory is used unchanged: it
// registers `web_search` and `web_fetch` (SearXNG self-hosted default, SSRF
// guard, spill-to-file). This bridge only advertises an additive capability
// marker so operators can verify the profile loaded. It adds no tools,
// prompts, or interception. The custom `web_fetch` extension was removed
// after this profile's parity gate passed.
export default function rpivWebExtension(pi: ExtensionAPI): void {
	upstreamWebTools(pi);
	registerCapability(pi, { id: "rpiv-web", version: 1, operations: {} });
}
