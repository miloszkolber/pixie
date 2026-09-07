<script lang="ts">
import type { ProjectIcon as ProjectIconName } from "@pixie/contracts";
import Icon, { type IconName } from "../../components/icon.svelte";

const icons: Record<ProjectIconName, IconName> = {
	folder: "folder",
	code: "code-xml",
	book: "book-open",
	flask: "flask-conical",
	rocket: "rocket",
	sparkles: "sparkles",
};

interface Props {
	icon?: ProjectIconName;
	size?: number | string;
	class?: string;
}

let { icon = "folder", size = 16, class: className = "" }: Props = $props();
</script>

<Icon name={icons[icon]} {size} class={className} />
