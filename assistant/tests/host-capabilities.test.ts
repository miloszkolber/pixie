import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import {
	CONTROLLER_METHOD_OWNERS,
	CONTROLLER_METHOD_ROUTES,
	CONTROLLER_METHOD_STATUS,
	CONTROLLER_METHODS,
	HOST_AVAILABLE_OPERATIONS,
	HOST_OPERATION_STATUS,
	HOST_OPERATIONS,
	NATIVE_CONTROLLER_ROUTES,
	NATIVE_WORKSPACE_ROUTES,
} from "../../shared/src/generated/protocol-catalog.ts";
import { FakeSession, rawFrames, rawHost, registerHostCleanup } from "./harness.ts";

registerHostCleanup();

describe("Bun host operationSet truthfulness", () => {
	test("advertises every catalog operation with truthful enablement", async () => {
		const raw = rawHost(new FakeSession("truth"), { protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		const hello = rawFrames(raw.socket).at(-1)?.result;
		expect(Object.keys(hello?.operationSet ?? {}).sort()).toEqual([...HOST_OPERATIONS].sort());
		// Every advertised bit is derived from the catalog status, except the
		// opt-in restart route which stays false unless explicitly enabled.
		for (const name of HOST_OPERATIONS) {
			const expected =
				name === "runtime.restart" ? false : HOST_OPERATION_STATUS[name] === "available";
			expect(hello?.operationSet?.[name]).toBe(expected);
		}
		for (const name of [
			"session.configure",
			"session.fork",
			"session.clone",
			"session.compact",
			"session.rename",
			"session.commands",
			"session.followUp",
			"session.clearQueue",
			"session.switch",
			"session.getMessages",
			"session.stats",
			"pi.sources.list",
			"pi.sources.create",
			"pi.sources.update",
			"pi.sources.delete",
			"pi.agent-mentions.list",
			"pi.mcp.servers.read",
			"pi.mcp.servers.probe",
			"pi.providers.list",
			"pi.providers.config.read",
			"pi.defaults.read",
			"pi.preferences.read",
			"pi.extensions.list",
			"pi.slash-commands.list",
			"provider.loginStart",
		]) {
			expect(hello?.operationSet?.[name]).toBe(true);
		}
		for (const name of [
			"session.delete",
			"session.archive",
			"session.steer",
			"session.prompt.image",
			"session.prompt.resource",
			"mcp.attach",
			"pi.session.info",
			"pi.session.steer",
			"pi.tools.list",
			"pi.tools.call",
			"runtime.capabilities",
			"pi.subagent.execute",
			"pi.todo.plan",
			"pi.llama",
			"pi.native-extensions",
		]) {
			expect(hello?.operationSet?.[name]).toBe(false);
		}
		// A catalogued-but-unavailable route fails closed with the catalog's
		// stated reason instead of inventing an implementation.
		await raw.send({ id: 5, method: "pi.session.steer", params: {} });
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("public run identifier");
		await raw.send({ id: 2, method: "session.delete", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 2,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
		await raw.send({ id: 3, method: "nope.unknown", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 3,
				error: expect.objectContaining({ code: -32601, reason: "method_not_found" }),
			}),
		);
		await raw.send({ id: 4, method: "pi.tools.list", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 4,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
	});

	test("supports image prompts and fails resource prompts closed", async () => {
		const raw = rawHost(new FakeSession("images"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({
			id: 3,
			method: "session.prompt",
			params: {
				sessionId: "images",
				content: [
					{ type: "text", text: "look" },
					{ type: "image", data: "aGVsbG8=", mimeType: "image/png" },
				],
			},
		});
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({ id: 3, result: expect.objectContaining({ stopReason: "stop" }) }),
		);
		await raw.send({
			id: 4,
			method: "session.prompt",
			params: {
				sessionId: "images",
				content: [{ type: "resource", resource: { text: "x" } }],
			},
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("resource");
	});

	test("session.list entries carry resident identity and cwd metadata", async () => {
		const raw = rawHost(new FakeSession("metadata"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const created = rawFrames(raw.socket).at(-1)?.result?.sessionId;
		expect(typeof created).toBe("string");
		await raw.send({ id: 3, method: "session.list", params: {} });
		const sessions = rawFrames(raw.socket).at(-1)?.result?.sessions as
			| { sessionId?: unknown; cwd?: unknown }[]
			| undefined;
		expect(Array.isArray(sessions)).toBe(true);
		const resident = sessions?.find((entry) => entry.sessionId === created);
		expect(resident).toBeDefined();
		// The contract requires an identity and a working directory for every
		// resident and cwd-scoped entry.
		expect(typeof resident?.sessionId).toBe("string");
		expect(typeof resident?.cwd).toBe("string");
		expect((resident?.cwd ?? "").length).toBeGreaterThan(0);
	});
});

describe("generated ownership/status parity", () => {
	test("every browser method maps to a known route with a matching owner", () => {
		const controllerRoutes = new Set<string>(NATIVE_CONTROLLER_ROUTES);
		const workspaceRoutes = new Set<string>(NATIVE_WORKSPACE_ROUTES);
		for (const method of CONTROLLER_METHODS) {
			const route = CONTROLLER_METHOD_ROUTES[method];
			const owner = CONTROLLER_METHOD_OWNERS[method];
			const hostStatus = HOST_OPERATION_STATUS[route as keyof typeof HOST_OPERATION_STATUS];
			if (hostStatus === undefined) {
				const [namespace, id] = route.split(":", 2);
				const known =
					(namespace === "controller" && controllerRoutes.has(id ?? "")) ||
					(namespace === "workspace" && workspaceRoutes.has(id ?? ""));
				expect(known).toBe(true);
				expect(owner).toBe(namespace);
				continue;
			}
			expect(owner).toBe("pi");
			// A browser method may not upgrade a host route the catalog rejects.
			if (hostStatus !== "available") expect(CONTROLLER_METHOD_STATUS[method]).toBe(hostStatus);
			else expect(CONTROLLER_METHOD_STATUS[method]).toBe("available");
		}
	});
});

describe("Bun host dispatch coverage", () => {
	// Routes served outside the host's dispatch switch. Keep this empty unless a
	// route is genuinely handled elsewhere; every available operation currently
	// has a `case` in assistant/src/host.ts.
	const handlerAllowlist: readonly string[] = [];
	// Available catalog operations deliberately exercised only through another
	// operation instead of a direct frame. Keep this empty unless a route is
	// genuinely dispatched indirectly.
	const dispatchCoverageAllowlist: readonly string[] = [];

	// A fail-closed probe proves only that the host refuses a route the catalog
	// marks unavailable, not that the host serves one. Such a frame must not
	// satisfy coverage for an operation advertised as available. The admin code
	// -32004 is the numeric form of capability_unavailable.
	const FAIL_CLOSED_MARKERS = ["capability_unavailable", "-32004"] as const;

	// A real dispatch is a frame a test actually sends. Matching the bare
	// operation name is not enough: the truthfulness lists above name every
	// operation without dispatching it. The detector therefore only counts a
	// `method: "<operation>"` frame property or the method argument of the
	// shared `request(...)` helpers; bare array elements in those lists never
	// match, so the capabilities test's own static lists cannot satisfy it.
	const dispatchPattern = /method\s*:\s*"([^"]+)"|request\s*\([^;]*?"([^"]+)"/gs;

	type DispatchSite = { readonly operation: string; readonly start: number; readonly end: number };

	function dispatchSites(source: string): DispatchSite[] {
		const sites: DispatchSite[] = [];
		for (const match of source.matchAll(dispatchPattern)) {
			const operation = match[1] ?? match[2];
			if (operation === undefined || match.index === undefined) continue;
			sites.push({ operation, start: match.index, end: match.index + match[0].length });
		}
		return sites;
	}

	function hostCaseOperations(hostSource: string): Set<string> {
		return new Set([...hostSource.matchAll(/case\s+"([^"]+)"/g)].map((match) => match[1]));
	}

	// A dispatch is positive unless the text up to the next dispatch asserts a
	// fail-closed outcome; that only documents refusal for the preceding frame.
	function positiveDispatchOperations(sources: readonly string[]): Set<string> {
		const covered = new Set<string>();
		for (const source of sources) {
			const sites = dispatchSites(source);
			sites.forEach((site, index) => {
				const next = sites[index + 1];
				const segment = source.slice(site.end, next ? next.start : source.length);
				if (!FAIL_CLOSED_MARKERS.some((marker) => segment.includes(marker))) {
					covered.add(site.operation);
				}
			});
		}
		return covered;
	}

	function uncoveredOperations(
		operations: readonly string[],
		sources: readonly string[],
		hostSource: string,
		allowlist: {
			readonly handlers?: readonly string[];
			readonly dispatches?: readonly string[];
		} = {},
	): string[] {
		const handlers = hostCaseOperations(hostSource);
		const dispatches = positiveDispatchOperations(sources);
		return operations.filter((operation) => {
			const handled = handlers.has(operation) || allowlist.handlers?.includes(operation);
			const dispatched = dispatches.has(operation) || allowlist.dispatches?.includes(operation);
			return !(handled && dispatched);
		});
	}

	function testSources(): string[] {
		const testDir = import.meta.dir;
		return readdirSync(testDir)
			.filter((entry) => entry.endsWith(".test.ts"))
			.map((entry) => readFileSync(join(testDir, entry), "utf8"));
	}

	function hostSource(): string {
		return readFileSync(join(import.meta.dir, "..", "src", "host.ts"), "utf8");
	}

	const syntheticHost = 'switch (method) { case "session.synthetic": { return; } }';

	test("requires a host handler and a positive dispatch for every available operation", () => {
		const uncovered = uncoveredOperations(HOST_AVAILABLE_OPERATIONS, testSources(), hostSource(), {
			handlers: handlerAllowlist,
			dispatches: dispatchCoverageAllowlist,
		});
		expect(uncovered).toEqual([]);
	});

	test("ignores a static list name and a dispatch the host cannot handle", () => {
		// Naming the operation in a static list is not a dispatch. This is the
		// false-negative the previous bare-text scan allowed.
		const staticListOnly = 'for (const name of ["session.synthetic"]) {}';
		expect(uncoveredOperations(["session.synthetic"], [staticListOnly], syntheticHost)).toEqual([
			"session.synthetic",
		]);
		// A real frame is not enough when the host has no case for the route.
		const dispatched = 'await raw.send({ id: 1, method: "session.synthetic", params: {} });';
		expect(uncoveredOperations(["session.synthetic"], [dispatched], "")).toEqual([
			"session.synthetic",
		]);
	});

	test("reports a host case reached only by a fail-closed probe as uncovered", () => {
		// The reproduced weakness: the frame exists, but the only assertion on it
		// expects capability_unavailable, so the host is not shown to serve the
		// route the catalog advertises.
		const failClosed = [
			'await raw.send({ id: 1, method: "session.synthetic", params: {} });',
			"expect(rawFrames(raw.socket).at(-1)).toEqual(",
			'\texpect.objectContaining({ reason: "capability_unavailable" }),',
			");",
		].join("\n");
		expect(uncoveredOperations(["session.synthetic"], [failClosed], syntheticHost)).toEqual([
			"session.synthetic",
		]);
		const positive = 'await raw.send({ id: 2, method: "session.synthetic", params: {} });';
		expect(
			uncoveredOperations(["session.synthetic"], [failClosed, positive], syntheticHost),
		).toEqual([]);
	});

	test("detects the request(...) helper form as a positive dispatch", () => {
		const viaHelper = 'const reply = await request(ws, 1, "session.synthetic", {});';
		expect(uncoveredOperations(["session.synthetic"], [viaHelper], syntheticHost)).toEqual([]);
	});
});
