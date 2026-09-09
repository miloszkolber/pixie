import { expect, test } from "bun:test";
import {
	MAX_IMAGE_AGGREGATE_BYTES,
	MAX_IMAGE_BYTES,
	MAX_IMAGES,
	MAX_PROMPT_TEXT_BYTES,
	highestSupportedThinkingLevel,
	normalizeNativeModels,
	redactSecrets,
	resolveNativeTrust,
	validateCreateRequest,
	validateModelSelection,
	validatePromptRequest,
	validateThinkingSelection,
} from "../../../assistant/src/session/validation.ts";

const identity = {
	sessionKey: "session-key",
	sessionId: "native-session",
	nativeSessionId: "native-session",
	bootId: "boot-1",
	childGeneration: 0,
	cwd: "/tmp/project",
	agentDir: "/tmp/agent",
};

const image = (data = "aGVsbG8=") => ({ type: "image" as const, mimeType: "image/png", data });

test("native trust loads user resources but never auto-trusts project resources", () => {
	const resources = [
		{ path: "/tmp/agent/extensions/user.js", scope: "user" as const },
		{ path: "/tmp/project/.pi/extensions/project.js", scope: "project" as const },
	];
	const undecided = resolveNativeTrust({ resources, projectTrust: null, defaultProjectTrust: "ask" });
	if (!undecided.ok) throw new Error(undecided.error.message);
	expect(undecided.value.trust).toMatchObject({ state: "untrusted", allowed: false, reason: "trust-required" });
	expect(undecided.value.loadedResources).toEqual([resources[0]]);

	const trusted = resolveNativeTrust({ resources, projectTrust: true, defaultProjectTrust: "ask" });
	if (!trusted.ok) throw new Error(trusted.error.message);
	expect(trusted.value.trust).toMatchObject({ state: "trusted", allowed: true, reason: "stored-decision" });
	expect(trusted.value.loadedResources).toHaveLength(2);

	const defaultTrusted = resolveNativeTrust({ resources, defaultProjectTrust: "always" });
	if (!defaultTrusted.ok) throw new Error(defaultTrusted.error.message);
	expect(defaultTrusted.value.trust.reason).toBe("default-always");
});

test("create validation keeps model and native resource identities bounded", () => {
	const result = validateCreateRequest({
		identity,
		resources: [{ path: "/tmp/project/.pi/extensions/project.js", scope: "project" }],
		projectTrust: true,
		availableModels: [{ provider: "fixture", id: "echo", name: "Echo", apiKey: "do-not-replay" } as never],
		availableThinkingLevels: ["minimal", "max"],
		model: { provider: "fixture", id: "echo" },
		thinkingLevel: "max",
	});
	if (!result.ok) throw new Error(result.error.message);
	expect(result.value.model).toEqual({ provider: "fixture", id: "echo" });
	expect(JSON.stringify(result.value)).not.toContain("do-not-replay");
	expect(validateCreateRequest({ identity: { ...identity, cwd: "relative" } }).ok).toBe(false);
});

test("image validation enforces per-image, count and aggregate byte bounds", () => {
	const within = validatePromptRequest({
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-1",
		runId: "run-1",
		content: [image()],
	});
	if (!within.ok) throw new Error(within.error.message);
	expect(within.value.images).toHaveLength(1);
	expect(within.value.replay).toEqual([{ type: "image", mimeType: "image/png", bytes: 8 }]);
	expect(JSON.stringify(within.value.replay)).not.toContain("aGVsbG8=");

	const tooMany = validatePromptRequest({
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-2",
		runId: "run-2",
		content: Array.from({ length: MAX_IMAGES + 1 }, () => image()),
	});
	expect(tooMany).toMatchObject({ ok: false, error: { message: "Prompt contains too many images" } });

	const perImage = validatePromptRequest({
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-3",
		runId: "run-3",
		content: [image("A".repeat(MAX_IMAGE_BYTES + 4))],
	});
	expect(perImage).toMatchObject({ ok: false, error: { message: "Image exceeds the per-image limit" } });

	const aggregateData = "A".repeat(Math.floor(MAX_IMAGE_AGGREGATE_BYTES / 8) + 4);
	const aggregate = validatePromptRequest({
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-4",
		runId: "run-4",
		content: Array.from({ length: 8 }, () => image(aggregateData)),
	});
	expect(aggregate).toMatchObject({ ok: false, error: { message: "Prompt images exceed the aggregate limit" } });
});

test("prompt text uses UTF-8 bytes and resource replay omits raw contents", () => {
	const valid = validatePromptRequest({
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-5",
		runId: "run-5",
		content: [
			{ type: "text", text: "é" },
			{
				type: "resource",
				resource: {
					uri: "pixie://attachment/a.txt",
					mimeType: "text/plain",
					text: "provider-secret-like user content",
					_meta: { name: "a.txt" },
				},
			},
		],
	});
	if (!valid.ok) throw new Error(valid.error.message);
	expect(valid.value.text).toContain("attached-file");
	expect(valid.value.replay).toEqual([
		{ type: "text", text: "é" },
		{ type: "resource", uri: "pixie://attachment/a.txt", mimeType: "text/plain", name: "a.txt" },
	]);
	expect(JSON.stringify(valid.value.replay)).not.toContain("provider-secret-like");

	const tooLong = validatePromptRequest({
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-6",
		runId: "run-6",
		content: [{ type: "text", text: "é".repeat(Math.floor(MAX_PROMPT_TEXT_BYTES / 2) + 1) }],
	});
	expect(tooLong).toMatchObject({ ok: false, error: { message: "Prompt text exceeds the limit" } });
});

test("model and thinking validation reject stale values without coercion", () => {
	const models = normalizeNativeModels([
		{ provider: "fixture", id: "echo" },
		{ provider: "fixture", id: "echo" },
	]);
	if (!models.ok) throw new Error(models.error.message);
	expect(models.value).toHaveLength(1);
	expect(validateModelSelection({ provider: "fixture", id: "missing" }, models.value)).toMatchObject({
		ok: false,
		error: { code: "unknown-model" },
	});
	expect(validateThinkingSelection("max", ["minimal", "max"])).toEqual({ ok: true, value: "max" });
	expect(validateThinkingSelection("high", ["minimal", "max"])).toMatchObject({
		ok: false,
		error: { code: "unsupported-thinking" },
	});
	expect(highestSupportedThinkingLevel(["minimal", "unknown-future-level", "max"])).toBe("max");
	expect(highestSupportedThinkingLevel(["unknown-future-level"])).toBeUndefined();
});

test("secret-shaped fields are omitted from replay values", () => {
	const value = redactSecrets({
		provider: "fixture",
		authorization: "Bearer secret",
		nested: { apiKey: "secret", visible: "kept" },
		items: [{ token: "secret", value: 1 }],
	});
	expect(value).toEqual({ provider: "fixture", nested: { visible: "kept" }, items: [{ value: 1 }] });
});
