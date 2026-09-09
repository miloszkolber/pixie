<script lang="ts">
	import type { Project } from "@pixie/contracts";
	import SchedulesView from "../../schedules/schedules-view.svelte";

	let { project }: { project: Project | null } = $props();
</script>

<div data-testid="settings-schedules-section" class="min-w-0">
	{#if project}
		{#key project.id}<SchedulesView {project} />{/key}
	{:else}
		<h2 class="tr-title-entity">Schedules</h2>
		<p class="mt-sm tr-text-ui text-text-muted">Select a project to manage its schedules. Schedules remain project-scoped; definitions and run sessions keep separate identities.</p>
	{/if}
</div>
