import { describe, expect, test } from "bun:test";
import {
	captureChatScrollAnchor,
	chatScrollIsAtBottom,
	createChatScroll,
	restoredChatScrollTop,
} from "@/chat/view/chat-scroll";

describe("chat scroll controller", () => {
	test("follows output until the reader deliberately leaves the bottom", () => {
		const calls: ScrollBehavior[] = [];
		const visibility: boolean[] = [];
		const scroll = createChatScroll(
			{ scrollToBottom: (behavior) => calls.push(behavior) },
			(visible) => visibility.push(visible),
		);

		expect(scroll.followOutput(true)).toBe("smooth");
		scroll.startInteraction();
		scroll.handleAtBottom(false);
		scroll.endInteraction();
		expect(scroll.showScrollButton).toBe(true);
		expect(scroll.followOutput(true)).toBe(false);
		expect(visibility).toEqual([true]);

		scroll.scrollToBottom();
		expect(calls).toEqual(["smooth"]);
		scroll.handleAtBottom(true);
		expect(scroll.showScrollButton).toBe(false);
		expect(scroll.followOutput(true)).toBe("smooth");
		expect(visibility).toEqual([true, false]);
	});

	test("upward wheel movement pins the reader away from streaming output", () => {
		const scroll = createChatScroll({ scrollToBottom: () => {} });
		scroll.handleWheel(-1);
		expect(scroll.followOutput(true)).toBe(false);
	});

	test("detects the live edge with a threshold and preserves position across prepends", () => {
		expect(chatScrollIsAtBottom({ scrollTop: 450, scrollHeight: 1_000, clientHeight: 500 })).toBe(
			true,
		);
		expect(chatScrollIsAtBottom({ scrollTop: 449, scrollHeight: 1_000, clientHeight: 500 })).toBe(
			false,
		);
		const anchor = captureChatScrollAnchor({
			scrollTop: 120,
			scrollHeight: 900,
			clientHeight: 500,
		});
		expect(restoredChatScrollTop(anchor, 1_240)).toBe(460);
	});
});
