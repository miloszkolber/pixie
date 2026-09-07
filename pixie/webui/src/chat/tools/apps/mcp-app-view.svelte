<script lang="ts">
import Button from "../../../components/button.svelte";
import Dialog from "../../../components/dialog.svelte";
import Icon from "../../../components/icon.svelte";
import type { ToolRenderProps } from "../../render/tool-registry";
import { getMcpAppSessionContext } from "./mcp-app-context";
import AppFrame, { type AppFrameHandle } from "./mcp-app-frame.svelte";
import { canOpenMcpApp } from "./mcp-app-view";

type Props = Pick<ToolRenderProps, "toolCallId" | "args" | "result" | "app" | "status">;
let { toolCallId, args, result, app, status }: Props = $props();
const session = getMcpAppSessionContext();
let available = $derived(!!session?.projectId && !!session.sessionId && canOpenMcpApp(app, status));
let open = $state(false);
let trigger = $state<HTMLSpanElement>();
let frame = $state<AppFrameHandle>();
let closing: Promise<void> | null = null;

async function close(): Promise<void> {
	if (closing) return closing;
	const pending = (async () => {
		try {
			await frame?.close();
		} finally {
			open = false;
		}
	})();
	closing = pending;
	try {
		await pending;
	} finally {
		if (closing === pending) closing = null;
	}
}

function changeOpen(next: boolean): void {
	if (next) {
		if (!closing) open = true;
		return;
	}
	void close();
}
</script>

{#if available && app}
	<span bind:this={trigger} class="inline-flex">
		<Button variant="outline" size="sm" data-testid="mcp-app-open" onclick={() => changeOpen(true)}>
			<Icon name="app-window" size={12} />Open app
		</Button>
	</span>
	<Dialog
		bind:open
		title={app.toolName}
		description={`Interactive view from ${app.extensionName}`}
		testid="mcp-app-dialog"
		class="h-[min(48rem,calc(100vh-2rem))] max-w-[min(72rem,calc(100vw-2rem))] overflow-hidden"
		onOpenChange={changeOpen}
		onClosedAutoFocus={() => trigger?.querySelector("button")?.focus()}
	>
		{#if open}
			<AppFrame bind:this={frame} {app} {toolCallId} {args} {result} {status} onRequestClose={close} />
		{/if}
	</Dialog>
{/if}
