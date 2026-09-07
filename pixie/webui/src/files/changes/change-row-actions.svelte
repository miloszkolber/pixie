<script module lang="ts">
export const ROW_MENU_SLOT = "mr-xs size-5 shrink-0";
</script>

<script lang="ts">
	import type { Snippet } from "svelte";
	import { mewa } from "../../../vendor/mewa-svelte/index.js";
	import { behavior as dropdownBehavior } from "../../../vendor/mewa-ui/components/dropdown-menu.js";
	import Icon from "../../components/icon.svelte";
	import { copyText } from "../../lib";

	interface Props {
		path: string;
		active?: boolean;
		onView: () => void;
		children: Snippet<[(event: MouseEvent) => void]>;
	}

	let { path, active = false, onView, children }: Props = $props();
	let open = $state(false);
	let menu: HTMLElement;
	const componentId = $props.id();
	const menuId = `change-row-actions-${componentId}`;

	function openContextMenu(event: MouseEvent): void {
		event.preventDefault();
		menu?.showPopover();
	}

	function choose(callback: () => void): void {
		menu?.hidePopover();
		callback();
	}
</script>

<div class="contents" {@attach mewa(dropdownBehavior)}>
	<div
		data-testid="change-row"
		data-active={active || open || undefined}
		class={`group flex min-w-0 items-center rounded-[var(--radius-sm)] ${
			active || open ? "bg-control-bg-selected" : "hover:bg-control-bg-hovered"
		}`}
	>
		{@render children(openContextMenu)}
		<button
			type="button"
			data-testid="change-row-menu"
			data-dropdown-menu-trigger={menuId}
			aria-haspopup="menu"
			aria-controls={menuId}
			aria-expanded="false"
			aria-label={`Actions for ${path}`}
		class={`${ROW_MENU_SLOT} flex items-center justify-center rounded-[var(--radius-sm)] text-text-muted outline-none transition hover:bg-container-elevated-bg hover:text-text-default focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary group-hover:opacity-100 ${open ? "opacity-100" : "opacity-0"}`}
		>
			<Icon name="chevron-down" size={16} />
		</button>
	</div>
	<div
		bind:this={menu}
		id={menuId}
		popover="auto"
		role="menu"
		data-testid="change-row-actions"
		class="dropdown-menu-content"
		data-align="end"
		ontoggle={(event) => (open = event.newState === "open")}
	>
		<button
			type="button"
			role="menuitem"
			class="dropdown-menu-item"
			data-testid="change-action-view"
			onclick={() => choose(onView)}
		>
			<Icon name="file-diff" size={16} />
			<span>View</span>
		</button>
		<button
			type="button"
			role="menuitem"
			class="dropdown-menu-item"
			data-testid="change-action-copy-path"
			onclick={() => choose(() => void copyText(path))}
		>
			<Icon name="copy" size={16} />
			<span>Copy path</span>
		</button>
	</div>
</div>

<style>
	.dropdown-menu-content[data-align="end"] {
		left: auto;
		right: anchor(right);
	}
</style>
