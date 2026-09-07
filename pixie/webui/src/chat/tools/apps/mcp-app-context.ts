import { getContext, setContext } from "svelte";

export interface McpAppSession {
	readonly projectId: string | undefined;
	readonly sessionId: string;
}

const MCP_APP_SESSION_CONTEXT = Symbol("pixie.mcp-app-session");

export function setMcpAppSessionContext(session: McpAppSession | null): McpAppSession | null {
	return setContext(MCP_APP_SESSION_CONTEXT, session);
}

export function getMcpAppSessionContext(): McpAppSession | null {
	return getContext<McpAppSession | null | undefined>(MCP_APP_SESSION_CONTEXT) ?? null;
}
