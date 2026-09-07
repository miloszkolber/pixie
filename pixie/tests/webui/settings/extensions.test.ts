import { expect, test } from "bun:test";
import type { NativeExtensionInventory } from "@pixie/contracts";
import type { WsTransport } from "@/connection/transport";
import { ExtensionsModel } from "@/settings/sections/extensions-model";
import { settingsTabs } from "@/settings/settings-dialog";
import { renderSvelte } from "../chat/svelte-render";

const empty = (sessionId: string | null = null): NativeExtensionInventory => ({
	version: 1, context: { cwd: "/project", sessionId, reader: sessionId ? "session" : "configured-only" },
	packages: [], paths: [], resources: [], extensions: [], errors: [], warnings: [],
});

test("native inventory requests retain exact target and reject stale replies, errors and wrong session results", async () => {
	const requests: { params: unknown; signal: AbortSignal; resolve: (value: NativeExtensionInventory) => void; reject: (reason: unknown) => void }[] = [];
	const transport = { request: (method: string, params: unknown, options: {signal: AbortSignal}) => {
		expect(method).toBe("pi.nativeExtensions");
		return new Promise<NativeExtensionInventory>((resolve, reject) => requests.push({params, signal: options.signal, resolve, reject}));
	} } as unknown as Pick<WsTransport, "request">;
	const target = { projectId: "p", root: "/project", sessionId: "chat" };
	const model = new ExtensionsModel(target, transport);
	const first = model.load(), second = model.load();
	expect(requests[0]!.params).toEqual(target);
	expect(requests[0]!.signal.aborted).toBe(true);
	requests[1]!.resolve(empty("chat")); await second;
	requests[0]!.reject(new Error("credential must not appear")); await first;
	expect(model.state.getState().inventory?.context.sessionId).toBe("chat");
	const wrong = model.load(); requests[2]!.resolve(empty("foreign")); await wrong;
	expect(model.state.getState().inventory).toBeNull();
	expect(model.state.getState().error).toContain("unavailable");
	const disposed = model.load(); model.dispose(); requests[3]!.resolve(empty("chat")); await disposed;
	expect(model.state.getState().inventory).toBeNull();
});

test("Extensions screen renders native metadata, scoped unavailable and failed states without controls or unsafe HTML", async () => {
	const path = "src/settings/sections/extensions-inventory.svelte";
	const inventory = empty("chat");
	inventory.extensions.push({ path: "<script>alert(1)</script>", resolvedPath: "/project/plugin.js",
		source: {scope: "project", origin: "top-level", source: "local"}, name: null, version: null,
		tools: ["unknown_tool"], commands: ["unknown-command"], interfaceSupport: "unknown" });
	inventory.errors.push({path: "/project/broken.js", code: "load-failed"});
	const html = await renderSvelte(path, { inventory, label: "Project: A · Session: chat" });
	expect(html).toContain("Project: A · Session: chat");
	expect(html).toContain("unknown_tool"); expect(html).toContain("unknown-command");
	expect(html).toContain("Version: unknown"); expect(html).toContain("Load failed:");
	expect(html).not.toContain("<script>alert(1)</script>");
	expect(html).not.toContain('type="checkbox"'); expect(html).not.toContain('role="switch"');
	expect(html).not.toContain("signet");
	const blank = await renderSvelte(path, {inventory: empty(), label: "Project: A"});
	expect(blank).toContain("No configured packages reported");
	expect(blank).toContain("Loaded inventory unavailable in this reader scope");
	const failed = await renderSvelte(path, {error: "Inventory unavailable", label: "Project: B"});
	expect(failed).toContain('role="alert"'); expect(failed).not.toContain("No configured packages");
	const loading = await renderSvelte(path, {loading: true, label: "Project: B"});
	expect(loading).toContain("Loading native inventory");
	expect(settingsTabs().map((tab) => tab.label)).toContain("Extensions");
	expect(settingsTabs().map((tab) => tab.label)).toContain("Schedules");
});

test("native configuration requires confirmation, saves scoped revisions and distinguishes saved configuration from deferred loading", async () => {
	const inventory = empty("chat");
	inventory.configurationRevisions = { user: "a".repeat(64), project: "b".repeat(64) };
	inventory.resources.push({ path: "/project/.pi/extensions/unknown.js", source: "auto", scope: "project", origin: "top-level", enabled: true, state: "loaded", resourceKey: "c".repeat(64), configurationSupported: true });
	const calls: {method: string; params: unknown}[] = [];
	let failSave = false;
	const transport = { request: async (method: string, params: unknown) => {
		calls.push({ method, params });
		if (method === "pi.nativeExtensions") return structuredClone(inventory);
		if (method === "pi.nativeExtensionConfigure") {
			if (failSave) throw new Error("private diagnostic must not render");
			inventory.resources[0]!.enabled = false;
			return { saved: true, loaded: false, reload: "deferred", reason: "sdk-loader-install-policy" };
		}
		if (method === "pi.nativeExtensionReload") return { loaded: false, reload: "deferred", reason: "session-busy" };
		throw new Error(`Unexpected method: ${method}`);
	} } as unknown as Pick<WsTransport, "request">;
	const model = new ExtensionsModel({projectId: "p", root: "/project", sessionId: "chat"}, transport);
	await model.load();
	const resource = inventory.resources[0]!;
	await model.configure(resource, () => false);
	expect(calls).toHaveLength(1);
	await model.configure(resource, (message) => { expect(message).toContain("project settings"); expect(message).toContain("host user's authority"); return true; });
	expect(calls[1]).toMatchObject({ method: "pi.nativeExtensionConfigure", params: { scope: "project", resourceKey: "c".repeat(64), expectedRevision: "b".repeat(64), enabled: false, confirmed: true } });
	expect(model.state.getState().notice).toContain("Configuration saved. Loaded extensions are unchanged");
	expect(model.state.getState().inventory?.resources[0]).toMatchObject({ enabled: false, state: "loaded" });
	await model.requestReload();
	expect(model.state.getState().notice).toContain("Nothing was stopped");
	failSave = true;
	await model.configure(resource, () => true);
	expect(model.state.getState().error).toContain("Save outcome not confirmed");
	expect(model.state.getState().error).not.toContain("private diagnostic");
});

test("configuration buttons name scope and show real operation progress rather than pretending a deferred reload ran", async () => {
	const inventory = empty("chat");
	inventory.configurationRevisions = { user: "a".repeat(64), project: "b".repeat(64) };
	inventory.resources.push({ path: "/project/plugin.js", source: "local", scope: "project", origin: "top-level", enabled: false, state: "not-loaded", resourceKey: "c".repeat(64), configurationSupported: true });
	const html = await renderSvelte("src/settings/sections/extensions-inventory.svelte", { inventory, label: "Project: A", busy: "saving", canRequestReload: true });
	expect(html).toContain("Enable on next load (project)");
	expect(html).toContain("Saving native configuration");
	expect(html).toContain("disabled");
	const checking = await renderSvelte("src/settings/sections/extensions-inventory.svelte", {inventory, label: "Project: A", busy: "checking-reload"});
	expect(checking).toContain("Checking reload safety");
	expect(checking).not.toContain("Reloading extensions");
});
