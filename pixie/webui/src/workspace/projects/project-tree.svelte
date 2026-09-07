<script lang="ts">
import type { Project } from "@pixie/contracts";
import Button from "../../components/button.svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { appStore, appStoreApi, toast } from "../../store";
import { enterDefaultProjectArea } from "../navigation/default-project-area";
import AddProjectMenu from "./add-project-menu.svelte";
import OpenProjectDialogs from "./open-project-dialogs.svelte";
import ProjectCustomizationDialog from "./project-customization-dialog.svelte";
import ProjectIcon from "./project-icon.svelte";
import ProjectSessions from "./project-sessions.svelte";

interface ProjectOpener {
	openProject: (path: string) => Promise<void>;
	pickAndOpen: () => void;
}

let opener = $state<ProjectOpener>();
let customizeProject = $state<Project | null>(null);

async function selectProject(project: Project): Promise<void> {
	appStoreApi.getState().selectProject(project.id);
	await enterDefaultProjectArea(project.id);
}

function closeProject(project: Project): void {
	void getTransport()
		.request("project.close", { id: project.id })
		.catch((cause) => toast.error(errorText(cause), `Couldn't close ${project.name}`));
}
</script>

<nav class="tree flex flex-col gap-sm" aria-label="Projects">
	<header class="flex h-7 items-center justify-between pr-xs pl-sm">
		<span class="tr-text-eyebrow text-text-muted">Projects</span>
		{#snippet addTrigger(menuId: string)}
			<Button
				variant="ghost"
				size="icon"
				data-testid="add-project-menu"
				data-dropdown-menu-trigger={menuId}
				aria-haspopup="menu"
				aria-controls={menuId}
				aria-expanded="false"
				aria-label="Add project"
			>
				<Icon name="plus" size={16} />
			</Button>
		{/snippet}
		<AddProjectMenu
			recentProjects={$appStore.recentProjects}
			onOpen={() => opener?.pickAndOpen()}
			onOpenRecent={(path) => void opener?.openProject(path)}
			trigger={addTrigger}
		/>
	</header>

	<ul class="tree-group flex flex-col gap-2xs">
		{#each $appStore.projects as project (project.id)}
			{@const selected = $appStore.selectedProjectId === project.id}
			<li class="tree-item group flex min-w-0 flex-col">
				<div class="flex w-full min-w-0 items-center">
					<button
						type="button"
						data-testid="project-row"
						data-project-id={project.id}
						data-selected={selected || undefined}
						title={project.roots[0]}
						class="tree-leaf min-w-0 flex-1"
						onclick={() => void selectProject(project)}
					>
						<ProjectIcon
							icon={project.icon ?? "folder"}
							size={16}
							class={selected ? "text-primary" : "text-text-muted"}
						/>
						<span class="min-w-0 flex-1">
							<span class="block truncate tr-text-ui text-text-default">{project.name}</span>
							<span class="block truncate tr-text-metadata text-text-muted">{project.roots[0]}</span>
						</span>
					</button>
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={`Customize ${project.name}`}
						title="Customize project"
						class="invisible group-hover:visible focus:visible"
						onclick={() => (customizeProject = project)}
					>
						<Icon name="settings-2" size={14} />
					</Button>
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={`Remove ${project.name} from pixie`}
						title="Remove from pixie"
						class="invisible group-hover:visible focus:visible"
						onclick={() => closeProject(project)}
					>
						<Icon name="x" size={14} />
					</Button>
				</div>
				{#if selected}
					<ul class="tree-group flex w-full flex-col gap-2xs py-2xs pl-lg">
						<ProjectSessions {project} />
					</ul>
				{/if}
			</li>
		{/each}
	</ul>
	{#if $appStore.projects.length === 0}
		<p class="px-sm py-xs tr-text-metadata text-text-muted">Open a directory to start a project.</p>
	{/if}
</nav>

<OpenProjectDialogs bind:this={opener} onOpened={selectProject} />
{#if customizeProject}
	<ProjectCustomizationDialog
		project={customizeProject}
		open
		onOpenChange={(next) => { if (!next) customizeProject = null; }}
	/>
{/if}
