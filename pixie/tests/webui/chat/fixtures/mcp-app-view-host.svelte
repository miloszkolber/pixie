<script lang="ts">
import type { McpAppAttachment } from "@pixie/contracts";
import { setMcpAppSessionContext } from "@/chat/tools/apps/mcp-app-context";
import McpAppView from "@/chat/tools/apps/mcp-app-view.svelte";

interface Props {
	projectId?: string | undefined;
	sessionId: string;
	toolCallId: string;
	args: Record<string, unknown>;
	result: unknown;
	app?: McpAppAttachment | undefined;
	status: "running" | "done" | "error";
}
let { projectId, sessionId, toolCallId, args, result, app, status }: Props = $props();
setMcpAppSessionContext({
	get projectId() {
		return projectId;
	},
	get sessionId() {
		return sessionId;
	},
});
</script>

<McpAppView {toolCallId} {args} {result} {app} {status} />
