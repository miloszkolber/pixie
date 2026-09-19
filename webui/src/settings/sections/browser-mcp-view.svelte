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
	class="browser-mcp-view u-flex u-w-full u-flex-col u-gap-lg"
>
	<div class="u-flex u-items-start u-justify-between u-gap-sm">
		<div class="u-min-w-0">
			<h2 class="tr-title-entity u-text-text-default">Browser MCP</h2>
			<p class="u-mt-xs u-text-text-muted tr-text-metadata">
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
				class={loading ? "browser-mcp-refresh-icon" : ""}
			/>
			{loading ? "Refreshing…" : "Refresh"}
		</Button>
	</div>

	{#if !connected}
		<p role="alert" class="u-text-feedback-error tr-text-metadata">
			Controller disconnected. Registration is unavailable and no configuration change is
			claimed.
		</p>
	{/if}
	{#if error}
		<p data-testid="browser-mcp-error" role="alert" class="u-text-feedback-error tr-text-metadata">
			{error}
		</p>
	{/if}
	{#if notice}
		<p
			data-testid="browser-mcp-notice"
			role="status"
			class="browser-mcp-notice tr-text-metadata"
		>
			{notice}
		</p>
	{/if}

	<section class="card u-flex u-flex-col u-gap-sm u-p-md">
		<label class="u-flex u-flex-col u-gap-xs tr-text-ui">
			Name
			<span class="u-text-text-muted tr-text-metadata">
				The Pi MCP entry name. Letters, numbers, dots, underscores and hyphens.
			</span>
			<input
				data-testid="browser-mcp-name"
				maxlength="128"
				value={draft.name}
				disabled={disabled}
				oninput={(event) => onName(event.currentTarget.value)}
				class="browser-mcp-input"
			/>
		</label>
		<label class="u-flex u-flex-col u-gap-xs tr-text-ui">
			URL
			<span class="u-text-text-muted tr-text-metadata">
				The endpoint Pi dials directly. It may be unauthenticated; the deployment owns
				hardening, network isolation and egress. Pixie does not proxy this traffic.
			</span>
			<input
				data-testid="browser-mcp-url"
				value={draft.url}
				disabled={disabled}
				placeholder="http://127.0.0.1:3000/mcp"
				oninput={(event) => onUrl(event.currentTarget.value)}
				class="browser-mcp-input"
			/>
		</label>
		<label class="u-flex u-items-center u-gap-xs tr-text-ui">
			<input
				data-testid="browser-mcp-enabled"
				type="checkbox"
				checked={draft.enabled}
				disabled={disabled}
				onchange={(event) => onEnabled(event.currentTarget.checked)}
			/>
			Enable and register this endpoint in Pi
		</label>
		<p class="u-text-text-muted tr-text-metadata">
			Register/Update applies the toggle: enabled upserts Pixie's entry, disabled removes it.
			Remove always unregisters Pixie's entry and disables the setting.
		</p>
		<div class="u-flex u-flex-wrap u-gap-xs">
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

	<section data-testid="browser-mcp-status" class="card u-flex u-flex-col u-gap-sm u-p-md">
		<h3 class="tr-title-section u-text-text-default">Registration status</h3>
		{#if status === null}
			<p role="status" class="u-text-text-muted tr-text-metadata">
				{loading
					? "Loading browser MCP status…"
					: "No status reported. Pixie leaves Pi's existing MCP configuration unchanged."}
			</p>
		{:else}
			<dl class="browser-mcp-metrics u-border-t">
				<div class="browser-mcp-metric u-min-w-0 u-py-xs">
					<dt class="u-text-text-muted tr-text-metadata">Registration</dt>
					<dd
						data-testid="browser-mcp-status-registered"
						class="browser-mcp-metric__value u-min-w-0 tr-text-metadata"
					>
						{browserMcpRegistrationLabel(status)}
					</dd>
				</div>
				<div class="browser-mcp-metric u-min-w-0 u-py-xs">
					<dt class="u-text-text-muted tr-text-metadata">Layer</dt>
					<dd class="browser-mcp-metric__value browser-mcp-metric__value--wrapped u-min-w-0 tr-text-metadata">
						{status.layer ?? "—"}
					</dd>
				</div>
				<div class="browser-mcp-metric u-min-w-0 u-py-xs">
					<dt class="u-text-text-muted tr-text-metadata">Path</dt>
					<dd class="browser-mcp-metric__value browser-mcp-metric__value--break-all u-min-w-0 tr-text-metadata">{status.path ?? "—"}</dd>
				</div>
				<div class="browser-mcp-metric u-min-w-0 u-py-xs">
					<dt class="u-text-text-muted tr-text-metadata">Disabled override</dt>
					<dd class="browser-mcp-metric__value u-min-w-0 tr-text-metadata">
						{status.disabled ? "Pi disables this entry" : "No disabled override"}
					</dd>
				</div>
				<div class="browser-mcp-metric u-min-w-0 u-py-xs">
					<dt class="u-text-text-muted tr-text-metadata">Endpoint</dt>
					<dd
						data-testid="browser-mcp-status-reachable"
						class={`browser-mcp-metric__value u-min-w-0 tr-text-metadata ${status.reachable ? "browser-mcp-metric__value--reachable" : "u-text-feedback-warning"}`}
					>
						{browserMcpReachabilityLabel(status)}
					</dd>
				</div>
				{#if serverLabel}
					<div class="browser-mcp-metric u-min-w-0 u-py-xs">
						<dt class="u-text-text-muted tr-text-metadata">Server</dt>
						<dd class="browser-mcp-metric__value browser-mcp-metric__value--wrapped u-min-w-0 tr-text-metadata">{serverLabel}</dd>
					</div>
				{/if}
				{#if status.protocolVersion}
					<div class="browser-mcp-metric u-min-w-0 u-py-xs">
						<dt class="u-text-text-muted tr-text-metadata">Protocol</dt>
						<dd class="browser-mcp-metric__value u-min-w-0 tr-text-metadata">{status.protocolVersion}</dd>
					</div>
				{/if}
			</dl>
			<div data-testid="browser-mcp-tools" class="u-text-text-muted tr-text-metadata">
				{browserMcpToolSummary(status)}
				{#if status.tools.length > 0}
					<ul class="u-mt-xs u-flex u-flex-wrap u-gap-xs">
						{#each status.tools as tool (tool)}
							<li class="browser-mcp-tool u-rounded u-border u-border-border-default u-bg-control-bg u-px-sm">
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
					class="u-text-feedback-warning tr-text-metadata"
				>
					Probe error: {status.error}
				</p>
			{/if}
		{/if}
	</section>
</div>

<style>
	.browser-mcp-view {
		max-inline-size: 56rem;
		margin-inline: auto;
	}

	.browser-mcp-notice,
	.browser-mcp-metric__value--reachable {
		color: var(--feedback-success);
	}

	.browser-mcp-input {
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--control-bg);
		padding: var(--space-xs) var(--space-sm);
		color: var(--text-default);
	}

	.browser-mcp-input:focus-visible {
		outline: var(--focus-ring-width, 2px) solid var(--border-focus);
		outline-offset: -1px;
	}

	.browser-mcp-input:disabled {
		border-color: var(--control-disabled-border);
		background: var(--control-disabled-bg);
		color: var(--control-disabled-text);
	}

	.browser-mcp-metrics {
		border-color: var(--border-muted);
	}

	.browser-mcp-metrics > * + * {
		border-top: 1px solid var(--border-muted);
	}

	.browser-mcp-metric {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		align-items: baseline;
		column-gap: var(--space-sm);
	}

	.browser-mcp-metric__value {
		text-align: right;
	}

	.browser-mcp-metric__value--wrapped {
		overflow-wrap: break-word;
	}

	.browser-mcp-metric__value--break-all {
		word-break: break-all;
	}

	.browser-mcp-tool {
		padding-block: var(--space-2xs);
	}

	:global(.browser-mcp-refresh-icon) {
		animation: browser-mcp-refresh-rotate 1s linear infinite;
	}

	@keyframes browser-mcp-refresh-rotate {
		to { transform: rotate(1turn); }
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.browser-mcp-refresh-icon) { animation: none; }
	}
</style>
