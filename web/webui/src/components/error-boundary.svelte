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
		class="error-boundary-fallback u-flex u-h-full u-min-h-0 u-flex-col u-items-center u-justify-center u-gap-sm u-overflow-auto u-text-center"
	>
		<Icon name="triangle-alert" size={24} class="u-text-feedback-error" />
		<p class="tr-title-compact u-text-text-default">
			{label ? `The ${label} panel hit an error` : "Something went wrong"}
		</p>
		<p class="error-boundary-message tr-text-metadata u-text-text-muted">
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

<style>
	.error-boundary-fallback {
		padding: var(--space-lg);
	}

	.error-boundary-message {
		max-width: calc(var(--size-2800) * 4);
	}
</style>
