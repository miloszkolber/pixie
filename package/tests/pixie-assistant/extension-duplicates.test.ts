import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { extensionInventory } from "../../../assistant/src/extension-inventory.ts";
import { Sessions } from "../../../assistant/src/sessions.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const close of cleanup.splice(0).reverse()) await close();
});

async function fixture() {
	const root = await mkdtemp(join(tmpdir(), "pixie-duplicate-tools-"));
	cleanup.push(() => rm(root, { recursive: true, force: true }));
	const agentDir = join(root, "agent"),
		cwd = join(root, "project");
	await mkdir(join(agentDir, "extensions"), { recursive: true });
	await mkdir(cwd);
	const first = join(agentDir, "extensions", "first.js");
	const second = join(agentDir, "extensions", "second.js");
	for (const [path, tag] of [
		[first, "first"],
		[second, "second"],
	] as const) {
		await writeFile(
			path,
			`export default pi => {
			pi.registerTool({ name: "shared_probe_tool", label: "${tag}", description: "${tag}", parameters: {type: "object", properties: {}}, execute: async () => ({ content: [{type: "text", text: "${tag}"}], details: {} }) });
		};`,
		);
	}
	await writeFile(join(agentDir, "settings.json"), JSON.stringify({}));
	return { agentDir, cwd, first, second };
}

test("duplicate tool names across extensions keep one registration and surface the conflict", async () => {
	const { agentDir, cwd, first, second } = await fixture();
	const sessions = new Sessions(agentDir, [], () => {});
	cleanup.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	// The session still builds: exactly one registration wins instead of
	// shadowing silently or failing the whole load.
	expect(
		entry.session.getActiveToolNames().filter((name) => name === "shared_probe_tool"),
	).toHaveLength(1);
	const inventory = sessions.inventory(entry);
	expect(inventory.errors).toHaveLength(1);
	const [conflict] = inventory.errors;
	expect([conflict.path, conflict.error].join("\n")).toContain(first);
	expect([conflict.path, conflict.error].join("\n")).toContain(second);
	// The configured-only reader reports the same files without evaluating them.
	const configured = await extensionInventory(agentDir, cwd);
	expect(configured.errors).toEqual([]);
	expect(
		configured.resources.filter((item) => item.path === first || item.path === second),
	).toHaveLength(2);
});
