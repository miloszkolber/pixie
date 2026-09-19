<script lang="ts">
import type { NativeExtensionInventory } from "@pixie/shared";
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

<section class="u-flex u-min-w-0 u-flex-col u-gap-md" aria-label="Native Pi extensions">
	<div class="u-flex u-flex-wrap u-items-center u-justify-between u-gap-sm">
		<h2 class="tr-title-entity">Extensions</h2>
		<Button variant="outline" disabled={loading || Boolean(busy)} onclick={onRefresh}>Refresh inventory</Button>
	</div>
	<p class="extension-text tr-text-ui">{label}</p>
	<p class="tr-text-ui u-text-text-muted">Native Pi configuration and loaded inventory are separate. Save enable/disable choices for the next native load. Saves do not install packages or reload running sessions. MCP connections are managed separately in Tools.</p>
	<p class="tr-text-ui u-text-text-muted">In-process reload is deferred: the pinned SDK resets global API providers and its default loader cannot guarantee no installation on reopening. Current tools, passive UI and active work remain unchanged. Static CLI resources such as --llama are not editable here.</p>
	{#if canRequestReload}<Button variant="outline" disabled={loading || Boolean(busy)} onclick={onReload}>Check session reload</Button>{/if}
	{#if busy}<p role="status" class="tr-text-ui">{busy === "saving" ? "Saving native configuration…" : "Checking reload safety…"}</p>{/if}
	{#if notice}<p role="status" class="tr-text-ui">{notice}</p>{/if}
	<a class="extensions-doc-link tr-text-ui" href="https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/packages.md" target="_blank" rel="noreferrer">Pi package documentation (opens in a new tab)</a>
	{#if loading}
		<p role="status" class="tr-text-ui">Loading native inventory…</p>
	{:else if error}
		<p role="alert" class="tr-text-ui u-text-feedback-error">{error}</p>
	{:else if inventory}
		<p role="status" class="tr-text-ui">{nativeReaderLabel(inventory.context.reader)}</p>
		<p class="extension-value tr-text-caption u-text-text-muted">Reader directory: {inventory.context.cwd}</p>
		{#if inventory.trust && !inventory.trust.projectTrusted}
			<p role="status" class="tr-text-ui">Project resources are not trusted here, so project extensions did not load. Record trust through Pi's own project-trust flow to enable them.</p>
		{/if}
		{#if inventory.warnings.length}
			<p role="alert" class="tr-text-ui u-text-feedback-error">Inventory is incomplete ({inventory.warnings.join(", ")}). Refresh does not change configuration. Check Pi's local diagnostics for details.</p>
		{/if}
		<h3 class="tr-title-entity">Configured packages</h3>
		{#if !inventory.packages.length}<p class="tr-text-ui u-text-text-muted">No configured packages reported.</p>{/if}
		<ul class="extension-list">
			{#each inventory.packages as pkg}
				<li class="u-min-w-0 u-py-sm tr-text-ui">
					<p class="extension-value">{pkg.name ?? pkg.source}</p>
					<p class="extension-value u-text-text-muted">{pkg.scope} · Installed version: {pkg.version ?? "unknown"} · {nativeStateLabel(pkg.state)}</p>
					<p class="extension-value tr-text-caption">Source: {pkg.source}{pkg.filtered ? " · Native resource filters configured" : ""}</p>
				</li>
			{/each}
		</ul>
		<h3 class="tr-title-entity">Configured extension paths</h3>
		{#if !inventory.paths.length}<p class="tr-text-ui u-text-text-muted">No explicit extension paths reported. Pi can also discover file extensions automatically.</p>{/if}
		<ul class="extension-list">
			{#each inventory.paths as path}<li class="extension-value u-py-sm tr-text-ui">{path.scope}: {path.path}</li>{/each}
		</ul>
		<h3 class="tr-title-entity">Resolved native resources</h3>
		<p class="tr-text-caption u-text-text-muted">Pi resolves user/project precedence and resource filters. A configured source can be shadowed, filtered, missing or absent from the current session.</p>
		{#if !inventory.resources.length}<p class="tr-text-ui u-text-text-muted">No extension resources resolved.</p>{/if}
		<ul class="extension-list">
			{#each inventory.resources as resource}
				<li class="extension-value u-flex u-flex-col u-items-start u-gap-sm u-py-sm tr-text-ui">
					<p>{resource.path}<br />{resource.scope} · {resource.origin} · {resource.enabled ? "Selected by native configuration" : "Excluded by native configuration"} · {nativeStateLabel(resource.state)}</p>
					{#if resource.configurationSupported && resource.resourceKey && inventory.configurationRevisions && (resource.scope === "user" || (resource.scope === "project" && inventory.context.reader !== "service")) && !inventory.warnings.length}
						<Button variant="outline" disabled={Boolean(busy) || loading} onclick={() => onConfigure(resource)}>{resource.enabled ? "Disable" : "Enable"} on next load ({resource.scope})</Button>
					{:else}<p class="tr-text-caption u-text-text-muted">Manage this resource through native Pi configuration.</p>
					{/if}
				</li>
			{/each}
		</ul>
		<h3 class="tr-title-entity">Actually loaded extensions</h3>
		{#if !inventory.extensions.length}<p class="tr-text-ui u-text-text-muted">{inventory.context.reader === "not-resident" || inventory.context.reader === "configured-only" ? "Loaded inventory unavailable in this reader scope." : "No loaded extensions reported."}</p>{/if}
		<ul class="extension-list">
			{#each inventory.extensions as extension}
				<li class="u-flex u-min-w-0 u-flex-col u-gap-xs u-py-sm tr-text-ui">
					<p class="extension-value">{extension.name ?? extension.path}</p>
					<p class="extension-value u-text-text-muted">{extension.source.scope} · {extension.source.origin} · Version: {extension.version ?? "unknown"} · Web interfaces: unknown</p>
					<p class="extension-value tr-text-caption">Source: {extension.source.source} · {extension.resolvedPath}</p>
					<p class="extension-text">Tools: {extension.tools.join(", ") || "none"}</p>
					<p class="extension-text">Commands: {extension.commands.join(", ") || "none"}</p>
				</li>
			{/each}
		</ul>
		{#if inventory.errors.length}
			<h3 class="tr-title-entity">Load failures</h3>
			<ul>{#each inventory.errors as failure}<li class="extension-value u-py-xs tr-text-ui u-text-feedback-error">Load failed: {failure.path}. Raw diagnostics remain on the host to protect credentials.</li>{/each}</ul>
		{/if}
	{/if}
</section>

<style>
	.extensions-doc-link {
		text-decoration-line: underline;
	}

	.extension-list > * + * {
		border-top: 1px solid var(--border-default);
	}

	.extension-value {
		word-break: break-all;
	}

	.extension-text {
		overflow-wrap: break-word;
	}
</style>
