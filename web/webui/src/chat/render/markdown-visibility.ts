// Share one observer per scroll pane, rather than one observer per message.
// Base Markdown stays mounted for native find, selection, links and accessibility.
const panes = new WeakMap<
	Element,
	{
		observer: IntersectionObserver;
		callbacks: Map<Element, (near: boolean) => void>;
	}
>();

export function observeMarkdown(node: HTMLElement, change: (near: boolean) => void): () => void {
	const root = node.closest("[data-testid=chat-scroll]");
	if (!root || typeof IntersectionObserver === "undefined") {
		change(true);
		return () => {};
	}
	let pane = panes.get(root);
	if (!pane) {
		const callbacks = new Map<Element, (near: boolean) => void>();
		const observer = new IntersectionObserver(
			(entries) => {
				for (const entry of entries) callbacks.get(entry.target)?.(entry.isIntersecting);
			},
			{ root, rootMargin: "800px 0px" },
		);
		pane = { observer, callbacks };
		panes.set(root, pane);
	}
	pane.callbacks.set(node, change);
	pane.observer.observe(node);
	return () => {
		pane.observer.unobserve(node);
		pane.callbacks.delete(node);
		if (pane.callbacks.size === 0) {
			pane.observer.disconnect();
			panes.delete(root);
		}
	};
}
