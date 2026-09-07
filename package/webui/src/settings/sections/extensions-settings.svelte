<script lang="ts">
import { appStore, selectActiveContentTab, selectActiveProjectArea } from "@/store";
import ExtensionsReader from "./extensions-reader.svelte";
let area = $derived(selectActiveProjectArea($appStore));
let tab = $derived(area ? selectActiveContentTab($appStore, area.id) : null);
let project = $derived(
	$appStore.projects.find(
		(project) => project.id === (area?.projectId ?? $appStore.selectedProjectId),
	),
);
let sessionId = $derived(area && tab?.kind === "chat" ? tab.sessionId : undefined);
let target = $derived(
	project
		? { projectId: project.id, root: project.roots[0] ?? "", ...(sessionId ? { sessionId } : {}) }
		: {},
);
let label = $derived(
	project
		? `Project: ${project.name}${sessionId ? ` · Session: ${sessionId}` : " · No session selected"}`
		: "Assistant service context · No project selected",
);
</script>

{#key JSON.stringify(target)}<ExtensionsReader {target} {label} />{/key}
