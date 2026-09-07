<script lang="ts">
import { tick } from "svelte";
import { observeMarkdown } from "./markdown-visibility";

type MarkdownDocumentModule = typeof import("./markdown-document");
type ParsedMarkdown = { source: string; html: string };

interface Props {
	text: string;
	class?: string;
}

let { text, class: className = "tr-prose-chat max-w-none break-words" }: Props = $props();
let root = $state<HTMLElement>();
let parsed = $state<ParsedMarkdown | null>(null);
let html = $derived(parsed?.source === text ? parsed.html : null);
let parseGeneration = 0;
let enhancementGeneration = 0;
let nearViewport = $state(false);
const highlightedBlocks = new Map<HTMLElement, HTMLElement>();
let markdownDocumentPromise: Promise<MarkdownDocumentModule> | null = null;

function loadMarkdownDocument(): Promise<MarkdownDocumentModule> {
	markdownDocumentPromise ??= import("./markdown-document");
	return markdownDocumentPromise;
}

function prepareMarkup(node: HTMLElement): void {
	for (const anchor of node.querySelectorAll<HTMLAnchorElement>("a[href]")) {
		anchor.target = "_blank";
		anchor.rel = "noopener noreferrer";
	}
	for (const table of node.querySelectorAll<HTMLTableElement>("table")) {
		if (table.parentElement?.classList.contains("markdown-table-scroll")) continue;
		const wrapper = document.createElement("div");
		wrapper.className = "markdown-table-scroll";
		table.replaceWith(wrapper);
		wrapper.append(table);
	}
}

function restoreCode(): void {
	// Replacing highlighted spans during a native selection would collapse it.
	if (document.getSelection()?.isCollapsed === false) return;
	for (const [highlighted, plain] of highlightedBlocks) {
		if (highlighted.isConnected) highlighted.replaceWith(plain);
	}
	highlightedBlocks.clear();
}

async function enhance(current: number, node: HTMLElement): Promise<void> {
	if (document.getSelection()?.isCollapsed === false) return;
	const codeBlocks = Array.from(
		node.querySelectorAll<HTMLElement>("pre > code[class*='language-']"),
	);
	if (codeBlocks.length === 0) return;
	const [{ highlightCode }, { codeLanguage }] = await Promise.all([
		import("../../lib/highlighter"),
		loadMarkdownDocument(),
	]);
	if (
		current !== enhancementGeneration ||
		!node.isConnected ||
		document.getSelection()?.isCollapsed === false
	)
		return;
	await Promise.all(
		codeBlocks.map(async (code) => {
			const language = codeLanguage(code.className);
			const pre = code.parentElement;
			if (!language || !pre) return;
			const highlighted = await highlightCode(
				(code.textContent ?? "").replace(/\n$/, ""),
				language,
			);
			if (
				!highlighted ||
				current !== enhancementGeneration ||
				!pre.isConnected ||
				document.getSelection()?.isCollapsed === false
			)
				return;
			const wrapper = document.createElement("div");
			wrapper.className = "chat-markdown-code";
			wrapper.innerHTML = highlighted;
			highlightedBlocks.set(wrapper, pre);
			pre.replaceWith(wrapper);
		}),
	);
}

$effect(() => {
	const source = text;
	const current = ++parseGeneration;
	void loadMarkdownDocument()
		.then(({ renderChatMarkdown }) => {
			const rendered = renderChatMarkdown(source);
			if (current === parseGeneration) parsed = { source, html: rendered };
		})
		.catch(() => {
			if (current === parseGeneration) parsed = null;
		});
	return () => {
		if (parseGeneration === current) parseGeneration += 1;
	};
});

$effect(() => {
	const node = root;
	if (node)
		return observeMarkdown(node, (near) => {
			nearViewport = near;
		});
});

$effect(() => {
	const rendered = html;
	const node = root;
	for (const [highlighted] of highlightedBlocks) {
		if (!highlighted.isConnected) highlightedBlocks.delete(highlighted);
	}
	if (rendered === null || !node) return;
	prepareMarkup(node);
	const near = nearViewport;
	const current = ++enhancementGeneration;
	if (near) void tick().then(() => enhance(current, node));
	else restoreCode();
	return () => {
		if (enhancementGeneration === current) enhancementGeneration += 1;
		// Discard detached old-source blocks while keeping a live selection intact.
		for (const [highlighted] of highlightedBlocks) {
			if (!highlighted.isConnected) highlightedBlocks.delete(highlighted);
		}
	};
});
</script>

<div bind:this={root} class={className} aria-busy={html === null}>
	{#if html === null}
		<span class="chat-markdown-fallback">{text}</span>
	{:else}
		{@html html}
	{/if}
</div>

<style>
	:global(.tr-prose-chat p) { margin-block: var(--space-300); }
	:global(.tr-prose-chat ul), :global(.tr-prose-chat ol) { margin-block: var(--space-300); padding-left: var(--space-500); }
	:global(.tr-prose-chat ul) { list-style: disc; }
	:global(.tr-prose-chat ol) { list-style: decimal; }
	:global(.tr-prose-chat li) { margin-block: 0.125rem; }
	:global(.tr-prose-chat a) { color: var(--text-link); text-decoration: underline; }
	:global(.tr-prose-chat .markdown-table-scroll) { overflow-x: auto; }
	:global(.tr-prose-chat table) { border-collapse: collapse; }
	:global(.tr-prose-chat th), :global(.tr-prose-chat td) { border: 1px solid var(--border-secondary); padding: var(--space-200) var(--space-300); }
	:global(.tr-prose-chat th) { text-align: left; }
	:global(.tr-prose-chat :not(pre) > code) { border-radius: var(--radius-xs); background: var(--container-elevated-bg); padding: 0.125rem 0.25rem; }
	:global(.tr-prose-chat pre) { overflow: auto; border-radius: var(--radius-sm); background: var(--container-elevated-bg); padding: var(--space-300); }
	:global(.tr-prose-chat .chat-markdown-code) { overflow: auto; border-radius: var(--radius-sm); }
	:global(.tr-prose-chat .chat-markdown-code pre) { margin: 0; background: var(--container-elevated-bg) !important; padding: var(--space-300); }
	.chat-markdown-fallback { white-space: pre-wrap; }
</style>
