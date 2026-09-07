import { createRequire } from "node:module";
import type { ExtensionAPI, ExtensionFactory } from "@earendil-works/pi-coding-agent";

// Direct upstream factories for transport/tool unit parity. Native discovery
// integration tests configure these packages in Pi settings instead.
const require = createRequire(import.meta.url);
export const piSubagent: ExtensionFactory = require("@mjakl/pi-subagent").default;
export function piMcpAdapterWithConfig(options?: {
	config?: Record<string, unknown>;
	agentDir?: string;
}): ExtensionFactory {
	return require("pi-mcp-adapter").createMcpAdapter(
		options?.config ? { config: options.config } : {},
	) as (pi: ExtensionAPI) => void;
}
