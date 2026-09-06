import { type ExtensionAPI, getAgentDir } from "@earendil-works/pi-coding-agent";
import { piMcpAdapterWithConfig } from "@pixie/pi-host/mcp";

// Persisted package/file-extension imports remain valid. This compatibility
// entry point contains no MCP client, transport, catalog or model tools.
export default function mcp(pi: ExtensionAPI, agentDir = getAgentDir()): void {
	piMcpAdapterWithConfig({ agentDir })(pi);
}
