<script lang="ts">
import type { Snippet } from "svelte";
import Button from "./button.svelte";
import { isChunkLoadError } from "./error-boundary-state";
import Icon from "./icon.svelte";

interface Props {
	children: Snippet;
	label?: string;
}

let { children, label }: Props = $props();

function report(error: unknown): void {
	console.error(`[ErrorBoundary${label ? `: ${label}` : ""}]`, error);
}
</script>

{#snippet failed(error: unknown, reset: () => void)}
	{@const chunkError = isChunkLoadError(error)}
	<div
		data-testid="error-boundary-fallback"
		role="alert"
		class="flex h-full min-h-0 flex-col items-center justify-center gap-sm overflow-auto p-lg text-center"
	>
		<Icon name="triangle-alert" size={24} class="text-feedback-error" />
		<p class="tr-title-compact text-text-default">
			{label ? `The ${label} panel hit an error` : "Something went wrong"}
		</p>
		<p class="max-w-[28rem] tr-text-metadata text-text-muted">
			{chunkError
				? "Failed to load part of the app (a stale or unreachable resource). Reloading usually fixes it."
				: error instanceof Error
					? error.message
					: "An unexpected error occurred while rendering this view."}
		</p>
		{#if chunkError}
			<Button data-testid="error-reload" onclick={() => window.location.reload()}>
				<Icon name="refresh-cw" size={16} /> Reload page
			</Button>
		{:else}
			<Button data-testid="error-retry" onclick={reset}>
				<Icon name="rotate-ccw" size={16} /> Try again
			</Button>
		{/if}
	</div>
{/snippet}

<svelte:boundary onerror={report} {failed}>
	{@render children()}
</svelte:boundary>
