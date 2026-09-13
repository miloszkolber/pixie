<script lang="ts">
import Icon from "../../components/icon.svelte";
import { PRODUCT_NAME } from "../../constants/branding";
import { appStore, appStoreApi } from "../../store";
import { enterDefaultProjectArea } from "../navigation/default-project-area";
import AddProjectMenu from "../projects/add-project-menu.svelte";
import OpenProjectDialogs from "../projects/open-project-dialogs.svelte";

interface ProjectOpener {
	openProject: (path: string) => Promise<void>;
	pickAndOpen: () => void;
}

let opener = $state<ProjectOpener>();
let project = $derived(
	$appStore.projects.find((item) => item.id === $appStore.selectedProjectId) ??
		$appStore.projects[0] ??
		null,
);
</script>

<div
	data-testid="welcome"
	class="app-content flex h-full min-h-0 flex-col items-center justify-center overflow-auto px-xl py-xl text-center"
>
	<h1 data-testid="welcome-title" class="tr-brand-hero max-w-[640px] break-words text-primary">
		{project ? project.name : PRODUCT_NAME}
	</h1>

	<div class="mt-xl flex flex-wrap justify-center gap-md">
		{#if project}
			<button
				type="button"
				data-testid="welcome-cta"
				class="card relative flex h-[150px] w-[220px] flex-col items-start justify-between border-primary-muted bg-primary-subtle p-lg text-left hover:bg-primary-soft"
				onclick={() => void enterDefaultProjectArea(project.id)}
			>
				<span class="flex size-9 items-center justify-center bg-primary text-text-on-primary">
					<Icon name="house" size={16} />
				</span>
				<span class="w-full">
					<span class="card-title block">Continue project</span>
					<span class="card-description mt-0.5 block">
						Open the project directory and its persistent agent sessions.
					</span>
				</span>
			</button>
		{:else}
			{#snippet openProjectTrigger(menuId: string)}
				<button
					type="button"
					data-dropdown-menu-trigger={menuId}
					aria-haspopup="menu"
					aria-controls={menuId}
					aria-expanded="false"
					data-testid="welcome-cta"
					class="card relative flex h-[150px] w-[220px] flex-col items-start justify-between border-primary-muted bg-primary-subtle p-lg text-left hover:bg-primary-soft"
				>
					<span class="flex size-9 items-center justify-center bg-primary text-text-on-primary">
						<Icon name="folder-open" size={16} />
					</span>
					<span class="w-full">
						<span class="card-title block">Open project</span>
						<span class="card-description mt-0.5 block">
							Choose an admitted directory. It may contain one or several repositories.
						</span>
					</span>
				</button>
			{/snippet}
			<AddProjectMenu
				recentProjects={$appStore.recentProjects}
				onOpen={() => opener?.pickAndOpen()}
				onOpenRecent={(path) => void opener?.openProject(path)}
				trigger={openProjectTrigger}
				align="start"
			/>
		{/if}
	</div>
</div>

<OpenProjectDialogs
	bind:this={opener}
	onOpened={(opened) => appStoreApi.getState().selectProject(opened.id, { reveal: true })}
/>
