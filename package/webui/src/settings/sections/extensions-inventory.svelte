<script lang="ts">
import type { NativeExtensionInventory } from "@pixie/contracts";
import Button from "@/components/button.svelte";
import { nativeReaderLabel, nativeStateLabel } from "./extensions-model";

let {
	inventory = null,
	loading = false,
	error = null,
	label,
	busy = null,
	notice = null,
	canRequestReload = false,
	onRefresh = () => {},
	onConfigure = () => {},
	onReload = () => {},
}: {
	inventory?: NativeExtensionInventory | null;
	loading?: boolean;
	error?: string | null;
	label: string;
	onRefresh?: () => void;
	busy?: "saving" | "checking-reload" | null;
	notice?: string | null;
	canRequestReload?: boolean;
	onConfigure?: (resource: NativeExtensionInventory["resources"][number]) => void;
	onReload?: () => void;
} = $props();
</script>

<section class="flex min-w-0 flex-col gap-md" aria-label="Native Pi extensions">
	<div class="flex flex-wrap items-center justify-between gap-sm">
		<h2 class="tr-title-entity">Extensions</h2>
		<Button variant="outline" disabled={loading || Boolean(busy)} onclick={onRefresh}>Refresh inventory</Button>
	</div>
	<p class="tr-text-ui break-words">{label}</p>
	<p class="tr-text-ui text-text-muted">Native Pi configuration and loaded inventory are separate. Save enable/disable choices for the next native load. Saves do not install packages or reload running sessions. MCP connections are managed separately in Tools.</p>
	<p class="tr-text-ui text-text-muted">In-process reload is deferred: the pinned SDK resets global API providers and its default loader cannot guarantee no installation on reopening. Current tools, passive UI and active work remain unchanged. Static CLI resources such as --llama are not editable here.</p>
	{#if canRequestReload}<Button variant="outline" disabled={loading || Boolean(busy)} onclick={onReload}>Check session reload</Button>{/if}
	{#if busy}<p role="status" class="tr-text-ui">{busy === "saving" ? "Saving native configuration…" : "Checking reload safety…"}</p>{/if}
	{#if notice}<p role="status" class="tr-text-ui">{notice}</p>{/if}
	<a class="tr-text-ui underline" href="https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/packages.md" target="_blank" rel="noreferrer">Pi package documentation (opens in a new tab)</a>
	{#if loading}
		<p role="status" class="tr-text-ui">Loading native inventory…</p>
	{:else if error}
		<p role="alert" class="tr-text-ui text-feedback-error">{error}</p>
	{:else if inventory}
		<p role="status" class="tr-text-ui">{nativeReaderLabel(inventory.context.reader)}</p>
		<p class="tr-text-caption break-all text-text-muted">Reader directory: {inventory.context.cwd}</p>
		{#if inventory.trust && !inventory.trust.projectTrusted}
			<p role="status" class="tr-text-ui">Project resources are not trusted here, so project extensions did not load. Record trust through Pi's own project-trust flow to enable them.</p>
		{/if}
		{#if inventory.warnings.length}
			<p role="alert" class="tr-text-ui text-feedback-error">Inventory is incomplete ({inventory.warnings.join(", ")}). Refresh does not change configuration. Check Pi's local diagnostics for details.</p>
		{/if}
		<h3 class="tr-title-entity">Configured packages</h3>
		{#if !inventory.packages.length}<p class="tr-text-ui text-text-muted">No configured packages reported.</p>{/if}
		<ul class="divide-y divide-border-default">
			{#each inventory.packages as pkg}
				<li class="min-w-0 py-sm tr-text-ui">
					<p class="break-all">{pkg.name ?? pkg.source}</p>
					<p class="break-all text-text-muted">{pkg.scope} · Installed version: {pkg.version ?? "unknown"} · {nativeStateLabel(pkg.state)}</p>
					<p class="break-all tr-text-caption">Source: {pkg.source}{pkg.filtered ? " · Native resource filters configured" : ""}</p>
				</li>
			{/each}
		</ul>
		<h3 class="tr-title-entity">Configured extension paths</h3>
		{#if !inventory.paths.length}<p class="tr-text-ui text-text-muted">No explicit extension paths reported. Pi can also discover file extensions automatically.</p>{/if}
		<ul class="divide-y divide-border-default">
			{#each inventory.paths as path}<li class="py-sm break-all tr-text-ui">{path.scope}: {path.path}</li>{/each}
		</ul>
		<h3 class="tr-title-entity">Resolved native resources</h3>
		<p class="tr-text-caption text-text-muted">Pi resolves user/project precedence and resource filters. A configured source can be shadowed, filtered, missing or absent from the current session.</p>
		{#if !inventory.resources.length}<p class="tr-text-ui text-text-muted">No extension resources resolved.</p>{/if}
		<ul class="divide-y divide-border-default">
			{#each inventory.resources as resource}
				<li class="flex flex-col items-start gap-sm py-sm break-all tr-text-ui">
					<p>{resource.path}<br />{resource.scope} · {resource.origin} · {resource.enabled ? "Selected by native configuration" : "Excluded by native configuration"} · {nativeStateLabel(resource.state)}</p>
					{#if resource.configurationSupported && resource.resourceKey && inventory.configurationRevisions && (resource.scope === "user" || (resource.scope === "project" && inventory.context.reader !== "service")) && !inventory.warnings.length}
						<Button variant="outline" disabled={Boolean(busy) || loading} onclick={() => onConfigure(resource)}>{resource.enabled ? "Disable" : "Enable"} on next load ({resource.scope})</Button>
					{:else}<p class="tr-text-caption text-text-muted">Manage this resource through native Pi configuration.</p>
					{/if}
				</li>
			{/each}
		</ul>
		<h3 class="tr-title-entity">Actually loaded extensions</h3>
		{#if !inventory.extensions.length}<p class="tr-text-ui text-text-muted">{inventory.context.reader === "not-resident" || inventory.context.reader === "configured-only" ? "Loaded inventory unavailable in this reader scope." : "No loaded extensions reported."}</p>{/if}
		<ul class="divide-y divide-border-default">
			{#each inventory.extensions as extension}
				<li class="flex min-w-0 flex-col gap-xs py-sm tr-text-ui">
					<p class="break-all">{extension.name ?? extension.path}</p>
					<p class="break-all text-text-muted">{extension.source.scope} · {extension.source.origin} · Version: {extension.version ?? "unknown"} · Web interfaces: unknown</p>
					<p class="break-all tr-text-caption">Source: {extension.source.source} · {extension.resolvedPath}</p>
					<p class="break-words">Tools: {extension.tools.join(", ") || "none"}</p>
					<p class="break-words">Commands: {extension.commands.join(", ") || "none"}</p>
				</li>
			{/each}
		</ul>
		{#if inventory.errors.length}
			<h3 class="tr-title-entity">Load failures</h3>
			<ul>{#each inventory.errors as failure}<li class="py-xs break-all tr-text-ui text-feedback-error">Load failed: {failure.path}. Raw diagnostics remain on the host to protect credentials.</li>{/each}</ul>
		{/if}
	{/if}
</section>
