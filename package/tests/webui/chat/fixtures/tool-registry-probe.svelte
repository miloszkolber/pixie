<script lang="ts">
import { DefaultToolRenderer, getToolRenderer, getToolSummary } from "@/chat/render/tool-registry";
import "@/chat/tools/register";

interface Props {
	name: string;
	compareWith?: string | undefined;
	args?: Record<string, unknown>;
}
let { name, compareWith, args = {} }: Props = $props();
let renderer = $derived(getToolRenderer(name));
let comparison = $derived(compareWith ? getToolRenderer(compareWith) : undefined);
let summary = $derived(
	getToolSummary(name, {
		toolCallId: "probe",
		toolName: name,
		args,
		result: undefined,
		status: "running",
		streaming: false,
	}),
);
</script>

<div
	data-testid="tool-registry-probe"
	data-default={renderer === DefaultToolRenderer}
	data-same={comparison ? renderer === comparison : undefined}
	data-summary={summary}
></div>
