<script lang="ts">
import type { Snippet } from "svelte";
import type { HTMLButtonAttributes } from "svelte/elements";

type ButtonVariant =
	| "default"
	| "secondary"
	| "outline"
	| "ghost"
	| "destructive"
	| "destructive-outline"
	| "link";
type ButtonSize = "default" | "sm" | "icon" | "icon-sm";
type Props = Omit<HTMLButtonAttributes, "children"> & {
	children?: Snippet;
	variant?: ButtonVariant;
	size?: ButtonSize;
};

let {
	children,
	variant = "default",
	size = "default",
	class: className = "",
	type = "button",
	...rest
}: Props = $props();
</script>

<button
	{type}
	class={`btn ${className}`}
	data-variant={variant}
	data-size={size === "default" ? undefined : size}
	{...rest}
>
	{@render children?.()}
</button>
