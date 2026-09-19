(async () => {
	const frame = () => new Promise((resolve) => requestAnimationFrame(resolve));
	const viewport = document.querySelector('[data-testid="chat-scroll"]');
	const rows = [...viewport.querySelectorAll("[data-chat-row]")];
	const first = rows.find((row) => row.textContent?.includes("History answer 00000"));
	if (!first) throw new Error("First history answer is absent");
	const last = rows.findLast((row) => row.querySelector(".chat-markdown-code"));
	if (!last) throw new Error("Visible code was not highlighted");
	const range = document.createRange();
	range.selectNodeContents(last.querySelector("code"));
	const selection = document.getSelection();
	selection.removeAllRanges();
	selection.addRange(range);
	const selected = selection.toString();
	first.scrollIntoView({ block: "center" });
	for (let index = 0; index < 5; index++) await frame();
	if (selection.toString() !== selected || !last.isConnected)
		throw new Error("Scrolling replaced selected code");
	selection.removeAllRanges();
	if (typeof window.find !== "function" || !window.find("History answer 00000", false, false, true))
		throw new Error("Native find lost old transcript text");
	selection.removeAllRanges();
	const link = first.querySelector("a");
	link.focus();
	if (document.activeElement !== link || link.target !== "_blank" || !link.rel.includes("noopener"))
		throw new Error("Historical links lost focus or isolation");
	const jump = document.querySelector('[data-testid="scroll-to-bottom"]');
	if (!jump) throw new Error("History jump control is unavailable");
	jump.focus();
	jump.click();
	await frame();
	if (document.activeElement !== viewport || jump.isConnected)
		throw new Error("Jump control lost keyboard focus when hidden");
	if (viewport.getAttribute("aria-live") !== "off")
		throw new Error("Transcript text is still an automatic live announcement");
	const deadline = performance.now() + 5000;
	while (
		viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop > 50 ||
		document.querySelector('[data-testid="scroll-to-bottom"]')
	) {
		if (performance.now() > deadline) throw new Error("Jump did not reach the conversation bottom");
		await frame();
	}
	await frame();
	await frame();
	const finalAnswer = rows.findLast((row) => row.textContent?.includes("Loaded answer"));
	const finalBounds = finalAnswer?.getBoundingClientRect();
	const bounds = viewport.getBoundingClientRect();
	if (!finalBounds || finalBounds.top >= bounds.bottom || finalBounds.bottom <= bounds.top)
		throw new Error("Jump left the final message outside the reading viewport");
	if (!first.isConnected || !last.isConnected)
		throw new Error("Transcript rows were remounted during traversal");
	return {
		nativeFind: true,
		selectionPreserved: true,
		historicalLinkFocus: true,
		rowsRetained: true,
		jumpFocus: true,
		jumpReachedFinalMessage: true,
		transcriptAnnouncementsOff: true,
	};
})();
