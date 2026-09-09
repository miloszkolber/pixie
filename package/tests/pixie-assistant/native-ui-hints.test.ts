import { expect, test } from "bun:test";
import {
	createCancellationForward,
	createWorkingHint,
	describeForwardedVsAccepted,
	FC15_CANCELLATION_BLOCKER,
	FC15_WORKING_BLOCKER,
	isNativeRequestCancellationObservable,
	isNativeWorkingMessageObservable,
	NATIVE_BASELINE,
} from "../../../assistant/src/extensions/native-ui-hints.ts";

test("FC15 blockers name the exact absent public API with reproduction and consequence", () => {
	for (const blocker of [FC15_WORKING_BLOCKER, FC15_CANCELLATION_BLOCKER]) {
		expect(blocker.fcId).toBe("FC15");
		expect(blocker.distribution).toContain("0.85.1");
		expect(blocker.missingPublicSymbol.length).toBeGreaterThan(0);
		expect(blocker.reproduction.length).toBeGreaterThan(0);
		expect(blocker.attemptedAlternatives.length).toBeGreaterThan(0);
		expect(blocker.userVisibleLimitation.length).toBeGreaterThan(0);
		expect(blocker.owner).toBe("E/B");
		expect(blocker.releaseConsequence).toContain("stays open");
	}
	expect(FC15_WORKING_BLOCKER.missingPublicSymbol).toContain("setWorkingMessage");
	expect(FC15_CANCELLATION_BLOCKER.missingPublicSymbol).toContain("cancellation");
	expect(FC15_WORKING_BLOCKER.attemptedAlternatives.join(" ")).not.toContain("timing guesses are sound");
	expect(NATIVE_BASELINE.distribution).toBe("Pi 0.85.1");
	expect(isNativeWorkingMessageObservable()).toBe(false);
	expect(isNativeRequestCancellationObservable()).toBe(false);
});

test("working hints are Pixie-local projections and never native acceptance", () => {
	const hint = createWorkingHint({ sessionId: "sess-1", message: "working" });
	expect(hint).toMatchObject({
		kind: "pixie-local-working-hint",
		sessionId: "sess-1",
		message: "working",
		nativeAccepted: false,
	});
	expect(hint.nativeAccepted).not.toBe(true as unknown as boolean);
	expect(hint.blocker.fcId).toBe("FC15");
	expect(() => createWorkingHint({ sessionId: "" })).toThrow("session identity");
	const long = createWorkingHint({ sessionId: "sess-1", message: "x".repeat(5000) });
	expect(long.message?.length).toBe(2000);
});

test("cancellation forwards require the exact request ID and never claim native cancellation", () => {
	const forward = createCancellationForward({
		sessionId: "sess-1",
		requestId: "req-exact-1",
		reason: "aborted",
	});
	expect(forward).toMatchObject({
		kind: "pixie-cancellation-forward",
		sessionId: "sess-1",
		requestId: "req-exact-1",
		forwarded: true,
		nativeCancelled: "unknown",
	});
	expect(forward.nativeCancelled).not.toBe("true" as unknown as string);
	expect(forward.blocker.fcId).toBe("FC15");
	expect(() => createCancellationForward({ sessionId: "sess-1", requestId: "" })).toThrow(
		"exact request ID",
	);
	expect(() => createCancellationForward({ sessionId: "", requestId: "req-1" })).toThrow(
		"session identity",
	);
	const def = createCancellationForward({ sessionId: "sess-1", requestId: "req-1" });
	expect(def.reason).toBe("cancelled");
});

test("forwarded delivery is distinguished from native acceptance", () => {
	const text = describeForwardedVsAccepted();
	expect(text).toContain("Forwarded");
	expect(text).toContain("Accepted");
	expect(text).toContain("unknown");
});
