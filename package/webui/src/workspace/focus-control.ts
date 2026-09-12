function isDisabled(element: HTMLElement): boolean {
	return (
		element.matches(":disabled, [aria-disabled='true']") ||
		element.closest("fieldset[disabled]") !== null
	);
}

/** A candidate is focusable only when it is connected, visible, and enabled. */
export function isFocusableControl(element: HTMLElement): boolean {
	if (
		!element.isConnected ||
		isDisabled(element) ||
		element.hidden ||
		element.closest('[aria-hidden="true"],[inert]') !== null
	)
		return false;
	const style = getComputedStyle(element);
	if (style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse")
		return false;
	// offsetParent misses fixed-position controls; client rects cover both cases.
	return element.getClientRects().length > 0 || element.offsetParent !== null;
}

export function focusFirstVisible(...selectors: string[]): boolean {
	const candidates = [
		...new Set(
			selectors.flatMap((selector) => [...document.querySelectorAll<HTMLElement>(selector)]),
		),
	];
	for (const candidate of candidates) {
		if (isFocusableControl(candidate)) {
			candidate.focus();
			return true;
		}
	}
	return false;
}

/** A panel counts as focusable when it owns at least one tabbable descendant. */
export function panelHasFocusableContent(panel: HTMLElement | null): boolean {
	if (!panel || !isFocusablePanel(panel)) return false;
	return [
		...panel.querySelectorAll<HTMLElement>(
			'button, a[href], input, select, textarea, [contenteditable="true"], [tabindex]:not([tabindex="-1"])',
		),
	].some(isFocusableControl);
}

function isFocusablePanel(panel: HTMLElement): boolean {
	return (
		panel.isConnected &&
		!panel.hidden &&
		panel.closest('[aria-hidden="true"],[inert]') === null &&
		getComputedStyle(panel).display !== "none" &&
		getComputedStyle(panel).visibility !== "hidden"
	);
}
