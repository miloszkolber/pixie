import { expect, test } from "bun:test";
import {
	describeContextUsage,
	describeTokenCount,
	formatSessionModel,
	formatThinkingLevel,
	releaseAffordanceReason,
	sessionStatusText,
	shouldShowReleaseAffordance,
} from "@/files/changes/details-model";

test("session identity stays explicit when model or thinking is unknown", () => {
	expect(formatSessionModel(null)).toBe("Unknown model");
	expect(formatSessionModel(undefined)).toBe("Unknown model");
	expect(formatSessionModel({ provider: "", id: "", name: "", available: true, hidden: false })).toBe(
		"Unknown model",
	);
	expect(
		formatSessionModel({ provider: "openai", id: "gpt-5", name: "GPT", available: true, hidden: false }),
	).toBe("openai/gpt-5");
	expect(formatThinkingLevel(null)).toBe("Unknown thinking level");
	expect(formatThinkingLevel("")).toBe("Unknown thinking level");
	expect(formatThinkingLevel("high")).toBe("high");
});

test("unknown usage is not zero", () => {
	expect(describeTokenCount(0, undefined)).toBe("Unknown");
	expect(describeTokenCount(0, false)).toBe("Unknown");
	expect(describeTokenCount(128, false)).toBe("Unknown");
	expect(describeTokenCount(0, true)).toBe("0");
	expect(describeTokenCount(2_500, true)).not.toBe("0");
	expect(describeTokenCount(2_500, undefined)).not.toBe("Unknown");
	expect(describeContextUsage(null)).toBe("Unknown");
	expect(describeContextUsage(undefined)).toBe("Unknown");
	expect(
		describeContextUsage({ tokens: null, contextWindow: 128_000, percent: null }),
	).toContain("Unknown");
	expect(
		describeContextUsage({ tokens: 1_000, contextWindow: 128_000, percent: 12.345 }),
	).toContain("% of");
});

test("session status is accessible text, not color alone", () => {
	expect(sessionStatusText({ isStreaming: true, archived: false, live: true })).toBe("Running");
	expect(sessionStatusText({ isStreaming: true, archived: true, live: true })).toBe("Running");
	expect(sessionStatusText({ isStreaming: false, archived: true, live: true })).toBe("Archived");
	expect(sessionStatusText({ isStreaming: false, archived: false, live: true })).toBe("Live");
	expect(sessionStatusText({ isStreaming: false, archived: false, live: false })).toBe("Unloaded");
});

test("idle release is offered only where the backend reports eligibility", () => {
	expect(shouldShowReleaseAffordance({ backendEligible: true, isStreaming: false })).toBe(true);
	expect(shouldShowReleaseAffordance({ backendEligible: true, isStreaming: true })).toBe(false);
	expect(shouldShowReleaseAffordance({ backendEligible: false, isStreaming: false })).toBe(false);
	expect(shouldShowReleaseAffordance({ backendEligible: undefined, isStreaming: false })).toBe(false);
	expect(
		releaseAffordanceReason({ backendEligible: true, isStreaming: false }),
	).toBeNull();
	expect(
		releaseAffordanceReason({ backendEligible: true, isStreaming: true }),
	).toBe("Session is running");
	expect(
		releaseAffordanceReason({
			backendEligible: false,
			backendReason: "queued work still pending",
			isStreaming: false,
		}),
	).toBe("queued work still pending");
	expect(
		releaseAffordanceReason({ backendEligible: false, isStreaming: false }),
	).toBe("Session is not idle");
});
