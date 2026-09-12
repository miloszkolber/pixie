import { expect, test } from "bun:test";
import { WS_METHODS } from "../../contracts/src/ws-protocol";
import type { WsMethod, WsMethodName } from "../../contracts/src/ws-protocol";

// API-01 exhaustive browser/controller catalog with FC mapping.
// Every row scopes its native/bridge route to one exact RPC or controller
// subsystem. No row uses a broad "Administration" or provider-wide gate (F20).
// The 8 schedule.* methods plus model.thinkingLevels and mcpAdapter.status are
// first-class rows here and in WS_METHODS (F41).
const CATALOG = [
	{ method: "project.open", fc: "FC08", owner: "controller", profile: "controller-local", nativeRoute: "controller:projects" },
	{ method: "project.update", fc: "FC08", owner: "controller", profile: "controller-local", nativeRoute: "controller:projects" },
	{ method: "project.list", fc: "FC08", owner: "controller", profile: "controller-local", nativeRoute: "controller:projects" },
	{ method: "project.close", fc: "FC08", owner: "controller", profile: "controller-local", nativeRoute: "controller:projects" },
	{ method: "project.watchReady", fc: "FC08", owner: "controller", profile: "controller-local", nativeRoute: "controller:project-watches" },
	{ method: "git.listRepositories", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:git" },
	{ method: "fs.readDir", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:files" },
	{ method: "fs.readFile", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:files" },
	{ method: "git.status", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:git" },
	{ method: "git.diffFile", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:git" },
	{ method: "git.listBranches", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:git" },
	{ method: "git.listCommits", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:git" },
	{ method: "directory.list", fc: "FC30", owner: "workspace", profile: "controller-local", nativeRoute: "workspace:files" },
	{ method: "skill.list", fc: "FC12", owner: "pi", profile: "A", nativeRoute: "pi.slash-commands.list" },
	{ method: "session.create", fc: "FC02", owner: "pi", profile: "V", nativeRoute: "piwire:NewSession" },
	{ method: "session.fork", fc: "FC07", owner: "pi", profile: "V", nativeRoute: "piwire:ForkSession" },
	{ method: "session.prompt", fc: "FC03", owner: "pi", profile: "V", nativeRoute: "piwire:Prompt" },
	{ method: "session.steer", fc: "FC05", owner: "pi", profile: "V", nativeRoute: "pi.session.steer" },
	{ method: "session.queueAdd", fc: "FC05", owner: "controller", profile: "V", nativeRoute: "controller:queues" },
	{ method: "session.queueEdit", fc: "FC05", owner: "controller", profile: "V", nativeRoute: "controller:queues" },
	{ method: "session.queueRemove", fc: "FC05", owner: "controller", profile: "V", nativeRoute: "controller:queues" },
	{ method: "session.queueRetry", fc: "FC05", owner: "controller", profile: "V", nativeRoute: "controller:queues" },
	{ method: "session.abort", fc: "FC05", owner: "pi", profile: "V", nativeRoute: "piwire:Cancel" },
	{ method: "session.delete", fc: "FC09", owner: "controller", profile: "V", nativeRoute: "controller:deletions" },
	{ method: "session.deletionRecovery", fc: "FC09", owner: "controller", profile: "controller-local", nativeRoute: "controller:deletions" },
	{ method: "session.confirmExternalDeletion", fc: "FC09", owner: "controller", profile: "controller-local", nativeRoute: "controller:deletions" },
	{ method: "session.rename", fc: "FC07", owner: "pi", profile: "V", nativeRoute: "pi.session.rename" },
	{ method: "session.archive", fc: "FC08", owner: "pi", profile: "V", nativeRoute: "pi.session.archive" },
	{ method: "session.unarchive", fc: "FC08", owner: "pi", profile: "V", nativeRoute: "pi.session.unarchive" },
	{ method: "session.setModel", fc: "FC10", owner: "pi", profile: "V", nativeRoute: "piwire:SetSessionConfigOption" },
	{ method: "session.setThinkingLevel", fc: "FC10", owner: "pi", profile: "V", nativeRoute: "piwire:SetSessionConfigOption" },
	{ method: "session.setConfigOption", fc: "FC10", owner: "pi", profile: "V", nativeRoute: "piwire:SetSessionConfigOption" },
	{ method: "session.getStats", fc: "FC11", owner: "controller", profile: "V", nativeRoute: "controller:session-stats" },
	{ method: "session.getCommands", fc: "FC12", owner: "pi", profile: "A", nativeRoute: "pi.slash-commands.list" },
	{ method: "session.getAgentMentions", fc: "FC23", owner: "pi", profile: "A", nativeRoute: "pi.agent-mentions.list" },
	{ method: "session.goalGet", fc: "FC25", owner: "controller", profile: "controller-local", nativeRoute: "controller:objectives" },
	{ method: "session.goalSet", fc: "FC25", owner: "controller", profile: "controller-local", nativeRoute: "controller:objectives" },
	{ method: "session.goalClear", fc: "FC25", owner: "controller", profile: "controller-local", nativeRoute: "controller:objectives" },
	{ method: "session.uiReply", fc: "FC13", owner: "pi", profile: "V", nativeRoute: "session.uiResponse" },
	{ method: "session.uiCancel", fc: "FC13", owner: "pi", profile: "V", nativeRoute: "session.uiCancel" },
	{ method: "session.list", fc: "FC08", owner: "pi", profile: "V", nativeRoute: "piwire:ListSessions" },
	{ method: "session.getMessages", fc: "FC06", owner: "pi", profile: "V", nativeRoute: "session.history" },
	{ method: "session.release", fc: "FC02", owner: "controller", profile: "controller-local", nativeRoute: "controller:leases" },
	{ method: "session.setLeases", fc: "FC02", owner: "controller", profile: "controller-local", nativeRoute: "controller:leases" },
	{ method: "model.list", fc: "FC17", owner: "pi", profile: "A", nativeRoute: "pi.providers.list" },
	{ method: "model.clampThinking", fc: "FC10", owner: "controller", profile: "V", nativeRoute: "controller:session-config" },
	{ method: "model.thinkingLevels", fc: "FC10", owner: "controller", profile: "V", nativeRoute: "controller:session-config" },
	{ method: "model.refresh", fc: "FC17", owner: "pi", profile: "A", nativeRoute: "pi.providers.inventory.refresh" },
	{ method: "model.setVisibility", fc: "FC17", owner: "controller", profile: "controller-local", nativeRoute: "controller:settings" },
	{ method: "model.setAllVisibility", fc: "FC17", owner: "controller", profile: "controller-local", nativeRoute: "controller:settings" },
	{ method: "pi.preferencesRead", fc: "FC19", owner: "pi", profile: "A", nativeRoute: "pi.preferences.read" },
	{ method: "pi.preferencesSave", fc: "FC19", owner: "pi", profile: "A", nativeRoute: "pi.preferences.save" },
	{ method: "pi.preferencesReset", fc: "FC19", owner: "pi", profile: "A", nativeRoute: "pi.preferences.reset" },
	{ method: "pi.defaultsRead", fc: "FC19", owner: "pi", profile: "A", nativeRoute: "pi.defaults.read" },
	{ method: "pi.defaultsSave", fc: "FC19", owner: "pi", profile: "A", nativeRoute: "pi.defaults.save" },
	{ method: "pi.defaultsClear", fc: "FC19", owner: "pi", profile: "A", nativeRoute: "pi.defaults.clear" },
	{ method: "pi.capabilities", fc: "FC01", owner: "pi", profile: "A", nativeRoute: "runtime.capabilities" },
	{ method: "pi.agentList", fc: "FC23", owner: "pi", profile: "A", nativeRoute: "pi.sources.list" },
	{ method: "pi.agentCreate", fc: "FC23", owner: "pi", profile: "A", nativeRoute: "pi.sources.create" },
	{ method: "pi.agentUpdate", fc: "FC23", owner: "pi", profile: "A", nativeRoute: "pi.sources.update" },
	{ method: "pi.agentDelete", fc: "FC23", owner: "pi", profile: "A", nativeRoute: "pi.sources.delete" },
	{ method: "provider.status", fc: "FC17", owner: "pi", profile: "A", nativeRoute: "pi.providers.list" },
	{ method: "provider.readiness", fc: "FC17", owner: "pi", profile: "A", nativeRoute: "pi.providers.readiness.check" },
	{ method: "provider.loginStart", fc: "FC18", owner: "pi", profile: "A", nativeRoute: "provider.loginStart" },
	{ method: "provider.loginReply", fc: "FC18", owner: "pi", profile: "A", nativeRoute: "provider.loginReply" },
	{ method: "provider.loginCancel", fc: "FC18", owner: "pi", profile: "A", nativeRoute: "provider.loginCancel" },
	{ method: "provider.logout", fc: "FC18", owner: "pi", profile: "A", nativeRoute: "pi.providers.config.delete" },
	{ method: "settings.update", fc: "FC17", owner: "controller", profile: "controller-local", nativeRoute: "controller:settings" },
	{ method: "history.search", fc: "FC06", owner: "controller", profile: "controller-local", nativeRoute: "controller:history-index" },
	{ method: "schedule.list", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "schedule.preview", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "schedule.health", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "schedule.create", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "schedule.update", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "schedule.delete", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "schedule.runNow", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "schedule.stop", fc: "FC29", owner: "controller", profile: "controller-local", nativeRoute: "controller:scheduler" },
	{ method: "pi.status", fc: "FC01", owner: "controller", profile: "A", nativeRoute: "controller:pi-status" },
	{ method: "runtime.status", fc: "FC01", owner: "controller", profile: "controller-local", nativeRoute: "controller:runtime-status" },
	{ method: "mcpRegistry.catalog", fc: "FC26", owner: "controller", profile: "controller-local", nativeRoute: "controller:mcp-registry" },
	{ method: "mcpRegistry.moduleSetEnabled", fc: "FC26", owner: "controller", profile: "controller-local", nativeRoute: "controller:mcp-registry" },
	{ method: "mcpRegistry.moduleRestart", fc: "FC26", owner: "controller", profile: "controller-local", nativeRoute: "controller:mcp-registry" },
	{ method: "mcpAdapter.status", fc: "FC26", owner: "pi", profile: "A", nativeRoute: "adapter.status" },
	{ method: "browser.panelOpen", fc: "FC31", owner: "controller", profile: "W", nativeRoute: "controller:browser-panels" },
	{ method: "browser.panelCommand", fc: "FC31", owner: "controller", profile: "W", nativeRoute: "controller:browser-panels" },
	{ method: "browser.panelClose", fc: "FC31", owner: "controller", profile: "W", nativeRoute: "controller:browser-panels" },
	{ method: "pi.extensionList", fc: "FC20", owner: "pi", profile: "A", nativeRoute: "pi.config.extensions.list" },
	{ method: "pi.nativeExtensions", fc: "FC20", owner: "pi", profile: "A", nativeRoute: "pi.extensions.list" },
	{ method: "pi.nativeExtensionConfigure", fc: "FC21", owner: "pi", profile: "A", nativeRoute: "pi.extensions.configure" },
	{ method: "pi.nativeExtensionReload", fc: "FC21", owner: "controller", profile: "A", nativeRoute: "controller:deferred-reload" },
	{ method: "pi.reload", fc: "FC22", owner: "pi", profile: "A", nativeRoute: "runtime.restart" },
	{ method: "pi.extensionAdd", fc: "FC21", owner: "pi", profile: "A", nativeRoute: "pi.config.extensions.add" },
	{ method: "pi.extensionSetEnabled", fc: "FC21", owner: "pi", profile: "A", nativeRoute: "pi.config.extensions.set-enabled" },
	{ method: "pi.extensionRemove", fc: "FC21", owner: "pi", profile: "A", nativeRoute: "pi.config.extensions.remove" },
	{ method: "session.extensionList", fc: "FC20", owner: "pi", profile: "A", nativeRoute: "pi.session.extensions.list" },
	{ method: "session.extensionAdd", fc: "FC21", owner: "pi", profile: "A", nativeRoute: "pi.session.extensions.add" },
	{ method: "session.extensionRemove", fc: "FC21", owner: "pi", profile: "A", nativeRoute: "pi.session.extensions.remove" },
	{ method: "session.toolList", fc: "FC26", owner: "pi", profile: "A", nativeRoute: "pi.tools.list" },
] as const;

type CatalogMethod = (typeof CATALOG)[number]["method"];

// Compile-time proof that the catalog, WS_METHODS values, and WsMethodMap keys
// describe the same browser surface. Any drift fails typecheck before tests run.
type CatalogCoversMap = Exclude<WsMethodName, CatalogMethod>;
const assertCatalogCoversMap: CatalogCoversMap extends never ? true : never = true;
type MapCoversCatalog = Exclude<CatalogMethod, WsMethodName>;
const assertMapCoversCatalog: MapCoversCatalog extends never ? true : never = true;
type WsValuesCoverMap = Exclude<WsMethodName, WsMethod>;
const assertWsValuesCoverMap: WsValuesCoverMap extends never ? true : never = true;
type MapCoversWsValues = Exclude<WsMethod, WsMethodName>;
const assertMapCoversWsValues: MapCoversWsValues extends never ? true : never = true;

void assertCatalogCoversMap;
void assertMapCoversCatalog;
void assertWsValuesCoverMap;
void assertMapCoversWsValues;

function extractHandlerMethods(source: string): string[] {
	const methods: string[] = [];
	const casePattern = /case\s+((?:"[^"]+"\s*,?\s*)+)/g;
	let found: RegExpExecArray | null;
	while ((found = casePattern.exec(source)) !== null) {
		const group = found[1] ?? "";
		for (const quoted of group.matchAll(/"([^"]+)"/g)) {
			const value = quoted[1] ?? "";
			if (/^[A-Za-z]+\.[A-Za-z]+$/.test(value)) methods.push(value);
		}
	}
	return methods;
}

test("exhaustive catalog covers every WS_METHODS value", () => {
	const values = Object.values(WS_METHODS);
	const catalogMethods = CATALOG.map((row) => row.method);
	expect(values.length).toBe(98);
	expect(CATALOG.length).toBe(98);
	expect(new Set(values).size).toBe(values.length);
	expect(new Set(catalogMethods).size).toBe(CATALOG.length);
	expect(new Set(catalogMethods)).toEqual(new Set(values));
	for (const required of [
		"schedule.list",
		"schedule.preview",
		"schedule.health",
		"schedule.create",
		"schedule.update",
		"schedule.delete",
		"schedule.runNow",
		"schedule.stop",
		"model.thinkingLevels",
		"mcpAdapter.status",
	] as const) {
		expect(values).toContain(required);
		expect(catalogMethods).toContain(required);
	}
});

test("catalog native routes are exact, not broad Administration assumptions", () => {
	const owners = new Set(["controller", "workspace", "pi"]);
	const profiles = new Set(["V", "A", "M", "W", "controller-local"]);
	for (const row of CATALOG) {
		expect(row.method.length).toBeGreaterThan(0);
		expect(/^FC(0[1-9]|[12][0-9]|3[0-3])$/.test(row.fc)).toBe(true);
		expect(owners.has(row.owner)).toBe(true);
		expect(profiles.has(row.profile)).toBe(true);
		expect(row.nativeRoute.length).toBeGreaterThan(0);
		expect(row.nativeRoute).not.toContain("Administration");
		expect(row.nativeRoute).not.toContain("administration");
		expect(row.nativeRoute).not.toContain("PiAdmin");
		expect(row.nativeRoute).not.toContain("*");
		expect(row.nativeRoute).not.toContain("broad");
		const exact =
			row.nativeRoute.startsWith("controller:") ||
			row.nativeRoute.startsWith("workspace:") ||
			row.nativeRoute.startsWith("piwire:") ||
			row.nativeRoute.includes(".");
		expect(exact).toBe(true);
	}
});

test("generated binding check matches Go handler cases both directions", async () => {
	const base = import.meta.dir;
	const handlerSource = await Bun.file(`${base}/../../internal/controller/handler.go`).text();
	const extensionsSource = await Bun.file(
		`${base}/../../internal/controller/pi_extensions.go`,
	).text();
	const handlerMethods = new Set([
		...extractHandlerMethods(handlerSource),
		...extractHandlerMethods(extensionsSource),
	]);
	const wsValues = new Set<string>(Object.values(WS_METHODS));
	const catalogMethods = new Set<string>(CATALOG.map((row) => row.method));
	expect(handlerMethods.size).toBe(98);
	expect(handlerMethods).toEqual(wsValues);
	expect(handlerMethods).toEqual(catalogMethods);
});
