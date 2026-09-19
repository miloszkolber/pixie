<script module lang="ts">
export const ROW_MENU_SLOT = "change-row-menu-slot";
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

<div class="change-row-actions" {@attach mewa(dropdownBehavior)}>
	<div
		data-testid="change-row"
		data-active={active || open || undefined}
		class="change-row u-flex u-min-w-0 u-items-center"
		class:change-row-active={active || open}
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
			class={`${ROW_MENU_SLOT} change-row-menu-trigger u-flex u-items-center u-justify-center u-text-text-muted u-outline-none`}
			class:change-row-menu-open={open}
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
	.change-row-actions { display: contents; }
	.change-row {
		border-radius: var(--radius-sm);
	}
	.change-row:not(.change-row-active):hover { background-color: var(--control-bg-hovered); }
	.change-row-active { background-color: var(--control-bg-selected); }
	:global(.change-row-menu-slot) {
		inline-size: calc(var(--space-base) * 20 / 13);
		block-size: calc(var(--space-base) * 20 / 13);
		flex: 0 0 auto;
		margin-inline-end: var(--space-xs);
	}
	.change-row-menu-trigger {
		border-radius: var(--radius-sm);
		opacity: 0;
		transition:
			opacity var(--transition-fast),
			background-color var(--transition-fast),
			color var(--transition-fast);
	}
	.change-row:hover .change-row-menu-trigger,
	.change-row-menu-open,
	.change-row-menu-trigger:focus-visible { opacity: 1; }
	.change-row-menu-trigger:hover { background-color: var(--container-elevated-bg); color: var(--text-default); }
	.change-row-menu-trigger:focus-visible {
		outline: 2px solid var(--primary);
		outline-offset: 1px;
	}
	.dropdown-menu-content[data-align="end"] {
		left: auto;
		right: anchor(right);
	}
</style>
