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
	class="app-content u-flex u-h-full u-min-h-0 u-flex-col u-items-center u-justify-center u-overflow-auto u-px-xl u-py-xl u-text-center"
>
	<h1 data-testid="welcome-title" class="tr-brand-hero welcome-panel__title">
		{project ? project.name : PRODUCT_NAME}
	</h1>

	<div class="welcome-panel__actions">
		{#if project}
			<button
				type="button"
				data-testid="welcome-cta"
				class="card welcome-panel__cta"
				onclick={() => void enterDefaultProjectArea(project.id)}
			>
				<span class="welcome-panel__cta-icon u-flex u-items-center u-justify-center">
					<Icon name="house" size={16} />
				</span>
				<span class="u-w-full">
					<span class="card-title welcome-panel__cta-title">Continue project</span>
					<span class="card-description welcome-panel__cta-description">
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
					class="card welcome-panel__cta"
				>
					<span class="welcome-panel__cta-icon u-flex u-items-center u-justify-center">
						<Icon name="folder-open" size={16} />
					</span>
					<span class="u-w-full">
						<span class="card-title welcome-panel__cta-title">Open project</span>
						<span class="card-description welcome-panel__cta-description">
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

<style>
	.welcome-panel__title {
		max-width: 640px;
		color: var(--primary);
		overflow-wrap: break-word;
	}

	.welcome-panel__actions {
		display: flex;
		flex-wrap: wrap;
		justify-content: center;
		gap: var(--space-md);
		margin-top: var(--space-xl);
	}

	.welcome-panel__cta {
		display: flex;
		position: relative;
		width: 220px;
		height: 150px;
		flex-direction: column;
		align-items: flex-start;
		justify-content: space-between;
		padding: var(--space-lg);
		border-color: var(--primary-muted);
		background-color: var(--primary-subtle);
		text-align: left;
	}

	.welcome-panel__cta:hover {
		background-color: var(--primary-soft);
	}

	.welcome-panel__cta-icon {
		width: 2.25rem;
		height: 2.25rem;
		background-color: var(--primary);
		color: var(--text-on-primary);
	}

	.welcome-panel__cta-title,
	.welcome-panel__cta-description {
		display: block;
	}

	.welcome-panel__cta-description {
		margin-top: 0.125rem;
	}
</style>
