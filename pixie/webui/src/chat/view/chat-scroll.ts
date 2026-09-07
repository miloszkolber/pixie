export interface ChatScrollTarget {
	scrollToBottom: (behavior: ScrollBehavior) => void;
}

export interface ScrollMetrics {
	scrollTop: number;
	scrollHeight: number;
	clientHeight: number;
}

export interface ChatScrollAnchor {
	scrollTop: number;
	scrollHeight: number;
}

export function chatScrollIsAtBottom(metrics: ScrollMetrics, threshold = 50): boolean {
	return metrics.scrollHeight - metrics.clientHeight - metrics.scrollTop <= threshold;
}

export function captureChatScrollAnchor(metrics: ScrollMetrics): ChatScrollAnchor {
	return { scrollTop: metrics.scrollTop, scrollHeight: metrics.scrollHeight };
}

export function restoredChatScrollTop(anchor: ChatScrollAnchor, nextScrollHeight: number): number {
	return Math.max(0, anchor.scrollTop + nextScrollHeight - anchor.scrollHeight);
}

export interface ChatScrollController {
	readonly showScrollButton: boolean;
	followOutput: (isAtBottom: boolean) => false | "smooth";
	handleAtBottom: (atBottom: boolean) => void;
	scrollToBottom: () => void;
	startInteraction: () => void;
	endInteraction: () => void;
	handleWheel: (deltaY: number) => void;
}

export function createChatScroll(
	target: ChatScrollTarget,
	onVisibilityChange: (visible: boolean) => void = () => {},
): ChatScrollController {
	let atBottom = true;
	let interacting = false;
	let pinnedAway = false;
	let showScrollButton = false;

	const setVisible = (next: boolean) => {
		if (showScrollButton === next) return;
		showScrollButton = next;
		onVisibilityChange(next);
	};

	return {
		get showScrollButton() {
			return showScrollButton;
		},
		followOutput: (isAtBottom) => (!pinnedAway && isAtBottom ? "smooth" : false),
		handleAtBottom: (next) => {
			atBottom = next;
			if (next) pinnedAway = false;
			else if (interacting) pinnedAway = true;
			setVisible(!next);
		},
		scrollToBottom: () => {
			pinnedAway = false;
			target.scrollToBottom("smooth");
		},
		startInteraction: () => {
			interacting = true;
		},
		endInteraction: () => {
			interacting = false;
			if (!atBottom) pinnedAway = true;
		},
		handleWheel: (deltaY) => {
			if (deltaY < 0) pinnedAway = true;
		},
	};
}
