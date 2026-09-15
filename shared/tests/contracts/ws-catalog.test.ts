import { expect, test } from "bun:test";
import { resolve } from "node:path";
import {
	loadProtocolCatalog,
	type OwnershipDefinition,
	ownershipProblems,
	SCHEMA_RELATIVE_PATH,
} from "../../../web/scripts/generate-contracts";
import {
	CONTROLLER_METHOD_FC,
	CONTROLLER_METHOD_OWNERS,
	CONTROLLER_METHOD_PROFILES,
	CONTROLLER_METHOD_REASONS,
	CONTROLLER_METHOD_ROUTES,
	CONTROLLER_METHOD_STATUS,
	CONTROLLER_METHODS,
	HOST_OPERATION_STATUS,
	NATIVE_CONTROLLER_ROUTES,
	NATIVE_WORKSPACE_ROUTES,
} from "../../src/generated/protocol-catalog";
import type { WsMethod, WsMethodName } from "../../src/ws-protocol";
import { WS_METHODS } from "../../src/ws-protocol";

// API-01 exhaustive browser/controller catalog with FC mapping.
// The ownership/status parity matrix is derived from the shared schema; this
// test never hand-maintains a second route table. Every row scopes its
// native/bridge route to one exact RPC or controller subsystem, so no row can
// use a broad "Administration" or provider-wide gate (F20). The 8 schedule.*
// methods plus model.thinkingLevels and mcpAdapter.status are first-class rows
// here and in WS_METHODS (F41).

type CatalogMethod = (typeof CONTROLLER_METHODS)[number];

// Compile-time proof that the generated catalog, WS_METHODS values, and
// WsMethodMap keys describe the same browser surface. Any drift fails
// typecheck before tests run.
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

test("exhaustive catalog covers every WS_METHODS value", () => {
	const values = Object.values(WS_METHODS);
	const catalogMethods = [...CONTROLLER_METHODS];
	expect(values.length).toBe(101);
	expect(catalogMethods.length).toBe(101);
	expect(new Set(values).size).toBe(values.length);
	expect(new Set(catalogMethods).size).toBe(catalogMethods.length);
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

// The generated route table is the only route vocabulary. A browser method may
// name a catalogued host operation or a declared controller/workspace
// subsystem; anything else is an unknown native route and must fail closed.
function routeKnown(route: string): boolean {
	if (HOST_OPERATION_STATUS[route as keyof typeof HOST_OPERATION_STATUS] !== undefined) return true;
	const [namespace, id] = route.split(":", 2);
	if (namespace === "controller")
		return (NATIVE_CONTROLLER_ROUTES as readonly string[]).includes(id ?? "");
	if (namespace === "workspace")
		return (NATIVE_WORKSPACE_ROUTES as readonly string[]).includes(id ?? "");
	return false;
}

function ownerForRoute(route: string): string {
	if (route.startsWith("controller:")) return "controller";
	if (route.startsWith("workspace:")) return "workspace";
	return "pi";
}

test("every browser method names a known route with a consistent owner and status", () => {
	for (const method of CONTROLLER_METHODS) {
		const route = CONTROLLER_METHOD_ROUTES[method];
		const owner = CONTROLLER_METHOD_OWNERS[method];
		const status = CONTROLLER_METHOD_STATUS[method];
		expect(routeKnown(route)).toBe(true);
		expect(owner).toBe(ownerForRoute(route));
		expect(["available", "unavailable", "absent"]).toContain(status);
		const hostStatus = HOST_OPERATION_STATUS[route as keyof typeof HOST_OPERATION_STATUS];
		if (hostStatus !== undefined) {
			// A browser method cannot upgrade a host route the catalog does not
			// implement, and it cannot declare a status the host does not have.
			expect(status).toBe(hostStatus);
			if (hostStatus !== "available") {
				expect(typeof CONTROLLER_METHOD_REASONS[method]).toBe("string");
				expect((CONTROLLER_METHOD_REASONS[method] ?? "").length).toBeGreaterThan(0);
			} else {
				expect(CONTROLLER_METHOD_REASONS[method]).toBeUndefined();
			}
		} else if (status === "available") {
			expect(CONTROLLER_METHOD_REASONS[method]).toBeUndefined();
		} else {
			expect((CONTROLLER_METHOD_REASONS[method] ?? "").length).toBeGreaterThan(0);
		}
	}
	// The specific legacy lies this matrix replaces: archive and adapter routes
	// were advertised as Pi-owned even though the catalog marks them absent.
	expect(CONTROLLER_METHOD_OWNERS["session.archive"]).toBe("controller");
	expect(CONTROLLER_METHOD_OWNERS["session.unarchive"]).toBe("controller");
	expect(CONTROLLER_METHOD_OWNERS["mcpAdapter.status"]).toBe("controller");
	expect(CONTROLLER_METHOD_OWNERS["pi.capabilities"]).toBe("controller");
	expect(CONTROLLER_METHOD_STATUS["session.steer"]).toBe("unavailable");
	expect(CONTROLLER_METHOD_STATUS["session.toolList"]).toBe("unavailable");
	expect(CONTROLLER_METHOD_STATUS["session.archive"]).toBe("unavailable");
});

test("catalog native routes are exact, not broad Administration assumptions", () => {
	const profiles = new Set(["V", "A", "M", "W", "controller-local"]);
	for (const method of CONTROLLER_METHODS) {
		expect(method.length).toBeGreaterThan(0);
		expect(/^FC(0[1-9]|[12][0-9]|3[0-3])$/.test(CONTROLLER_METHOD_FC[method])).toBe(true);
		expect(profiles.has(CONTROLLER_METHOD_PROFILES[method])).toBe(true);
		const route = CONTROLLER_METHOD_ROUTES[method];
		expect(route.length).toBeGreaterThan(0);
		expect(route).not.toContain("Administration");
		expect(route).not.toContain("administration");
		expect(route).not.toContain("PiAdmin");
		expect(route).not.toContain("*");
		expect(route).not.toContain("broad");
		const exact =
			route.startsWith("controller:") ||
			route.startsWith("workspace:") ||
			HOST_OPERATION_STATUS[route as keyof typeof HOST_OPERATION_STATUS] !== undefined;
		expect(exact).toBe(true);
	}
});

test("ownership validation fails on unknown routes and over-advertised statuses", () => {
	const host = [
		{ name: "session.list", status: "available" as const },
		{
			name: "session.delete",
			status: "absent" as const,
			reason: "Session deletion is controller-owned state.",
		},
	];
	const nativeRoutes = { controller: ["archive"], workspace: ["git"] };
	const row = (overrides: Partial<OwnershipDefinition>): OwnershipDefinition => ({
		method: "session.list",
		fc: "FC08",
		owner: "pi",
		profile: "V",
		route: "session.list",
		...overrides,
	});

	expect(ownershipProblems([row({})], host, nativeRoutes)).toEqual([]);
	expect(ownershipProblems([row({ route: "session.archive" })], host, nativeRoutes)).toContain(
		"session.list: unknown native route session.archive",
	);
	expect(ownershipProblems([row({ route: "controller:missing" })], host, nativeRoutes)).toContain(
		"session.list: unknown controller route controller:missing",
	);
	expect(
		ownershipProblems([row({ route: "controller:archive", owner: "pi" })], host, nativeRoutes),
	).toContain("session.list: controller route controller:archive requires the controller owner");
	expect(
		ownershipProblems(
			[row({ route: "session.delete", owner: "pi", status: "available" })],
			host,
			nativeRoutes,
		),
	).toContain("session.list: advertises session.delete as available but the host marks it absent");
	expect(
		ownershipProblems(
			[
				row({
					method: "session.archive",
					owner: "controller",
					route: "controller:archive",
					status: "unavailable",
				}),
			],
			host,
			nativeRoutes,
		),
	).toContain("session.archive: a non-available route requires a reason");
	// The live schema must always be internally consistent.
	const schema = loadProtocolCatalog(resolve(import.meta.dir, "..", "..", SCHEMA_RELATIVE_PATH));
	expect(ownershipProblems(schema.ownership, schema.operations.host, schema.nativeRoutes)).toEqual(
		[],
	);
});

function extractHandlerMethods(source: string): string[] {
	const methods: string[] = [];
	const casePattern = /case\s+((?:"[^"]+"\s*,?\s*)+)/g;
	let found: RegExpExecArray | null = casePattern.exec(source);
	while (found !== null) {
		const group = found[1] ?? "";
		for (const quoted of group.matchAll(/"([^"]+)"/g)) {
			const value = quoted[1] ?? "";
			if (/^[A-Za-z]+\.[A-Za-z]+$/.test(value)) methods.push(value);
		}
		found = casePattern.exec(source);
	}
	return methods;
}

test("generated binding check matches Go handler cases both directions", async () => {
	const base = import.meta.dir;
	const handlerSource = await Bun.file(
		`${base}/../../../web/internal/controller/handler.go`,
	).text();
	const extensionsSource = await Bun.file(
		`${base}/../../../web/internal/controller/pi_extensions.go`,
	).text();
	const handlerMethods = new Set([
		...extractHandlerMethods(handlerSource),
		...extractHandlerMethods(extensionsSource),
	]);
	const wsValues = new Set<string>(Object.values(WS_METHODS));
	const catalogMethods = new Set<string>(CONTROLLER_METHODS);
	expect(handlerMethods.size).toBe(101);
	expect(handlerMethods).toEqual(wsValues);
	expect(handlerMethods).toEqual(catalogMethods);
});
