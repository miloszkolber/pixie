import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	extensionInventory,
	inventoryReference,
} from "../../../agent/pixie-assistant/src/extension-inventory.ts";
import { startHost } from "../../../agent/pixie-assistant/src/server.ts";
import { Sessions } from "../../../agent/pixie-assistant/src/sessions.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const close of cleanup.splice(0).reverse()) await close();
});
async function fixture() {
	const root = await mkdtemp(join(tmpdir(), "pixie-inventory-"));
	cleanup.push(() => rm(root, { recursive: true, force: true }));
	const agentDir = join(root, "agent"),
		cwd = join(root, "project"),
		pkg = join(root, "unfamiliar");
	await mkdir(join(agentDir, "extensions"), { recursive: true });
	await mkdir(join(cwd, ".pi"), { recursive: true });
	await mkdir(pkg);
	await writeFile(
		join(pkg, "package.json"),
		JSON.stringify({
			name: "unfamiliar-native",
			version: "1.2.3",
			pi: { extensions: ["index.js"] },
			secret: "manifest-secret",
		}),
	);
	await writeFile(
		join(pkg, "index.js"),
		`export default pi => {
		pi.registerTool({ name: "unfamiliar_native", label: "Unknown", description: "Unknown", parameters: {type: "object", properties: {}}, execute: async () => ({ content: [], details: {} }) });
		pi.registerCommand("unfamiliar-command", {description: "Unknown", handler: async () => {}});
	};`,
	);
	return { root, agentDir, cwd, pkg };
}

test("read-only native resolution inventories unknown packages, files, missing sources and project precedence without evaluating code", async () => {
	const { root, agentDir, cwd, pkg } = await fixture();
	const marker = join(root, "must-not-exist");
	await writeFile(
		join(agentDir, "extensions", "unknown.js"),
		`import { writeFileSync } from "node:fs"; writeFileSync(${JSON.stringify(marker)}, "evaluated"); export default () => {};`,
	);
	const missing = join(root, "missing-package");
	const missingFile = join(root, "missing-extension.js");
	const commandLog = join(root, "package-commands.jsonl");
	const packageProbe = join(root, "package-probe.ts");
	await writeFile(
		packageProbe,
		`import { appendFileSync } from "node:fs";
		appendFileSync(${JSON.stringify(commandLog)}, JSON.stringify(process.argv.slice(2)) + "\\n");
		console.log(${JSON.stringify(join(root, "legacy-packages"))});`,
	);
	const config = JSON.stringify({
		packages: [pkg, missing, "npm:pixie-fixture-never-installed-482@1.0.0"],
		extensions: [missingFile],
		npmCommand: ["/usr/bin/env", process.execPath, packageProbe],
	});
	await writeFile(join(agentDir, "settings.json"), config);
	await writeFile(join(cwd, ".pi", "settings.json"), JSON.stringify({ packages: [pkg] }));
	const inventory = await extensionInventory(agentDir, cwd);
	expect(inventory.context.reader).toBe("configured-only");
	expect(inventory.extensions).toEqual([]);
	expect(inventory.packages.filter((item) => item.source === pkg)).toEqual([
		expect.objectContaining({ scope: "user", version: "1.2.3", state: "not-observed" }),
		expect.objectContaining({ scope: "project", name: "unfamiliar-native", state: "not-observed" }),
	]);
	expect(
		inventory.resources.filter((item) => item.source === pkg).map((item) => item.scope),
	).toEqual(["project"]);
	expect(inventory.packages.find((item) => item.source === missing)?.state).toBe("missing");
	expect(inventory.packages.find((item) => item.source.startsWith("npm:"))?.installed).toBe(false);
	expect(inventory.paths).toContainEqual({ scope: "user", path: missingFile });
	expect(inventory.resources.some((item) => item.path.endsWith("unknown.js"))).toBe(true);
	await expect(readFile(marker)).rejects.toThrow();
	expect(await readFile(join(agentDir, "settings.json"), "utf8")).toBe(config);
	// The SDK can query legacy global roots, but inspection never installs,
	// updates, or invokes a package lifecycle command.
	const commands = (await readFile(commandLog, "utf8"))
		.trim()
		.split("\n")
		.map((line) => JSON.parse(line));
	expect(commands.length).toBeGreaterThan(0);
	expect(commands.every((command) => JSON.stringify(command) === '["root","-g"]')).toBe(true);
	expect(JSON.stringify(inventory)).not.toContain("manifest-secret");
});

test("actual resident inventory reports native contributions, failed loads, unknown support and configured divergence without raw diagnostics", async () => {
	const { agentDir, cwd, pkg } = await fixture();
	await writeFile(join(agentDir, "settings.json"), JSON.stringify({ packages: [pkg] }));
	await writeFile(
		join(agentDir, "extensions", "broken.js"),
		'export default () => { throw new Error("Authorization: Bearer fixture-credential"); };',
	);
	const sessions = new Sessions(agentDir, [], () => {});
	cleanup.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	await writeFile(join(agentDir, "settings.json"), JSON.stringify({ packages: [] }));
	const inventory = await extensionInventory(
		agentDir,
		cwd,
		entry.session,
		"session",
		entry.session.sessionId,
	);
	expect(inventory.packages).toEqual([]);
	expect(inventory.extensions.find((item) => item.source.source === pkg)).toMatchObject({
		version: null,
		tools: ["unfamiliar_native"],
		commands: ["unfamiliar-command"],
		interfaceSupport: "unknown",
	});
	expect(inventory.errors).toEqual([
		{ path: join(agentDir, "extensions", "broken.js"), code: "load-failed" },
	]);
	expect(JSON.stringify(inventory)).not.toContain("fixture-credential");
	expect(JSON.stringify(inventory)).not.toContain("signet");
});

test("malformed native settings produce an explicit incomplete inventory and references redact URL credentials", async () => {
	const { agentDir, cwd } = await fixture();
	await writeFile(join(agentDir, "settings.json"), '{"token":"secret-value", broken');
	const inventory = await extensionInventory(agentDir, cwd);
	expect(inventory.warnings).toContain("settings-read-failed");
	expect(JSON.stringify(inventory)).not.toContain("secret-value");
	expect(
		inventoryReference("git:https://user:private-key@example.test/pkg?token=secret#private"),
	).toBe("git:https://example.test/pkg");
});

test("assistant inventory requests never reopen an evicted session and include the loaded SDK llama bootstrap", async () => {
	const { agentDir, cwd } = await fixture();
	const host = await startHost({
		agentDir,
		secret: "inventory-fixture-secret",
		port: 0,
		llama: true,
	});
	cleanup.push(() => host.close());
	const entry = await host.sessions.create(cwd);
	const id = entry.session.sessionId;
	const loaded = await extensionInventory(agentDir, cwd, entry.session, "session", id);
	expect(loaded.extensions.some((item) => item.commands.includes("llama"))).toBe(true);
	await host.sessions.release(id);
	const socket = new WebSocket(`ws://127.0.0.1:${host.server.port}/pi`, {
		headers: { Authorization: "Bearer inventory-fixture-secret" },
	});
	cleanup.push(async () => socket.close());
	await new Promise<void>((resolve, reject) => {
		socket.onopen = () => resolve();
		socket.onerror = () => reject(new Error("socket failed"));
	});
	const response = new Promise<unknown>((resolve) => {
		socket.onmessage = (event) => resolve(JSON.parse(String(event.data)));
	});
	socket.send(
		JSON.stringify({ id: 1, method: "pi.extensions.list", params: { sessionId: id, cwd } }),
	);
	expect(await response).toMatchObject({
		result: { context: { reader: "not-resident", sessionId: id }, extensions: [] },
	});
	expect(host.sessions.entries.has(id)).toBe(false);
});
