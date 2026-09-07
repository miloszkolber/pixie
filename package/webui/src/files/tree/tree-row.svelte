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
	class={`tree-leaf flex min-h-7 w-full min-w-0 items-center gap-xs rounded-[var(--radius-sm)] px-xs text-left tr-text-ui text-text-muted ${
		highlight === "self"
			? `hover:bg-control-bg-hovered ${active ? "bg-control-bg-selected" : ""}`
			: ""
	}`}
>
	{#if kind === "dir"}
		<Icon name={expanded ? "chevron-down" : "chevron-right"} size={14} class="text-text-muted" />
	{:else}
		<span class="size-3.5 shrink-0"></span>
	{/if}
	<Icon name={kind === "dir" ? "folder" : "file"} size={16} class="text-text-muted" />
	<span class={`min-w-0 flex-1 truncate ${labelClassName}`}>{label}</span>
	{@render trailing?.()}
</button>
