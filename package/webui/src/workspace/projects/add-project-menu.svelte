<script lang="ts">
import type { Project } from "@pixie/contracts";
import type { Snippet } from "svelte";
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as dropdownBehavior } from "../../../vendor/mewa-ui/components/dropdown-menu.js";
import Icon from "../../components/icon.svelte";

interface Props {
	recentProjects: Project[];
	onOpen: () => void;
	onOpenRecent: (path: string) => void;
	trigger: Snippet<[string]>;
	align?: "start" | "end";
}

let { recentProjects, onOpen, onOpenRecent, trigger, align = "end" }: Props = $props();
const componentId = $props.id();
const menuId = `add-project-${componentId}`;
let menu: HTMLElement;

function choose(callback: () => void): void {
	menu?.hidePopover();
	callback();
}
</script>

<div class="contents" {@attach mewa(dropdownBehavior)}>
	{@render trigger(menuId)}
	<div
		bind:this={menu}
		id={menuId}
		popover="auto"
		role="menu"
		class="dropdown-menu-content"
		data-align={align}
	>
		<button
			type="button"
			role="menuitem"
			class="dropdown-menu-item"
			data-testid="menu-open-project"
			onclick={() => choose(onOpen)}
		>
			<Icon name="folder" size={16} />
			<span>Open project</span>
		</button>
		{#if recentProjects.length > 0}
			<div class="dropdown-menu-separator"></div>
			<div class="dropdown-menu-label">Recents</div>
			<div role="group">
				{#each recentProjects as project (project.id)}
					<button
						type="button"
						role="menuitem"
						class="dropdown-menu-item"
						title={project.roots[0]}
						onclick={() => {
							const path = project.roots[0];
							if (path) choose(() => onOpenRecent(path));
						}}
					>
						<Icon name="folder" size={16} />
						<span class="truncate">{project.roots[0]}</span>
					</button>
				{/each}
			</div>
		{/if}
	</div>
</div>

<style>
	.dropdown-menu-content[data-align="end"] {
		left: auto;
		right: anchor(right);
	}
</style>
