<script lang="ts">
import type {
	PiExtensionCatalog,
	PiExtensionSummary,
	PiSessionExtensionSummary,
	PiToolSummary,
	McpGatewayCatalog,
	McpGatewayModule,
} from "@pixie/contracts";
import { onDestroy } from "svelte";
import Button from "@/components/button.svelte";
import { errorText, getTransport } from "@/connection";
import { appStore, selectActiveContentTab, selectActiveProjectArea } from "@/store";
import {
	extensionWarningText,
	filterTools,
	isSessionInventoryCurrent,
	uniqueExtensions,
} from "./pi-tools-settings";

let catalog = $state<PiExtensionCatalog | null>(null);
let gatewayCatalog = $state<McpGatewayCatalog | null>(null);
let extensions = $state<PiSessionExtensionSummary[]>([]);
let tools = $state<PiToolSummary[]>([]);
let loadedSessionTarget = $state<string | null>(null);
let query = $state("");
let loading = $state(true);
let busy = $state<string | null>(null);
let error = $state<string | null>(null);
let loadSequence = 0;
let mounted = true;

let activeArea = $derived(selectActiveProjectArea($appStore));
let activeTab = $derived(activeArea ? selectActiveContentTab($appStore, activeArea.id) : null);
let activeProjectId = $derived(activeArea?.projectId ?? null);
let activeSessionId = $derived(activeTab?.kind === "chat" ? activeTab.sessionId : null);
let activeTarget = $derived(`${activeProjectId ?? ""}\0${activeSessionId ?? ""}`);
let configuredNames = $derived(
	new Set(catalog?.configured.map((extension) => extension.name) ?? []),
);
let sessionNames = $derived(new Set(extensions.map((extension) => extension.name)));
let knownSessionExtensions = $derived(
	uniqueExtensions([...(catalog?.configured ?? []), ...(catalog?.available ?? [])]).filter(
		(extension) => !sessionNames.has(extension.name),
	),
);
let visibleTools = $derived(filterTools(tools, query));
let hasActiveChat = $derived(activeProjectId !== null && activeSessionId !== null);
let sessionInventoryCurrent = $derived(
	hasActiveChat && isSessionInventoryCurrent(loadedSessionTarget, activeTarget, loading),
);
let mcpAvailable = $derived($appStore.agentProfile?.capabilities?.mcp === 1);
let sessionMcpAvailable = $derived(
	activeSessionId
		? ($appStore.sessions[activeSessionId]?.capabilities ?? $appStore.agentProfile?.capabilities)
				?.mcp === 1
		: false,
);
let warning = $derived(extensionWarningText(catalog?.warningCount ?? 0));

async function load(): Promise<void> {
	const sequence = ++loadSequence;
	const target = activeTarget;
	const projectId = activeProjectId;
	const sessionId = activeSessionId;
	loading = true;
	error = null;
	loadedSessionTarget = null;
	const [nextCatalog, nextGatewayCatalog, nextExtensions, nextTools] = await Promise.allSettled([
		mcpAvailable ? getTransport().request("pi.extensionList", {}) : Promise.resolve(null),
		mcpAvailable || sessionMcpAvailable
			? getTransport().request("mcpGateway.catalog", {})
			: Promise.resolve(null),
		projectId && sessionId && sessionMcpAvailable
			? getTransport().request("session.extensionList", { projectId, sessionId })
			: Promise.resolve([]),
		projectId && sessionId
			? getTransport().request("session.toolList", { projectId, sessionId })
			: Promise.resolve([]),
	]);
	if (!mounted || sequence !== loadSequence || target !== activeTarget) return;
	if (nextCatalog.status === "fulfilled") catalog = nextCatalog.value;
	if (nextGatewayCatalog.status === "fulfilled") gatewayCatalog = nextGatewayCatalog.value;
	if (nextExtensions.status === "fulfilled") extensions = nextExtensions.value;
	if (nextTools.status === "fulfilled") tools = nextTools.value;
	if (
		projectId &&
		sessionId &&
		nextExtensions.status === "fulfilled" &&
		nextTools.status === "fulfilled"
	)
		loadedSessionTarget = target;
	const failures = [nextCatalog, nextGatewayCatalog, nextExtensions, nextTools].filter(
		(result) => result.status === "rejected",
	);
	error = failures.length
		? "Some tool settings could not be refreshed. Successful results are retained; retry to refresh the rest."
		: null;
	loading = false;
}

$effect(() => {
	void activeTarget;
	void load();
});

onDestroy(() => {
	mounted = false;
	loadSequence += 1;
});

function sessionParams(): { projectId: string; sessionId: string } {
	if (!activeProjectId || !activeSessionId) throw new Error("An active chat is required");
	return { projectId: activeProjectId, sessionId: activeSessionId };
}

async function mutate(key: string, action: () => Promise<unknown>): Promise<void> {
	const target = activeTarget;
	busy = key;
	error = null;
	try {
		await action();
		await load();
	} catch (cause) {
		if (target === activeTarget) error = errorText(cause);
	} finally {
		if (mounted) busy = null;
	}
}

function gatewayModuleLabel(module: McpGatewayModule): string {
	if (module.binding === "conflict") return "Conflict";
	if (module.binding === "unavailable") return "Pi unavailable";
	if (module.binding === "enabled") return "Enabled in Pi";
	if (module.binding === "disabled") return "Disabled in Pi";
	return "Not configured in Pi";
}

function gatewayActionLabel(module: McpGatewayModule): string {
	return module.binding === "enabled" ? "Disable" : "Use in Pi";
}

function setGatewayEnabled(module: McpGatewayModule, enabled: boolean): void {
	void mutate(`gateway:${module.id}`, () =>
		getTransport().request("mcpGateway.moduleSetPiEnabled", {
			moduleId: module.id,
			enabled,
			...(gatewayCatalog?.gateway.revision ? { revision: gatewayCatalog.gateway.revision } : {}),
		}),
	);
}
</script>

{#snippet ExtensionLabel(extension: PiExtensionSummary)}
	<div class="min-w-0">
		<div class="break-words text-text-default tr-text-ui">
			{extension.displayName ?? extension.name}
		</div>
		{#if extension.description}
			<p class="break-words text-text-muted tr-text-metadata">{extension.description}</p>
		{/if}
	</div>
{/snippet}

{#snippet ToolInventory(tool: PiToolSummary)}
	<div
		class="card flex flex-wrap items-start justify-between gap-sm p-sm"
		data-testid="tool-inventory"
	>
		<div class="min-w-0">
			<div class="break-words text-text-default tr-text-ui">{tool.name}</div>
			<p class="break-words text-text-muted tr-text-metadata">
				{tool.description || "No description supplied by Pi."}
				{tool.parameters.length ? ` Parameters: ${tool.parameters.join(", ")}` : ""}
			</p>
		</div>
	</div>
{/snippet}

<div data-testid="settings-pi-tools" class="flex flex-col gap-lg">
	<div class="flex flex-wrap items-start justify-between gap-sm">
		<div>
			<h3 class="tr-title-section text-text-default">Extensions and tools</h3>
			<p class="text-text-muted tr-text-metadata">
				Pi supplies the active tools. Compatible extensions supply additional capabilities.
			</p>
		</div>
		<Button size="sm" variant="outline" disabled={loading || busy !== null} onclick={() => void load()}>
			Refresh
		</Button>
	</div>
	{#if error}<p role="alert" class="text-feedback-error tr-text-metadata">{error}</p>{/if}
	{#if busy}
		<p role="status" aria-live="polite" class="text-text-muted tr-text-metadata">
			Updating Pi settings…
		</p>
	{/if}

	{#if mcpAvailable || sessionMcpAvailable}
	<section class="flex flex-col gap-sm" aria-labelledby="mcp-modules-heading">
		<div>
			<h4 id="mcp-modules-heading" class="tr-text-eyebrow text-text-muted">
				Pixie MCP modules
			</h4>
			<p class="text-text-muted tr-text-metadata">
				Published modules stay available in Pixie MCP when disabled here.
 {#if !mcpAvailable}Global module configuration requires a global MCP extension.{/if}
			</p>
		</div>
		{#if loading && !gatewayCatalog}
			<p role="status" class="text-text-muted tr-text-metadata">Checking MCP host…</p>
		{:else if gatewayCatalog?.gateway.state === "not-configured"}
			<p class="text-text-muted tr-text-metadata">
				Pixie MCP is not configured. Configure the MCP host to publish Browser tools.
			</p>
		{:else if gatewayCatalog?.gateway.state === "unreachable" || gatewayCatalog?.gateway.state === "incompatible"}
			<p role="status" class="text-text-muted tr-text-metadata">
				{gatewayCatalog.gateway.detail ?? "Pixie MCP is unavailable."}
			</p>
		{/if}
		{#if gatewayCatalog?.modules.length === 0 && gatewayCatalog.gateway.state === "ready"}
			<p class="text-text-muted tr-text-metadata">No MCP modules are published.</p>
		{:else}
			{#each gatewayCatalog?.modules ?? [] as module (module.id)}
				<div class="card flex flex-wrap items-center justify-between gap-sm p-sm" data-testid="mcp-module-row">
					{@render ExtensionLabel({
						name: module.extensionName,
						displayName: module.displayName,
						description: module.description,
						type: "mcp",
					})}
					<div class="flex min-w-0 flex-wrap items-center gap-xs">
						<div class="min-w-0 tr-text-metadata text-text-muted">
<span>{gatewayModuleLabel(module)}</span>
<p>{module.detail ?? `Host module: ${module.state}`}</p>
{#if module.bindingDetail}<p>{module.bindingDetail}</p>{/if}
<p>{sessionInventoryCurrent ? sessionNames.has(module.extensionName) ? "Present in the current session" : "Not in the current session" : "Current-session membership not checked"}</p>
</div>
						<Button
							size="sm"
							variant="outline"
						disabled={
								!mcpAvailable || busy !== null ||
								module.binding === "conflict" ||
								module.binding === "unavailable" ||
								(module.state !== "ready" && module.binding !== "enabled")
							}
							aria-label={`${gatewayActionLabel(module)} ${module.displayName}`}
							onclick={() => setGatewayEnabled(module, module.binding !== "enabled")}
						>
							{gatewayActionLabel(module)}
						</Button>
					</div>
				</div>
			{/each}
		{/if}
	</section>

 {/if}
 {#if mcpAvailable}
	<section class="flex flex-col gap-sm" aria-labelledby="global-extensions-heading">
		<div>
			<h4 id="global-extensions-heading" class="tr-text-eyebrow text-text-muted">
				Global extensions
			</h4>
			<p class="text-text-muted tr-text-metadata">Changes persist in Pi configuration.</p>
		</div>
		{#if loading}
			<p role="status" class="text-text-muted tr-text-metadata">Loading extensions…</p>
		{/if}
		{#if !loading && catalog?.configured.length === 0}
			<p class="text-text-muted tr-text-metadata">No extensions are configured in Pi.</p>
		{/if}
		{#if warning}<p class="text-text-muted tr-text-metadata">{warning}</p>{/if}
		{#each catalog?.configured ?? [] as extension (extension.configKey ?? extension.name)}
			<div class="card flex flex-wrap items-center justify-between gap-sm p-sm">
				{@render ExtensionLabel(extension)}
				<div class="flex gap-xs">
					<Button
						size="sm"
						variant="outline"
						disabled={!extension.configKey || loading || busy !== null}
						aria-label={`${extension.enabled ? "Disable" : "Enable"} ${extension.displayName ?? extension.name}`}
						onclick={() =>
							void mutate(`enable:${extension.configKey}`, () =>
								getTransport().request("pi.extensionSetEnabled", {
									configKey: extension.configKey ?? "",
									enabled: !extension.enabled,
								}),
							)}
					>
						{extension.enabled ? "Disable" : "Enable"}
					</Button>
					<Button
						size="sm"
						variant="ghost"
						disabled={!extension.configKey || loading || busy !== null}
						aria-label={`Remove ${extension.displayName ?? extension.name}`}
						onclick={() =>
							void mutate(`remove:${extension.configKey}`, () =>
								getTransport().request("pi.extensionRemove", {
									configKey: extension.configKey ?? "",
								}),
							)}
					>
						Remove
					</Button>
				</div>
			</div>
		{/each}
		{#each (catalog?.available ?? []).filter(
			(extension) =>
				(extension.type === "builtin" || extension.type === "platform") &&
				!configuredNames.has(extension.name),
		) as extension (extension.name)}
			<div class="card flex flex-wrap items-center justify-between gap-sm p-sm">
				{@render ExtensionLabel(extension)}
				<Button
					size="sm"
					disabled={loading || busy !== null}
					aria-label={`Add ${extension.displayName ?? extension.name}`}
					onclick={() =>
						void mutate(`add:${extension.name}`, () =>
							getTransport().request("pi.extensionAdd", {
								name: extension.name,
								enabled: true,
							}),
						)}
				>
					Add
				</Button>
			</div>
		{/each}
	</section>

	{/if}
	<section class="flex flex-col gap-sm" aria-labelledby="session-tools-heading">
		<h4 id="session-tools-heading" class="tr-text-eyebrow text-text-muted">Active chat tools</h4>
		{#if !hasActiveChat}
			<p class="text-text-muted tr-text-metadata">
				Open a chat in the current project to manage its effective extensions and tools.
			</p>
		{:else if !sessionInventoryCurrent}
			<p role="status" class="text-text-muted tr-text-metadata">
				{loading
					? "Loading active chat extensions and tools…"
					: "Active chat tools are unavailable. Refresh to try again."}
			</p>
		{:else}
			<p class="text-text-muted tr-text-metadata">
				Session connections add their tools to this chat.
			</p>
			<div class="flex flex-wrap gap-sm">
				{#each extensions as extension (extension.extensionKey)}
					<div class="card flex items-center gap-xs p-xs">
						<span class="text-text-default tr-text-ui">
							{extension.displayName ?? extension.name}
						</span>
						<Button
							size="sm"
							variant="ghost"
							disabled={busy !== null}
							aria-label={`Remove ${extension.displayName ?? extension.name} from active chat`}
							onclick={() =>
								void mutate(`session-remove:${extension.extensionKey}`, () =>
									getTransport().request("session.extensionRemove", {
										...sessionParams(),
										extensionKey: extension.extensionKey,
									}),
								)}
						>
							Remove
						</Button>
					</div>
				{/each}
				{#each knownSessionExtensions as extension (extension.name)}
					<Button
						size="sm"
						variant="outline"
						disabled={busy !== null}
						onclick={() =>
							void mutate(`session-add:${extension.name}`, () =>
								getTransport().request("session.extensionAdd", {
									...sessionParams(),
									name: extension.name,
								}),
							)}
					>
						Add {extension.displayName ?? extension.name}
					</Button>
				{/each}
			</div>
			<label class="field text-text-default tr-text-ui">
				Search tools
				<input class="text-field-input" bind:value={query} />
			</label>
			{#if !loading && visibleTools.length === 0}
				<p class="text-text-muted tr-text-metadata">No tools match this chat.</p>
			{/if}
			{#each visibleTools as tool (tool.name)}
				{@render ToolInventory(tool)}
			{/each}
		{/if}
	</section>
</div>
