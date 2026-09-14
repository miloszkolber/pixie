<script lang="ts">
import type { AgentProfile } from "@pixie/shared";
import { agentOperationRows } from "./agent-settings";

interface Props {
	profile: AgentProfile;
}

let { profile }: Props = $props();
let operations = $derived(agentOperationRows(profile));
</script>

<div class="agent-settings u-flex u-w-full u-flex-col u-gap-lg">
	<div>
		<h2 class="tr-title-entity u-text-text-default">{profile.name || "Connected agent"}</h2>
		<p class="u-mt-xs tr-text-ui u-text-text-muted">
			{profile.version ? `Version ${profile.version} · ` : ""}
			{profile.compatible ? "Compatible with Pixie" : "Missing required capabilities"}
		</p>
	</div>
	{#if profile.missingRequired.length > 0}
		<div class="callout" data-variant="caution">
			<div class="callout-content">
				<h3 class="callout-title">Required capabilities</h3>
				<ul class="agent-settings__missing-list u-mt-xs tr-text-metadata u-text-text-muted">
					{#each profile.missingRequired as capability (capability)}
						<li><code>{capability}</code></li>
					{/each}
				</ul>
			</div>
		</div>
	{/if}
	<div>
		<h3 class="tr-text-ui u-text-text-default">Optional capabilities</h3>
		<dl class="agent-settings__operations u-mt-sm u-rounded u-border u-border-border-default">
			{#each operations as operation (operation.operation)}
				<div class="u-flex u-items-center u-justify-between u-gap-md u-px-md u-py-sm">
					<dt class="tr-text-ui u-text-text-default">{operation.label}</dt>
					<dd
						class={`tr-text-metadata ${operation.available ? "availability--available" : "u-text-text-muted"}`}
					>
						{operation.available ? "Available" : "Unavailable"}
					</dd>
				</div>
			{/each}
		</dl>
	</div>
</div>

<style>
	.agent-settings {
		max-inline-size: 36rem;
		margin-inline: auto;
	}

	.agent-settings__missing-list {
		padding-left: var(--space-lg);
		list-style-type: disc;
	}

	.agent-settings__operations > * + * {
		border-top: 1px solid var(--border-muted);
	}

	.availability--available {
		color: var(--feedback-success);
	}
</style>
