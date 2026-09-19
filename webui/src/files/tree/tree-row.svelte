<script lang="ts">
import type { Snippet } from "svelte";
import Icon from "../../components/icon.svelte";

interface Props {
	testid: string;
	kind: "dir" | "file";
	expanded?: boolean | undefined;
	active?: boolean | undefined;
	dataStatus?: string | undefined;
	label: string;
	labelClassName?: string | undefined;
	trailing?: Snippet | undefined;
	highlight?: "self" | "wrapper";
	onclick?: (() => void) | undefined;
	ondblclick?: (() => void) | undefined;
	oncontextmenu?: ((event: MouseEvent) => void) | undefined;
}

let {
	testid,
	kind,
	expanded = false,
	active = false,
	dataStatus,
	label,
	labelClassName = "",
	trailing,
	highlight = "self",
	onclick,
	ondblclick,
	oncontextmenu,
}: Props = $props();
</script>

<button
	type="button"
	data-testid={testid}
	data-kind={kind}
	data-active={active || undefined}
	data-status={dataStatus}
	{onclick}
	{ondblclick}
	{oncontextmenu}
	class={`tree-leaf file-tree-row u-flex u-w-full u-min-w-0 u-items-center u-gap-xs u-px-xs u-text-left tr-text-ui ${
		highlight === "self"
			? `file-tree-row--hoverable ${active ? "file-tree-row--active" : ""}`
			: ""
	}`}
>
	{#if kind === "dir"}
		<Icon name={expanded ? "chevron-down" : "chevron-right"} size={14} class="u-text-text-muted" />
	{:else}
		<span class="file-tree-row-toggle-space u-shrink-0"></span>
	{/if}
	<Icon name={kind === "dir" ? "folder" : "file"} size={16} class="u-text-text-muted" />
	<span class={`u-min-w-0 u-flex-1 u-truncate ${labelClassName}`}>{label}</span>
	{@render trailing?.()}
</button>

<style>
	.file-tree-row {
		min-block-size: var(--panel-header-row-height);
		border-radius: 0;
	}
	.file-tree-row--hoverable:hover { background-color: var(--control-bg-hovered); }
	.file-tree-row--active { background-color: var(--control-bg-selected); }
	.file-tree-row-toggle-space {
		inline-size: calc(var(--panel-header-row-height) / 2);
		block-size: calc(var(--panel-header-row-height) / 2);
	}
	.tree-row-status-added { color: var(--feedback-success); }
	.tree-row-status-deleted {
		color: var(--feedback-error);
		text-decoration: line-through;
	}
	.tree-row-status-renamed { color: var(--feedback-info); }
</style>
