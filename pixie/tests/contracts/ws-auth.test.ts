import { describe, expect, test } from "bun:test";
import {
	CODE_TOKEN_MIN_LENGTH,
	isCodeToken,
	isStrongToken,
	TOKEN_SENTINELS,
} from "../../contracts/src/ws-auth";

const strongToken = "controller-token-0123456789abcdef0123456789";

describe("authentication token validation", () => {
	test("rejects tokens shorter than the minimum", () => {
		expect(isStrongToken("x".repeat(CODE_TOKEN_MIN_LENGTH - 1))).toBe(false);
		expect(isCodeToken("short-controller-token")).toBe(false);
	});

	test("rejects documented setup sentinels", () => {
		for (const sentinel of TOKEN_SENTINELS) expect(isStrongToken(sentinel)).toBe(false);
	});

	test("accepts a strong printable token for the browser service", () => {
		expect(isStrongToken(strongToken)).toBe(true);
		expect(isCodeToken(strongToken)).toBe(true);
	});
});
