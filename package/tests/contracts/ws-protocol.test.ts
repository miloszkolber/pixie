import { expect, test } from "bun:test";
import {
	normalizeSessionTitle,
	SESSION_TITLE_MAX_LENGTH,
} from "../../contracts/src/agent-protocol";
import { REQUEST_IMAGE_BASE64_BUDGET } from "../../contracts/src/domain";
import { MAX_SERIALIZED_WS_REQUEST_BYTES } from "../../contracts/src/ws-protocol";

test("the WebSocket envelope fits the accepted aggregate image budget", () => {
	const request = JSON.stringify({
		id: "request-id",
		method: "session.prompt",
		params: {
			sessionId: "session-id",
			text: "",
			images: [
				{ type: "image", mimeType: "image/png", data: "A".repeat(REQUEST_IMAGE_BASE64_BUDGET) },
			],
		},
	});

	expect(Buffer.byteLength(request)).toBeGreaterThan(REQUEST_IMAGE_BASE64_BUDGET);
	expect(Buffer.byteLength(request)).toBeLessThanOrEqual(MAX_SERIALIZED_WS_REQUEST_BYTES);
});

test("session lifecycle titles are normalized and bounded", () => {
	expect(normalizeSessionTitle("  Focused chat  ")).toBe("Focused chat");
	expect(() => normalizeSessionTitle("   ")).toThrow("cannot be empty");
	expect(() => normalizeSessionTitle(`bad\0title`)).toThrow("invalid character");
	expect(() => normalizeSessionTitle("x".repeat(SESSION_TITLE_MAX_LENGTH + 1))).toThrow(
		`${SESSION_TITLE_MAX_LENGTH} characters or fewer`,
	);
});
