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
import SessionFlatList from "./session-flat-list.svelte";
import {
	buildRemoveProjectRequest,
	parseCatalogView,
	type SessionCatalogView,
} from "./session-catalog";
import { openSettingsFrom } from "../../settings/open-settings";
import { SettingsSection } from "../../settings/state";

interface ProjectOpener {
	openProject: (path: string) => Promise<void>;
	pickAndOpen: () => void;
}

interface Props {
	chrome?: "full" | "bare";
	activeSessionId?: string | null;
	filter?: string;
}

let { chrome = "full", activeSessionId = null, filter = "" }: Props = $props();
let opener = $state<ProjectOpener>();
let customizeProject = $state<Project | null>(null);
// Grouped and flat views share one catalog (see session-catalog.ts).
let catalogView = $state<SessionCatalogView>("grouped");
let query = $derived(filter.trim().toLowerCase());
let visibleProjects = $derived(
	query
		? $appStore.projects.filter(
				(project) =>
					project.name.toLowerCase().includes(query) ||
					project.roots.some((root) => root.toLowerCase().includes(query)),
			)
		: $appStore.projects,
);

async function selectProject(project: Project): Promise<void> {
	appStoreApi.getState().selectProject(project.id);
	await enterDefaultProjectArea(project.id);
}

function closeProject(project: Project): void {
	// Removing a project from Pixie only closes it; native chats are kept.
	// Never call session.delete/session.archive here.
	const request = buildRemoveProjectRequest(project.id);
	void getTransport()
		.request(request.method, request.params)
		.catch((cause) => toast.error(errorText(cause), `Couldn't close ${project.name}`));
}

function setCatalogView(view: unknown): void {
	catalogView = parseCatalogView(view);
}
</script>

{#snippet projectList()}
	<div data-testid="session-catalog-view" role="group" aria-label="Session catalog view" class="flex shrink-0 gap-2xs px-xs">
		<Button
			variant="ghost"
			size="sm"
			data-testid="catalog-view-grouped"
			aria-pressed={catalogView === "grouped"}
			onclick={() => setCatalogView("grouped")}
		>
			Grouped
		</Button>
		<Button
			variant="ghost"
			size="sm"
			data-testid="catalog-view-flat"
			aria-pressed={catalogView === "flat"}
			onclick={() => setCatalogView("flat")}
		>
			Flat
		</Button>
	</div>
	{#if catalogView === "flat"}
		<SessionFlatList projects={visibleProjects} {activeSessionId} />
	{:else}
	<ul class="tree-group flex flex-col gap-2xs">
		{#each visibleProjects as project (project.id)}
			{@const selected = $appStore.selectedProjectId === project.id}
			{@const expanded = $appStore.expandedProjectIds[project.id] === true || selected}
			<li class="tree-item group flex min-w-0 flex-col">
				<div class="flex w-full min-w-0 items-center">
					<Button
						variant="ghost"
						size="icon-sm"
						data-testid="project-expand"
						data-project-id={project.id}
						aria-expanded={expanded}
						aria-label={expanded ? `Collapse ${project.name}` : `Expand ${project.name}`}
						title={expanded ? `Collapse ${project.name}` : `Expand ${project.name}`}
						onclick={() => appStoreApi.getState().toggleProjectExpanded(project.id)}
					>
						<Icon name={expanded ? "chevron-down" : "chevron-right"} size={14} />
					</Button>
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
				{#if expanded}
					<ul class="tree-group pixie-guide flex w-full flex-col gap-2xs py-2xs pl-md">
						<li class="tree-item"><button type="button" class="tree-leaf tr-text-metadata" onclick={(event) => openSettingsFrom(event.currentTarget, SettingsSection.Schedules)}>Schedules</button></li>
						<ProjectSessions {project} {activeSessionId} />
					</ul>
				{/if}
			</li>
		{/each}
	</ul>
	{/if}
	{#if $appStore.projects.length === 0}
		<p class="px-sm py-xs tr-text-metadata text-text-muted">Open a directory to start a project.</p>
	{:else if visibleProjects.length === 0}
		<p class="px-sm py-xs tr-text-metadata text-text-muted">No projects match this filter.</p>
	{/if}
	<!-- Ungrouped chats without a named project render in the flat catalog's
	explicit Ungrouped section; this tree never invents a hidden catch-all project. -->
{/snippet}

{#if chrome === "full"}
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
		{@render projectList()}
	</nav>
{:else}
	<div class="tree flex flex-col gap-sm">
		{@render projectList()}
	</div>
{/if}

{#if chrome === "full"}
	<OpenProjectDialogs bind:this={opener} onOpened={selectProject} />
{/if}
{#if customizeProject}
	<ProjectCustomizationDialog
		project={customizeProject}
		open
		onOpenChange={(next) => { if (!next) customizeProject = null; }}
	/>
{/if}
