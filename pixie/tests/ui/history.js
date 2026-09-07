// Run against production assets through agent-browser eval. This deliberately
// uses public controls and DOM geometry, without importing application stores.
(() => {
	globalThis.__pixieHistory = { done: false };
	(async () => {
		const frame = () => new Promise((resolve) => requestAnimationFrame(resolve));
		const settled = async () => {
			await frame();
			await frame();
		};
		const viewport = document.querySelector('[data-testid="chat-scroll"]');
		if (!viewport) throw new Error("Chat viewport is unavailable");
		const heap = () => performance.memory?.usedJSHeapSize ?? null;
		const rows = () => [...document.querySelectorAll("[data-chat-row]")];
		const snapshot = () => ({
			rows: rows().length,
			elements: document.getElementsByTagName("*").length,
			heapBytes: heap(),
		});
		const longTasks = [];
		const observer = new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) longTasks.push(entry.duration);
		});
		observer.observe({ type: "longtask" });
		// Compare paging from a settled initial page. Fast replay can expose the
		// text fallback before fonts, Markdown and visible code are enhanced.
		await document.fonts.ready;
		const readyDeadline = performance.now() + 15000;
		for (;;) {
			const bounds = viewport.getBoundingClientRect();
			const visibleCode = [...viewport.querySelectorAll("pre > code")].filter((code) => {
				const rect = code.getBoundingClientRect();
				return rect.height > 0 && rect.bottom > bounds.top && rect.top < bounds.bottom;
			});
			if (
				!viewport.querySelector('[aria-busy="true"]') &&
				visibleCode.every((code) => code.closest(".chat-markdown-code"))
			)
				break;
			if (performance.now() > readyDeadline)
				throw new Error("Initial history rendering did not settle");
			await frame();
		}
		await settled();
		const initial = snapshot();
		const pages = [];
		for (;;) {
			const button = document.querySelector('button[aria-label="Load earlier messages"]');
			if (!button) break;
			const before = rows();
			const anchor = before.find((row) => {
				const rect = row.getBoundingClientRect();
				return (
					rect.bottom > viewport.getBoundingClientRect().top &&
					rect.top < viewport.getBoundingClientRect().bottom
				);
			});
			const top = anchor?.getBoundingClientRect().top;
			const started = performance.now();
			button.click();
			while (
				rows().length === before.length ||
				document.querySelector('button[aria-label="Loading earlier messages…"]')
			) {
				if (document.querySelector('button[aria-label="Retry loading earlier messages"]'))
					throw new Error("Older-page load failed");
				if (performance.now() - started > 15000) throw new Error("Older-page load timed out");
				await frame();
			}
			await settled();
			const drift =
				anchor && top !== undefined ? Math.abs(anchor.getBoundingClientRect().top - top) : null;
			pages.push({ ms: performance.now() - started, anchorDriftPx: drift });
			if (pages.length > 105) throw new Error("History pagination did not converge");
		}
		const loaded = snapshot();
		// Traverse the retained DOM to force distant content-visibility regions into
		// view. Measure frame intervals rather than calling this a paint-time trace.
		const gaps = [];
		let previous = performance.now();
		for (let step = 0; step < 120; step++) {
			viewport.scrollTop = (viewport.scrollHeight - viewport.clientHeight) * (step / 119);
			await frame();
			const now = performance.now();
			gaps.push(now - previous);
			previous = now;
		}
		await settled();
		observer.disconnect();
		const percentile = (values, portion) =>
			[...values].sort((a, b) => a - b)[
				Math.min(values.length - 1, Math.floor(values.length * portion))
			] ?? 0;
		const answers = [
			...document.querySelectorAll('[data-testid="chat-message"][data-role="assistant"]'),
		]
			.map((node) => node.textContent?.match(/History answer (\d+)/)?.[1])
			.filter(Boolean);
		if (new Set(answers).size !== answers.length || !answers.includes("00000"))
			throw new Error("History lost or duplicated messages");
		if (document.documentElement.scrollWidth !== document.documentElement.clientWidth)
			throw new Error("History overflows horizontally");
		if (Math.max(...pages.map((page) => page.anchorDriftPx ?? 0)) > 2)
			throw new Error("Older paging moved the visible row");
		return {
			userAgent: navigator.userAgent,
			reducedMotion: matchMedia("(prefers-reduced-motion: reduce)").matches,
			viewport: { width: innerWidth, height: innerHeight },
			initial,
			loaded,
			traversed: snapshot(),
			historyAnswers: answers.length,
			pages: pages.length,
			pageMedianMs: percentile(
				pages.map((page) => page.ms),
				0.5,
			),
			pageP95Ms: percentile(
				pages.map((page) => page.ms),
				0.95,
			),
			maxAnchorDriftPx: Math.max(...pages.map((page) => page.anchorDriftPx ?? 0)),
			frameP95Ms: percentile(gaps, 0.95),
			maxFrameMs: Math.max(...gaps),
			longTasks: longTasks.length,
			maxLongTaskMs: Math.max(0, ...longTasks),
		};
	})().then(
		(result) => {
			globalThis.__pixieHistory = { done: true, result };
		},
		(error) => {
			globalThis.__pixieHistory = { done: true, error: String(error) };
		},
	);
	return true;
})();
