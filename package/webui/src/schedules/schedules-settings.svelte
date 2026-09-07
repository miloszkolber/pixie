<script lang="ts">
import { appStore } from "../store";
import SchedulesView from "./schedules-view.svelte";

let project = $derived($appStore.projects.find((item) => item.id === $appStore.selectedProjectId));
</script>

{#if project}
	{#key project.id}<SchedulesView {project} />{/key}
{:else}
	<h2 class="tr-title-entity">Schedules</h2>
	<p class="mt-sm tr-text-ui text-text-muted">Select a project in the sidebar to manage its schedules.</p>
{/if}
