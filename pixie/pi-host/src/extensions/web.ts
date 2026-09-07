import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { registerCapability } from "../capabilities.ts";

export default function webExtension(pi: ExtensionAPI): void {
	registerCapability(pi, { id: "web", version: 1, operations: {} });
	pi.registerTool({
		name: "web_fetch",
		label: "Fetch web page",
		description:
			"Read an HTTP(S) resource. Use the Browser MCP tools for pages that require JavaScript.",
		parameters: Type.Object({ url: Type.String() }),
		execute: async (_id, p, signal) => {
			const url = new URL(p.url);
			if (!["http:", "https:"].includes(url.protocol)) throw new Error("HTTP(S) URL required");
			const response = await fetch(url, {
				signal: signal
					? AbortSignal.any([signal, AbortSignal.timeout(30000)])
					: AbortSignal.timeout(30000),
			});
			if (!response.ok) throw new Error(`HTTP ${response.status}`);
			const reader = response.body?.getReader();
			if (!reader) throw new Error("Empty response");
			const decoder = new TextDecoder();
			let size = 0,
				result = "",
				truncated = false;
			try {
				while (true) {
					const { done, value } = await reader.read();
					if (done) break;
					size += value.byteLength;
					if (size > 1024 * 1024) {
						truncated = true;
						break;
					}
					result += decoder.decode(value, { stream: true });
				}
				result += decoder.decode();
			} finally {
				await reader.cancel();
			}
			return {
				content: [{ type: "text", text: result }],
				details: { url: response.url, truncated },
			};
		},
	});
}
