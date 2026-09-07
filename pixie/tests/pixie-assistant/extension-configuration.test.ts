import { afterEach, expect, test } from "bun:test";
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { SettingsManager, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";
import { configureExtension } from "../../../pi/pixie-assistant/src/extension-configuration.ts";
import { extensionInventory } from "../../../pi/pixie-assistant/src/extension-inventory.ts";
import { SESSION_LIVENESS_EVENT, Sessions } from "../../../pi/pixie-assistant/src/sessions.ts";
import { makeProvider } from "./provider-fixture.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => { for (const fn of cleanup.splice(0).reverse()) await fn(); });
async function fixture() {
	const root = await mkdtemp(join(tmpdir(), "pixie-native-configuration-"));
	cleanup.push(() => rm(root, { recursive: true, force: true }));
	const agentDir = join(root, "agent"), cwd = join(root, "project");
	await mkdir(join(agentDir, "extensions"), { recursive: true });
	await mkdir(join(cwd, ".pi", "extensions"), { recursive: true });
	const extension = join(agentDir, "extensions", "unfamiliar.js");
	await writeFile(extension, `export default pi => {
		pi.registerTool({ name: "native_configuration_probe", label: "Probe", description: "Probe", parameters: { type: "object", properties: {} }, execute: async () => ({content: [], details: {}}) });
		pi.on("session_start", (_event, ctx) => ctx.ui.setStatus("native-probe", "running"));
	};`);
	await writeFile(join(agentDir, "settings.json"), JSON.stringify({ extensions: [], future: { keep: "unchanged" } }));
	return { root, agentDir, cwd, extension };
}
async function requestFor(agentDir: string, cwd: string, path: string, enabled: boolean): Promise<Parameters<typeof configureExtension>[2]> {
	const inventory = await extensionInventory(agentDir, cwd);
	const resource = inventory.resources.find((item) => item.path === path);
	if (!resource || (resource.scope !== "user" && resource.scope !== "project")) throw new Error("Missing native fixture resource");
	return { scope: resource.scope, resourceKey: resource.resourceKey,
		expectedRevision: inventory.configurationRevisions[resource.scope], enabled, confirmed: true };
}

test("native user and project file filters save exact scope and leave loaded sessions unchanged", async () => {
	const { agentDir, cwd, extension } = await fixture();
	const projectExtension = join(cwd, ".pi", "extensions", "project.js");
	await writeFile(projectExtension, "export default () => {};");
	await writeFile(join(cwd, ".pi", "settings.json"), JSON.stringify({ futureProject: 42 }));
	const sessions = new Sessions(agentDir, [], () => {});
	cleanup.push(() => sessions.close());
	const original = await sessions.create(cwd);
	await chmod(join(agentDir, "settings.json"), 0o640);
	expect(await configureExtension(agentDir, cwd, await requestFor(agentDir, cwd, extension, false))).toMatchObject({ saved: true, loaded: false, reload: "deferred" });
	const configured = JSON.parse(await readFile(join(agentDir, "settings.json"), "utf8"));
	expect((await stat(join(agentDir, "settings.json"))).mode & 0o777).toBe(0o640);
	expect(configured).toMatchObject({ future: { keep: "unchanged" }, extensions: [`-${extension}`] });
	expect(original.session.getActiveToolNames()).toContain("native_configuration_probe");
	const inventory = await extensionInventory(agentDir, cwd, original.session, "session", original.session.sessionId);
	expect(inventory.resources.find((item) => item.path === extension)).toMatchObject({ enabled: false, state: "loaded" });
	const next = await sessions.create(cwd);
	expect(next.session.getActiveToolNames()).not.toContain("native_configuration_probe");
	await configureExtension(agentDir, cwd, await requestFor(agentDir, cwd, projectExtension, false));
	expect(JSON.parse(await readFile(join(cwd, ".pi", "settings.json"), "utf8"))).toEqual({ futureProject: 42, extensions: [`-${projectExtension}`] });
	expect(JSON.parse(await readFile(join(agentDir, "settings.json"), "utf8"))).toEqual(configured);
	await configureExtension(agentDir, cwd, await requestFor(agentDir, cwd, extension, true));
	const enabled = await sessions.create(cwd);
	expect(enabled.session.getActiveToolNames().filter((name) => name === "native_configuration_probe")).toHaveLength(1);
});

test("unsupported native package-file filters and unconfirmed changes leave settings untouched", async () => {
	const { agentDir, cwd, extension } = await fixture();
	const path = join(agentDir, "settings.json");
	const before = JSON.stringify({ packages: [extension], future: "preserve" });
	await writeFile(path, before);
	const request = await requestFor(agentDir, cwd, extension, false);
	await expect(configureExtension(agentDir, cwd, { ...request, confirmed: false })).rejects.toThrow("Confirm");
	await expect(configureExtension(agentDir, cwd, request)).rejects.toThrow("cannot apply");
	expect(await readFile(path, "utf8")).toBe(before);
});

test("native package filters enable only the selected resource and preserve other resource filters and unknown keys", async () => {
	const { root, agentDir, cwd } = await fixture();
	const pkg = join(root, "package"); await mkdir(pkg);
	for (const name of ["one.js", "two.js"]) await writeFile(join(pkg, name), "export default () => {};");
	await writeFile(join(pkg, "package.json"), JSON.stringify({ name: "unfamiliar-configurable", version: "1.0.0", pi: { extensions: ["one.js", "two.js"] } }));
	await writeFile(join(agentDir, "settings.json"), JSON.stringify({ packages: [{ source: pkg, extensions: [], skills: ["kept/**"], future: "keep" }], untouched: true }));
	await configureExtension(agentDir, cwd, await requestFor(agentDir, cwd, join(pkg, "one.js"), true));
	const current = JSON.parse(await readFile(join(agentDir, "settings.json"), "utf8"));
	expect(current).toMatchObject({ untouched: true, packages: [{ source: pkg, extensions: ["!**", "+one.js"], skills: ["kept/**"], future: "keep" }] });
	const inventory = await extensionInventory(agentDir, cwd);
	expect(inventory.resources.find((item) => item.path === join(pkg, "one.js"))?.enabled).toBe(true);
	expect(inventory.resources.find((item) => item.path === join(pkg, "two.js"))?.enabled).toBe(false);
});

test("scoped revisions reject stale filters and malformed settings without overwriting edits", async () => {
	const { agentDir, cwd, extension } = await fixture();
	const path = join(agentDir, "settings.json");
	const request = await requestFor(agentDir, cwd, extension, false);
	const changed = JSON.stringify({ extensions: ["!other.js"], future: "concurrent" });
	await writeFile(path, changed);
	await expect(configureExtension(agentDir, cwd, request)).rejects.toThrow("changed");
	expect(await readFile(path, "utf8")).toBe(changed);
	const malformed = '{"credential":"do-not-print", invalid';
	await writeFile(path, malformed);
	await expect(configureExtension(agentDir, cwd, request)).rejects.toThrow("unreadable");
	expect(await readFile(path, "utf8")).toBe(malformed);
});

test("write-time compare-and-set preserves unrelated concurrent edits and rejects concurrent filter edits", async () => {
	const { agentDir, cwd, extension } = await fixture();
	const path = join(agentDir, "settings.json");
	const originalSetter = SettingsManager.prototype.setExtensionPaths;
	try {
		for (const conflict of [false, true]) {
			const request = await requestFor(agentDir, cwd, extension, conflict);
			SettingsManager.prototype.setExtensionPaths = function(paths) {
				const latest = JSON.parse(readFileSync(path, "utf8"));
				latest.otherProcess = { preserved: true };
				if (conflict) latest.extensions.push("!concurrent.js");
				writeFileSync(path, JSON.stringify(latest));
				originalSetter.call(this, paths);
			};
			if (conflict) await expect(configureExtension(agentDir, cwd, request)).rejects.toThrow("conflicted");
			else expect((await configureExtension(agentDir, cwd, request)).saved).toBe(true);
			expect(JSON.parse(await readFile(path, "utf8")).otherProcess).toEqual({ preserved: true });
			if (conflict) expect(JSON.parse(await readFile(path, "utf8")).extensions).toContain("!concurrent.js");
		}
	} finally { SettingsManager.prototype.setExtensionPaths = originalSetter; }
});

test("repeated deferred reload leaves another native session's active provider, tools, listeners and passive state intact", async () => {
	const { agentDir, cwd, extension } = await fixture();
	let finish!: () => void;
	const pause = new Promise<void>((resolve) => { finish = resolve; });
	const events: unknown[] = [];
	const apis: ExtensionAPI[] = [];
	const contexts: ExtensionContext[] = [];
	let starts = 0, shutdowns = 0;
	const sessions = new Sessions(agentDir, [makeProvider(pause), (pi) => {
		apis.push(pi);
		pi.on("session_start", (_event, context) => { starts++; contexts.push(context); });
		pi.on("session_shutdown", () => { shutdowns++; });
	}], (_id, event) => events.push(event));
	cleanup.push(() => sessions.close());
	cleanup.push(async () => { finish(); });
	const idle = await sessions.create(cwd), active = await sessions.create(cwd);
	await active.session.setModel(active.modelRuntime.getModel("fixture", "echo")!);
	const run = sessions.call("session.prompt", { sessionId: active.session.sessionId, content: [{ type: "text", text: "still running" }] });
	try {
		for (let i = 0; i < 100 && active.session.isIdle; i++) await Bun.sleep(5);
		await configureExtension(agentDir, cwd, await requestFor(agentDir, cwd, extension, false));
		const eventCount = events.length;
		for (let i = 0; i < 3; i++) {
			expect(sessions.nativeReloadStatus(idle.session.sessionId)).toMatchObject({ loaded: false, reload: "deferred", reason: "sdk-loader-install-policy" });
			expect(sessions.nativeReloadStatus(active.session.sessionId).reason).toBe("session-busy");
		}
		expect(sessions.entries.get(idle.session.sessionId)).toBe(idle);
		expect(sessions.entries.get(active.session.sessionId)).toBe(active);
		expect(starts).toBe(2); expect(shutdowns).toBe(0); expect(events).toHaveLength(eventCount);
		expect(idle.session.getActiveToolNames().filter((name) => name === "native_configuration_probe")).toHaveLength(1);
		apis[0]!.events.emit(SESSION_LIVENESS_EVENT, { key: "background", active: true });
		expect(sessions.nativeReloadStatus(idle.session.sessionId).reason).toBe("session-busy");
		apis[0]!.events.emit(SESSION_LIVENESS_EVENT, { key: "background", active: false });
		let answered = false;
		const dialog = contexts[0]!.ui.input("Keep this dialog").then((value) => { answered = true; return value; });
		expect(sessions.nativeReloadStatus(idle.session.sessionId).reason).toBe("session-busy");
		expect(answered).toBe(false);
		const request = events.findLast((event) => (event as {type: string}).type === "pixie:ui:request") as { requestId: string };
		await sessions.resolveUiResponse({ sessionId: idle.session.sessionId, requestId: request.requestId, value: "kept" });
		expect(await dialog).toBe("kept");
		finish(); await run;
		expect(active.session.messages.some((message) => message.role === "assistant")).toBe(true);
	} finally { finish(); await run.catch(() => {}); }
});

test("saving an enabled extension does not claim it loaded or roll back configuration after a later native load failure", async () => {
	const { agentDir, cwd, extension } = await fixture();
	await writeFile(extension, 'export default () => { throw new Error("fixture load failed"); };');
	await configureExtension(agentDir, cwd, await requestFor(agentDir, cwd, extension, false));
	const saved = await configureExtension(agentDir, cwd, await requestFor(agentDir, cwd, extension, true));
	expect(saved).toMatchObject({ saved: true, loaded: false, reload: "deferred" });
	const sessions = new Sessions(agentDir, [], () => {});
	cleanup.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	const inventory = await extensionInventory(agentDir, cwd, entry.session, "session", entry.session.sessionId);
	expect(inventory.errors).toEqual([{ path: extension, code: "load-failed" }]);
	expect(inventory.resources.find((resource) => resource.path === extension)).toMatchObject({ enabled: true, state: "failed" });
});
