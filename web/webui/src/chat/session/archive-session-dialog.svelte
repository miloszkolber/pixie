<script lang="ts">
import ConfirmDialog from "../../components/confirm-dialog.svelte";
import { getTransport } from "../../connection";
import { appStoreApi } from "../../store";
import type { SessionLifecycleTarget } from "./session-lifecycle";

interface Props {
	target: SessionLifecycleTarget;
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
}
let { target, open = $bindable(false), onOpenChange }: Props = $props();

async function archive(): Promise<void> {
	const requested = target;
	await getTransport().request("session.archive", {
		projectId: requested.projectId,
		sessionId: requested.sessionId,
	});
	appStoreApi.getState().applySessionLifecycle({
		projectId: requested.projectId,
		sessionId: requested.sessionId,
		operation: "archived",
	});
}
</script>

<ConfirmDialog
	bind:open
	title="Archive this chat?"
	description="The agent will retain the conversation. You can restore it from chat history."
	confirmLabel="Archive"
	confirmTestId="session-archive-confirm"
	onConfirm={archive}
	{onOpenChange}
/>
