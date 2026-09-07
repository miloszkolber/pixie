import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const component = new URL(
	"../../../webui/src/chat/session/session-goal-control.svelte",
	import.meta.url,
);

test("session goal control compiles as Svelte and preserves the guarded editor contract", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);
	for (const testId of [
		"session-goal-control",
		"session-goal-editor",
		"session-goal-input",
		"session-goal-error",
		"session-goal-clear",
		"session-goal-save",
	])
		expect(source).toContain(`data-testid="${testId}"`);
	expect(source).toContain('request("session.goalGet"');
	expect(source).toContain('request("session.goalSet"');
	expect(source).toContain('request("session.goalClear"');
	expect(source).toContain("queueMicrotask");
	expect(source).toContain("onMount(() =>");
	expect(source).toContain("load(id, areaId)");
	expect(source).toContain("normalizeSessionGoal(draft)");
	expect(source).toContain("maxlength={SESSION_GOAL_MAX_LENGTH + 1}");
	expect(source).toContain("generation !== requestGeneration");
	expect(source).toContain("goalRevision");
	expect(source).toContain("setSessionGoal(id, value, goalRevision)");
	expect(source).toContain("setSessionGoal(sessionId, value, goalRevision)");
	expect(source).toContain("requestGeneration += 1");
	expect(source).toContain('role="alert"');
	expect(source).toContain("Managed by the agent");
	expect(source).toContain("cannot access or update this goal");
});
