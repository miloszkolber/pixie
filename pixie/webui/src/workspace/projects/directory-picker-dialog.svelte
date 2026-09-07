<script lang="ts">
import type { DirectoryListing } from "@pixie/contracts";
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
	class="max-h-[min(38rem,calc(100vh-2rem))] max-w-[min(42rem,calc(100vw-2rem))]"
	onOpenChange={setOpen}
>
	<div class="flex flex-col gap-md">
		<div class="text-field flex min-w-0 items-center gap-xs">
			<Button
				variant="ghost"
				size="icon"
				aria-label="Go to parent directory"
				disabled={!parent || loading}
				onclick={() => parent && navigate(parent)}
			>
				<Icon name="chevron-left" size={16} />
			</Button>
			<span data-testid="directory-picker-path" class="min-w-0 flex-1 truncate tr-text-metadata">
				{current ?? "Configured directories"}
			</span>
		</div>
		<label class="field flex-row items-center self-start">
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
			class="min-h-40 overflow-auto border border-border-default"
			aria-busy={loading}
		>
			{#if loading}
				<div role="status" class="app-empty app-empty--compact min-h-40">
					<Icon name="loader-circle" size={16} class="animate-spin" /> Loading directories…
				</div>
			{:else if error}
				<div role="alert" class="app-empty app-empty--compact min-h-40 text-feedback-error">
					{error}
				</div>
			{:else if listing?.directories.length}
				<ul aria-label="Directories" class="tree p-2xs">
					{#each listing.directories as directory (directory.path)}
						<li class="tree-item">
							<button
								type="button"
								class="tree-leaf"
								onclick={() => navigate(directory.path)}
							>
								<Icon name={current === null ? "folder" : "folder-open"} size={16} />
								<span class="min-w-0 flex-1 truncate">{directory.name}</span>
							</button>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="app-empty app-empty--compact min-h-40">No directories are available here.</p>
			{/if}
		</div>
		{#if listing && listing.warnings.length > 0}
			<p role="status" class="callout" data-variant="warning">{listing.warnings.join(" ")}</p>
		{/if}
		{#if listing && (listing.page > 0 || listing.hasMore)}
			<div class="flex items-center justify-between gap-sm">
				<Button
					variant="ghost"
					size="sm"
					disabled={listing.page === 0 || loading}
					onclick={() => (page -= 1)}
				>Previous</Button>
				<span class="tr-text-metadata text-text-muted">Page {listing.page + 1}</span>
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
