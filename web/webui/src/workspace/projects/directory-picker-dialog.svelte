<script lang="ts">
import type { DirectoryListing } from "@pixie/shared";
import Button from "../../components/button.svelte";
import Dialog from "../../components/dialog.svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { DIRECTORY_PAGE_SIZE, parentPath } from "./directory-picker";

interface Props {
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
	onSelect: (path: string) => void;
}

let { open = $bindable(false), onOpenChange, onSelect }: Props = $props();
let path = $state<string>();
let page = $state(0);
let includeHidden = $state(false);
let listing = $state<DirectoryListing | null>(null);
let loading = $state(false);
let error = $state<string | null>(null);
let requestId = 0;

let current = $derived(listing?.path ?? null);
let atRoot = $derived(current !== null && listing?.roots.includes(current) === true);
let parent = $derived(current && !atRoot ? parentPath(current) : null);
let canSelect = $derived(current !== null && !loading && error === null);

function setOpen(next: boolean): void {
	open = next;
	onOpenChange?.(next);
}

function navigate(next: string | undefined): void {
	path = next;
	page = 0;
}

$effect(() => {
	if (!open) return;
	path = undefined;
	page = 0;
	includeHidden = false;
	listing = null;
	error = null;
});

$effect(() => {
	if (!open) return;
	const request = ++requestId;
	loading = true;
	error = null;
	void getTransport()
		.request("directory.list", {
			...(path ? { path } : {}),
			page,
			pageSize: DIRECTORY_PAGE_SIZE,
			includeHidden,
		})
		.then((result) => {
			if (request === requestId) listing = result;
		})
		.catch((cause) => {
			if (request === requestId) error = errorText(cause, "Couldn't load directories.");
		})
		.finally(() => {
			if (request === requestId) loading = false;
		});
	return () => {
		requestId += 1;
	};
});
</script>

<Dialog
	bind:open
	title="Choose a project directory"
	description="Only directories under configured Pixie mounts are available."
	class="directory-picker-dialog"
	onOpenChange={setOpen}
>
	<div class="u-flex u-flex-col u-gap-md">
		<div class="text-field u-flex u-min-w-0 u-items-center u-gap-xs">
			<Button
				variant="ghost"
				size="icon"
				aria-label="Go to parent directory"
				disabled={!parent || loading}
				onclick={() => parent && navigate(parent)}
			>
				<Icon name="chevron-left" size={16} />
			</Button>
			<span data-testid="directory-picker-path" class="u-min-w-0 u-flex-1 u-truncate tr-text-metadata">
				{current ?? "Configured directories"}
			</span>
		</div>
		<label class="field directory-picker-dialog__hidden-toggle u-flex u-items-center">
			<input
				type="checkbox"
				class="checkbox"
				checked={includeHidden}
				disabled={loading}
				onchange={(event) => {
					includeHidden = event.currentTarget.checked;
					page = 0;
				}}
			/>
			<Icon name={includeHidden ? "eye" : "eye-off"} size={14} />
			<span class="field-label">Show hidden directories</span>
		</label>
		<div
			class="directory-picker-dialog__listing"
			aria-busy={loading}
		>
			{#if loading}
				<div role="status" class="app-empty app-empty--compact directory-picker-dialog__empty">
					<span class="directory-picker-dialog__spinner u-inline-flex u-shrink-0"><Icon name="loader-circle" size={16} /></span> Loading directories…
				</div>
			{:else if error}
				<div role="alert" class="app-empty app-empty--compact directory-picker-dialog__empty directory-picker-dialog__error">
					{error}
				</div>
			{:else if listing?.directories.length}
				<ul aria-label="Directories" class="tree directory-picker-dialog__tree">
					{#each listing.directories as directory (directory.path)}
						<li class="tree-item">
							<button
								type="button"
								class="tree-leaf"
								onclick={() => navigate(directory.path)}
							>
								<Icon name={current === null ? "folder" : "folder-open"} size={16} />
							<span class="u-min-w-0 u-flex-1 u-truncate">{directory.name}</span>
							</button>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="app-empty app-empty--compact directory-picker-dialog__empty">No directories are available here.</p>
			{/if}
		</div>
		{#if listing && listing.warnings.length > 0}
			<p role="status" class="callout" data-variant="warning">{listing.warnings.join(" ")}</p>
		{/if}
		{#if listing && (listing.page > 0 || listing.hasMore)}
			<div class="u-flex u-items-center u-justify-between u-gap-sm">
				<Button
					variant="ghost"
					size="sm"
					disabled={listing.page === 0 || loading}
					onclick={() => (page -= 1)}
				>Previous</Button>
				<span class="tr-text-metadata u-text-text-muted">Page {listing.page + 1}</span>
				<Button
					variant="ghost"
					size="sm"
					disabled={!listing.hasMore || loading}
					onclick={() => (page += 1)}
				>Next</Button>
			</div>
		{/if}
	</div>
	{#snippet actions()}
		<Button variant="ghost" onclick={() => setOpen(false)}>Cancel</Button>
		<Button disabled={!canSelect} onclick={() => current && onSelect(current)}>
			Select this directory
		</Button>
	{/snippet}
</Dialog>

<style>
	:global(.dialog.directory-picker-dialog) {
		max-height: min(38rem, calc(100vh - 2rem));
		max-width: min(42rem, calc(100vw - 2rem));
	}

	.directory-picker-dialog__hidden-toggle {
		align-self: flex-start;
		flex-direction: row;
	}

	.directory-picker-dialog__listing {
		min-height: 10rem;
		overflow: auto;
		border: 1px solid var(--border-default);
	}

	.directory-picker-dialog__empty {
		min-height: 10rem;
	}

	.directory-picker-dialog__error {
		color: var(--feedback-error);
	}

	.directory-picker-dialog__tree {
		padding: var(--space-2xs);
	}

	.directory-picker-dialog__spinner {
		animation: directory-picker-dialog-spin 1s linear infinite;
	}

	@keyframes directory-picker-dialog-spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.directory-picker-dialog__spinner {
			animation: none;
		}
	}
</style>
