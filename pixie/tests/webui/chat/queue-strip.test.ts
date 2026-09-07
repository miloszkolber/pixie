import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const component = new URL("../../../webui/src/chat/session/queue-strip.svelte", import.meta.url);

test("the queue strip compiles as Svelte and preserves its interactive contract", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);

	for (const testId of [
		"queue-strip",
		"queue-item",
		"queue-item-retry",
		"queue-item-edit",
		"queue-item-remove",
	]) {
		expect(source).toContain(`data-testid="${testId}"`);
	}
	expect(source).toContain("data-kind={item.kind}");
	expect(source).toContain("data-index={item.index}");
	expect(source.match(/disabled=\{!queue\.revision\}/g)).toHaveLength(3);
	expect(source).toContain("onclick={() => onRetry(item.kind, item.index)}");
	expect(source).toContain("onclick={() => onEdit(item.kind, item.index)}");
	expect(source).toContain("onclick={() => onRemove(item.kind, item.index)}");
});

test("uncertain follow-ups explain and expose only the explicit blocked retry", async () => {
	const source = await Bun.file(component).text();
	const steering = source.indexOf("...queue.steering.map");
	const followUp = source.indexOf("...queue.followUp.map");
	expect(steering).toBeGreaterThan(-1);
	expect(followUp).toBeGreaterThan(steering);
	expect(source).toContain(
		"queue.blocked?.lane === item.kind && queue.blocked.index === item.index",
	);
	expect(source).toContain("{#if item.blocked}");
	expect(source).toContain("may already be sent");
	expect(source).toContain("check the transcript before retrying");
	expect(source).toContain(`Send queued message again (may duplicate): \${item.text}`);
});
