<script lang="ts">
import type { BrowserMCPStatus } from "@pixie/shared";
import Button from "@/components/button.svelte";
import Icon from "@/components/icon.svelte";
import {
	type BrowserMcpBusy,
	type BrowserMcpDraft,
	browserMcpReachabilityLabel,
	browserMcpRegistrationLabel,
	browserMcpServerLabel,
	browserMcpToolSummary,
} from "./browser-mcp-settings";

interface Props {
	draft: BrowserMcpDraft;
	status: BrowserMCPStatus | null;
	loading: boolean;
	busy: BrowserMcpBusy;
	error: string | null;
	notice: string | null;
	connected: boolean;
	onName: (value: string) => void;
	onUrl: (value: string) => void;
	onEnabled: (value: boolean) => void;
	onSave: () => void;
	onRemove: () => void;
	onRefresh: () => void;
}

let {
	draft,
	status,
	loading,
	busy,
	error,
	notice,
	connected,
	onName,
	onUrl,
	onEnabled,
	onSave,
	onRemove,
	onRefresh,
}: Props = $props();

let disabled = $derived(!connected || busy !== null);
let serverLabel = $derived(browserMcpServerLabel(status));
</script>

<div
	data-testid="browser-mcp-settings"
	class="@container mx-auto flex w-full max-w-[56rem] flex-col gap-lg"
>
	<div class="flex items-start justify-between gap-sm">
		<div class="min-w-0">
			<h2 class="tr-title-entity text-text-default">Browser MCP</h2>
			<p class="mt-xs text-text-muted tr-text-metadata">
				Pi connects directly to an external browser MCP endpoint. Pixie stores this
				setting, registers it in Pi's MCP configuration, then probes and reports. Pixie
				hosts no browser and never proxies MCP traffic.
			</p>
		</div>
		<Button
			variant="ghost"
			size="sm"
			data-testid="browser-mcp-refresh"
			disabled={disabled || loading}
			onclick={onRefresh}
		>
			<Icon
				name="refresh-cw"
				size={14}
				class={loading ? "animate-spin motion-reduce:animate-none" : ""}
			/>
			{loading ? "Refreshing…" : "Refresh"}
		</Button>
	</div>

	{#if !connected}
		<p role="alert" class="text-feedback-error tr-text-metadata">
			Controller disconnected. Registration is unavailable and no configuration change is
			claimed.
		</p>
	{/if}
	{#if error}
		<p data-testid="browser-mcp-error" role="alert" class="text-feedback-error tr-text-metadata">
			{error}
		</p>
	{/if}
	{#if notice}
		<p
			data-testid="browser-mcp-notice"
			role="status"
			class="text-feedback-success tr-text-metadata"
		>
			{notice}
		</p>
	{/if}

	<section class="card flex flex-col gap-sm p-md">
		<label class="flex flex-col gap-xs tr-text-ui">
			Name
			<span class="text-text-muted tr-text-metadata">
				The Pi MCP entry name. Letters, numbers, dots, underscores and hyphens.
			</span>
			<input
				data-testid="browser-mcp-name"
				maxlength="128"
				value={draft.name}
				disabled={disabled}
				oninput={(event) => onName(event.currentTarget.value)}
				class="rounded border border-border-default bg-control-bg px-sm py-xs"
			/>
		</label>
		<label class="flex flex-col gap-xs tr-text-ui">
			URL
			<span class="text-text-muted tr-text-metadata">
				The endpoint Pi dials directly. It may be unauthenticated; the deployment owns
				hardening, network isolation and egress. Pixie does not proxy this traffic.
			</span>
			<input
				data-testid="browser-mcp-url"
				value={draft.url}
				disabled={disabled}
				placeholder="http://127.0.0.1:3000/mcp"
				oninput={(event) => onUrl(event.currentTarget.value)}
				class="rounded border border-border-default bg-control-bg px-sm py-xs"
			/>
		</label>
		<label class="flex items-center gap-xs tr-text-ui">
			<input
				data-testid="browser-mcp-enabled"
				type="checkbox"
				checked={draft.enabled}
				disabled={disabled}
				onchange={(event) => onEnabled(event.currentTarget.checked)}
			/>
			Enable and register this endpoint in Pi
		</label>
		<p class="text-text-muted tr-text-metadata">
			Register/Update applies the toggle: enabled upserts Pixie's entry, disabled removes it.
			Remove always unregisters Pixie's entry and disables the setting.
		</p>
		<div class="flex flex-wrap gap-xs">
			<Button
				size="sm"
				data-testid="browser-mcp-save"
				disabled={disabled}
				onclick={onSave}
			>
				<Icon name="save" size={14} />
				{busy === "saving" ? "Saving…" : "Register/Update"}
			</Button>
			<Button
				size="sm"
				variant="outline"
				data-testid="browser-mcp-remove"
				disabled={disabled}
				onclick={onRemove}
			>
				<Icon name="trash-2" size={14} />
				{busy === "removing" ? "Removing…" : "Remove"}
			</Button>
		</div>
	</section>

	<section data-testid="browser-mcp-status" class="card flex flex-col gap-sm p-md">
		<h3 class="tr-title-section text-text-default">Registration status</h3>
		{#if status === null}
			<p role="status" class="text-text-muted tr-text-metadata">
				{loading
					? "Loading browser MCP status…"
					: "No status reported. Pixie leaves Pi's existing MCP configuration unchanged."}
			</p>
		{:else}
			<dl class="divide-y divide-border-muted border-border-muted border-t">
				<div class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-sm py-xs">
					<dt class="text-text-muted tr-text-metadata">Registration</dt>
					<dd
						data-testid="browser-mcp-status-registered"
						class="min-w-0 text-right tr-text-metadata"
					>
						{browserMcpRegistrationLabel(status)}
					</dd>
				</div>
				<div class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-sm py-xs">
					<dt class="text-text-muted tr-text-metadata">Layer</dt>
					<dd class="min-w-0 break-words text-right tr-text-metadata">
						{status.layer ?? "—"}
					</dd>
				</div>
				<div class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-sm py-xs">
					<dt class="text-text-muted tr-text-metadata">Path</dt>
					<dd class="min-w-0 break-all text-right tr-text-metadata">{status.path ?? "—"}</dd>
				</div>
				<div class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-sm py-xs">
					<dt class="text-text-muted tr-text-metadata">Disabled override</dt>
					<dd class="min-w-0 text-right tr-text-metadata">
						{status.disabled ? "Pi disables this entry" : "No disabled override"}
					</dd>
				</div>
				<div class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-sm py-xs">
					<dt class="text-text-muted tr-text-metadata">Endpoint</dt>
					<dd
						data-testid="browser-mcp-status-reachable"
						class={`min-w-0 text-right tr-text-metadata ${status.reachable ? "text-feedback-success" : "text-feedback-warning"}`}
					>
						{browserMcpReachabilityLabel(status)}
					</dd>
				</div>
				{#if serverLabel}
					<div class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-sm py-xs">
						<dt class="text-text-muted tr-text-metadata">Server</dt>
						<dd class="min-w-0 break-words text-right tr-text-metadata">{serverLabel}</dd>
					</div>
				{/if}
				{#if status.protocolVersion}
					<div class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-sm py-xs">
						<dt class="text-text-muted tr-text-metadata">Protocol</dt>
						<dd class="min-w-0 text-right tr-text-metadata">{status.protocolVersion}</dd>
					</div>
				{/if}
			</dl>
			<div data-testid="browser-mcp-tools" class="text-text-muted tr-text-metadata">
				{browserMcpToolSummary(status)}
				{#if status.tools.length > 0}
					<ul class="mt-xs flex flex-wrap gap-xs">
						{#each status.tools as tool (tool)}
							<li class="rounded border border-border-default bg-control-bg px-sm py-2xs">
								{tool}
							</li>
						{/each}
					</ul>
				{/if}
			</div>
			{#if status.error}
				<p
					data-testid="browser-mcp-probe-error"
					role="alert"
					class="text-feedback-warning tr-text-metadata"
				>
					Probe error: {status.error}
				</p>
			{/if}
		{/if}
	</section>
</div>
