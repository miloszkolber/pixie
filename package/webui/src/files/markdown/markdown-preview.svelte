<script lang="ts">
import { tick } from "svelte";
import circleAlert from "../../../vendor/mewa-icons/icons/circle-alert.svg";
import info from "../../../vendor/mewa-icons/icons/info.svg";
import lightbulb from "../../../vendor/mewa-icons/icons/lightbulb.svg";
import octagonAlert from "../../../vendor/mewa-icons/icons/octagon-alert.svg";
import triangleAlert from "../../../vendor/mewa-icons/icons/triangle-alert.svg";
import { getTransport } from "../../connection";
import { highlightCode } from "../../lib/highlighter";
import { stripFrontmatter } from "../../lib/utils";
import { openFileInTab } from "../tabs/open-tabs";
import { type AlertVariant, parseAlertMarker, renderMarkdown, slugify } from "@/lib/markdown";
import { classifyHref, projectFileUrl, resolveRelativePath, splitHash } from "./markdown-links";

interface Props {
	content: string;
	projectAreaId: string;
	path: string;
}
let { content, projectAreaId, path }: Props = $props();
let root = $state<HTMLElement>();
let html = $derived(renderMarkdown(stripFrontmatter(content)));
let enhancementGeneration = 0;

const alerts: Record<AlertVariant, { label: string; icon: string }> = {
	note: { label: "Note", icon: info },
	tip: { label: "Tip", icon: lightbulb },
	important: { label: "Important", icon: circleAlert },
	warning: { label: "Warning", icon: triangleAlert },
	caution: { label: "Caution", icon: octagonAlert },
};

async function enhance(generation: number, areaId: string, filePath: string): Promise<void> {
	if (!root) return;
	const seen = new Map<string, number>();
	for (const heading of root.querySelectorAll<HTMLHeadingElement>("h1,h2,h3,h4,h5,h6")) {
		const base = slugify(heading.textContent ?? "");
		if (!base) continue;
		const count = seen.get(base) ?? 0;
		seen.set(base, count + 1);
		heading.id = count === 0 ? base : `${base}-${count}`;
	}
	for (const quote of root.querySelectorAll<HTMLQuoteElement>("blockquote")) {
		const paragraph = quote.querySelector(":scope > p:first-child");
		if (!paragraph) continue;
		const firstText = paragraph.firstChild;
		if (!(firstText instanceof Text)) continue;
		const parsed = parseAlertMarker(firstText.data);
		if (!parsed) continue;
		firstText.data = parsed.rest;
		if ((paragraph.textContent ?? "").trim() === "" && paragraph.children.length === 0) {
			paragraph.remove();
		}
		quote.dataset.testid = "md-alert";
		quote.dataset.variant = parsed.variant;
		quote.classList.add("markdown-alert", `markdown-alert-${parsed.variant}`);
		const heading = document.createElement("strong");
		heading.className = "markdown-alert-label";
		const icon = document.createElement("span");
		icon.className = "markdown-alert-icon";
		icon.style.setProperty("--icon-source", `url("${alerts[parsed.variant].icon}")`);
		icon.setAttribute("aria-hidden", "true");
		const label = document.createElement("span");
		label.textContent = alerts[parsed.variant].label;
		heading.append(icon, label);
		quote.prepend(heading);
	}
	for (const anchor of root.querySelectorAll<HTMLAnchorElement>("a[href]")) {
		if (classifyHref(anchor.getAttribute("href") ?? undefined) === "external") {
			anchor.target = "_blank";
			anchor.rel = "noopener noreferrer";
		}
	}
	for (const image of root.querySelectorAll<HTMLImageElement>("img[src]")) {
		const source = image.dataset.markdownSource ?? image.getAttribute("src") ?? undefined;
		if (classifyHref(source) !== "relative" || !source) continue;
		image.dataset.markdownSource = source;
		const resolved = projectFileUrl(getTransport().httpBase(), areaId, filePath, source);
		if (resolved) image.setAttribute("src", resolved);
	}
	for (const code of root.querySelectorAll<HTMLElement>("pre > code[class*='language-']")) {
		const language = /(?:^|\s)language-([^\s]+)/.exec(code.className)?.[1];
		const pre = code.parentElement;
		if (!language || !pre) continue;
		const highlighted = await highlightCode(code.textContent ?? "", language);
		if (!highlighted || generation !== enhancementGeneration || !pre.isConnected) continue;
		const wrapper = document.createElement("div");
		wrapper.className = "markdown-code-block";
		wrapper.innerHTML = highlighted;
		pre.replaceWith(wrapper);
	}
}

function activateLink(event: MouseEvent): void {
	const container = root;
	if (!container) return;
	const anchor = (event.target as Element | null)?.closest<HTMLAnchorElement>("a[href]");
	if (!anchor || !container.contains(anchor)) return;
	const href = anchor.getAttribute("href") ?? undefined;
	const kind = classifyHref(href);
	if (kind === "empty") {
		event.preventDefault();
	} else if (kind === "anchor" && href) {
		event.preventDefault();
		container
			.querySelector<HTMLElement>(`#${CSS.escape(decodeURIComponent(href.slice(1)))}`)
			?.scrollIntoView({ behavior: "smooth", block: "start" });
	} else if (kind === "relative" && href) {
		event.preventDefault();
		const reference = splitHash(href).path;
		const target = resolveRelativePath(path, reference.split("?", 1)[0] ?? "");
		if (target) void openFileInTab(projectAreaId, target, "preview");
	}
}

$effect(() => {
	const rendered = html;
	const areaId = projectAreaId;
	const filePath = path;
	void rendered;
	const generation = ++enhancementGeneration;
	void tick().then(() => enhance(generation, areaId, filePath));
	return () => {
		if (generation === enhancementGeneration) enhancementGeneration += 1;
	};
});

$effect(() => {
	const node = root;
	if (!node) return;
	node.addEventListener("click", activateLink);
	return () => node.removeEventListener("click", activateLink);
});
</script>

<div data-testid="markdown-preview" class="h-full overflow-auto bg-container-project-bg">
	<article class="mx-auto max-w-[78ch] px-xl py-lg">
		<div
			bind:this={root}
			class="tr-prose-doc markdown-document max-w-none break-words text-pretty text-text-default"
		>
			{@html html}
		</div>
	</article>
</div>

<style>
	:global(.markdown-document > :first-child) { margin-top: 0; }
	:global(.markdown-document > :last-child) { margin-bottom: 0; }
	:global(.markdown-document h1), :global(.markdown-document h2) { border-bottom: 1px solid var(--border-primary); padding-bottom: var(--space-200); }
	:global(.markdown-document h1) { margin: 0 0 var(--space-400); }
	:global(.markdown-document h2) { margin: var(--space-800) 0 var(--space-400); }
	:global(.markdown-document h3), :global(.markdown-document h4) { margin: var(--space-600) 0 var(--space-300); }
	:global(.markdown-document h5), :global(.markdown-document h6) { margin: var(--space-400) 0 var(--space-200); }
	:global(.markdown-document p), :global(.markdown-document ul), :global(.markdown-document ol), :global(.markdown-document table), :global(.markdown-document pre) { margin-block: var(--space-400); }
	:global(.markdown-document ul), :global(.markdown-document ol) { padding-left: 1.6em; }
	:global(.markdown-document ul) { list-style: disc; }
	:global(.markdown-document ol) { list-style: decimal; }
	:global(.markdown-document li) { margin-block: var(--space-100); }
	:global(.markdown-document li > ul), :global(.markdown-document li > ol) { margin-block: var(--space-100); }
	:global(.markdown-document li:has(> input[type="checkbox"])) { list-style: none; }
	:global(.markdown-document input[type="checkbox"]) { margin-right: var(--space-200); accent-color: var(--primary); }
	:global(.markdown-document a) { color: var(--text-link); text-decoration: underline; text-underline-offset: 2px; }
	:global(.markdown-document blockquote) { border-left: 2px solid var(--border-secondary); padding-left: var(--space-400); color: var(--text-muted); }
	:global(.markdown-document hr) { height: 1px; margin-block: var(--space-800); border: 0; background: var(--border-primary); }
	:global(.markdown-document table) { display: block; width: max-content; max-width: 100%; overflow-x: auto; border-collapse: collapse; }
	:global(.markdown-document th), :global(.markdown-document td) { border: 1px solid var(--border-primary); padding: var(--space-200) var(--space-300); text-align: left; vertical-align: top; }
	:global(.markdown-document th) { background: var(--container-elevated-bg); }
	:global(.markdown-document tbody tr:nth-child(2n)) { background: var(--sunken); }
	:global(.markdown-document :not(pre) > code) { border-radius: var(--radius-xs); background: var(--container-elevated-bg); padding: 0.125rem 0.25rem; }
	:global(.markdown-document img) { max-width: 100%; margin-block: var(--space-400); border-radius: var(--radius-sm); }
	:global(.markdown-code-block) { margin-block: var(--space-400); overflow: auto; border-radius: var(--radius-sm); }
	:global(.markdown-code-block .shiki) { margin: 0; background: var(--container-elevated-bg) !important; padding: var(--space-300); }
	:global(.markdown-alert) { --alert-color: var(--feedback-info); --alert-bg: var(--feedback-info-subtle); margin-block: var(--space-400); border-left: 2px solid var(--alert-color); border-radius: 0 var(--radius-sm) var(--radius-sm) 0; padding: var(--space-300) var(--space-400); background: var(--alert-bg); color: var(--text-default); }
	:global(.markdown-alert-tip) { --alert-color: var(--feedback-success); --alert-bg: var(--feedback-success-subtle); }
	:global(.markdown-alert-important) { --alert-color: var(--primary); --alert-bg: var(--primary-subtle); }
	:global(.markdown-alert-warning) { --alert-color: var(--feedback-warning); --alert-bg: var(--feedback-warning-subtle); }
	:global(.markdown-alert-caution) { --alert-color: var(--feedback-error); --alert-bg: var(--feedback-error-subtle); }
	:global(.markdown-alert-label) { display: flex; align-items: center; gap: var(--space-200); margin-bottom: var(--space-100); color: var(--alert-color); }
	:global(.markdown-alert-icon) { display: inline-block; width: 1rem; height: 1rem; flex: 0 0 auto; background: currentColor; mask: var(--icon-source) center / contain no-repeat; -webkit-mask: var(--icon-source) center / contain no-repeat; }
	:global(.markdown-alert > :last-child) { margin-bottom: 0; }
</style>
