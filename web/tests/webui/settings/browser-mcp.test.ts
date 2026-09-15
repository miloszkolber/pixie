import { expect, test } from "bun:test";
import type { BrowserMCPStatus } from "@pixie/shared";
import type { WsTransport } from "@/connection/transport";
import {
	BrowserMcpModel,
	browserMcpDraftFromStatus,
	browserMcpNameError,
	browserMcpUrlError,
} from "@/settings/sections/browser-mcp-settings";
import { settingsTabs } from "@/settings/settings-dialog";
import { renderSvelte } from "../chat/svelte-render";

const viewPath = "src/settings/sections/browser-mcp-view.svelte";

function status(overrides: Partial<BrowserMCPStatus> = {}): BrowserMCPStatus {
	return {
		name: "pixie-browser",
		enabled: true,
		registered: true,
		disabled: false,
		reachable: true,
		tools: ["Tool 1", "Tool 2"],
		...overrides,
	};
}

type RequestCall = { method: string; params: unknown; resolve?: (value: unknown) => void };

function mockTransport(handler: (method: string, params: unknown) => Promise<unknown>): {
	transport: Pick<WsTransport, "request">;
	calls: RequestCall[];
} {
	const calls: RequestCall[] = [];
	const transport = {
		request: (method: string, params: unknown) => {
			calls.push({ method, params });
			return handler(method, params);
		},
	} as unknown as Pick<WsTransport, "request">;
	return { transport, calls };
}

test("browser MCP draft validation mirrors the fail-closed controller rules", () => {
	expect(browserMcpNameError("")).not.toBeNull();
	expect(browserMcpNameError("pixie-browser")).toBeNull();
	expect(browserMcpNameError("browser_mcp.1")).toBeNull();
	expect(browserMcpNameError("browser mcp")).not.toBeNull();
	expect(browserMcpNameError("bröwser")).not.toBeNull();
	expect(browserMcpNameError("a".repeat(129))).not.toBeNull();
	expect(browserMcpNameError(`${"a".repeat(128)}`)).toBeNull();

	expect(browserMcpUrlError("")).not.toBeNull();
	expect(browserMcpUrlError("not a url")).not.toBeNull();
	expect(browserMcpUrlError("ftp://127.0.0.1:3000/mcp")).not.toBeNull();
	expect(browserMcpUrlError("http://user:secret@127.0.0.1:3000/mcp")).not.toBeNull();
	expect(browserMcpUrlError("http://127.0.0.1:3000/mcp")).toBeNull();
	expect(browserMcpUrlError("https://browser.example.com/mcp")).toBeNull();
});

test("browser MCP configure, remove and load use the exact methods and reject stale replies", async () => {
	const pending: Array<{ method: string; params: unknown; resolve: (v: unknown) => void }> = [];
	const transport = {
		request: (method: string, params: unknown) =>
			new Promise<unknown>((resolve) => pending.push({ method, params, resolve })),
	} as unknown as Pick<WsTransport, "request">;
	const model = new BrowserMcpModel(transport);

	const save = model.save({
		name: "pixie-browser",
		url: "http://127.0.0.1:3000/mcp",
		enabled: true,
	});
	expect(pending[0]!.method).toBe("browserMcp.configure");
	expect(pending[0]!.params).toEqual({
		name: "pixie-browser",
		url: "http://127.0.0.1:3000/mcp",
		enabled: true,
	});
	pending[0]!.resolve(status());
	await save;
	expect(model.state.getState().status?.registered).toBe(true);
	expect(model.state.getState().notice).toContain("saved in Pi");

	const remove = model.remove();
	expect(pending[1]!.method).toBe("browserMcp.remove");
	expect(pending[1]!.params).toEqual({});
	pending[1]!.resolve(status({ enabled: false, registered: false, reachable: true, tools: [] }));
	await remove;
	expect(model.state.getState().status?.registered).toBe(false);

	const first = model.load();
	const second = model.load();
	expect(pending[2]!.method).toBe("browserMcp.status");
	pending[3]!.resolve(status({ reachable: false, tools: [] }));
	await second;
	pending[2]!.resolve(status({ reachable: true, tools: ["Tool 1"] }));
	await first;
	expect(model.state.getState().status?.reachable).toBe(false);
	expect(model.state.getState().status?.tools).toEqual([]);
});

test("browser MCP fails closed on an invalid draft or an unavailable assistant", async () => {
	const { transport, calls } = mockTransport(async () => {
		throw new Error("Pi administration is not configured");
	});
	const model = new BrowserMcpModel(transport);

	expect(
		await model.save({ name: "bröwser", url: "http://127.0.0.1:1/mcp", enabled: true }),
	).toBeNull();
	expect(calls).toHaveLength(0);
	expect(model.state.getState().error).toContain("ASCII letters, numbers");

	await model.save({ name: "pixie-browser", url: "javascript:alert(1)", enabled: true });
	expect(calls).toHaveLength(0);
	expect(model.state.getState().error).toContain("http or https");

	await model.save({ name: "pixie-browser", url: "http://127.0.0.1:3000/mcp", enabled: true });
	expect(calls).toHaveLength(1);
	expect(model.state.getState().status).toBeNull();
	expect(model.state.getState().error).toContain("not configured");

	await model.load();
	expect(model.state.getState().status).toBeNull();
	expect(model.state.getState().error).toContain("not configured");
});

test("browser MCP status omits a configured endpoint URL and asks for it before an update", async () => {
	const configured = status({ name: "configured-browser" });
	const { transport } = mockTransport(async () => configured);
	const model = new BrowserMcpModel(transport);

	expect(await model.load()).toBe(configured);
	expect(browserMcpDraftFromStatus(configured)).toEqual({
		name: "configured-browser",
		url: "",
		enabled: true,
	});
	expect(model.state.getState().notice).toContain("not shown for security");
});

test("browser MCP requires a disabled transition before renaming an enabled entry", async () => {
	const { transport, calls } = mockTransport(async (_method, params) => {
		const request = params as { name: string; enabled: boolean };
		return status({ name: request.name, enabled: request.enabled, registered: request.enabled });
	});
	const model = new BrowserMcpModel(transport);
	model.state.setState({ status: status() });

	expect(
		await model.save({
			name: "renamed-browser",
			url: "http://127.0.0.1:3000/mcp",
			enabled: true,
		}),
	).toBeNull();
	expect(calls).toHaveLength(0);
	expect(model.state.getState().error).toContain("remove the enabled entry first");

	model.state.setState({ status: status({ enabled: false, registered: false }) });
	expect(
		await model.save({
			name: "renamed-browser",
			url: "http://127.0.0.1:3000/mcp",
			enabled: false,
		}),
	).toMatchObject({ name: "renamed-browser", enabled: false });
	expect(calls).toHaveLength(1);
	expect(calls[0]?.params).toEqual({
		name: "renamed-browser",
		url: "http://127.0.0.1:3000/mcp",
		enabled: false,
	});
});

test("browser MCP view renders registration, probe and tool detail without claiming unregistered success", async () => {
	const draft = { name: "pixie-browser", url: "http://127.0.0.1:3000/mcp", enabled: true };
	const handlers = {
		onName: () => {},
		onUrl: () => {},
		onEnabled: () => {},
		onSave: () => {},
		onRemove: () => {},
		onRefresh: () => {},
	};

	const registered = await renderSvelte(viewPath, {
		draft,
		status: status(),
		loading: false,
		busy: null,
		error: null,
		notice: "Browser MCP registration saved in Pi.",
		connected: true,
		...handlers,
	});
	expect(registered).toContain('data-testid="browser-mcp-settings"');
	expect(registered).toContain("Registered in Pi");
	expect(registered).toContain("Reachable");
	expect(registered).toContain("Tool 1");
	expect(registered).toContain("2 tools reported.");
	expect(registered).toContain("hosts no browser and never proxies MCP traffic");

	const unregistered = await renderSvelte(viewPath, {
		draft: { ...draft, enabled: false },
		status: status({
			enabled: false,
			registered: false,
			reachable: false,
			tools: [],
			error: "The endpoint probe failed. Check the endpoint and its deployment configuration.",
		}),
		loading: false,
		busy: null,
		error: null,
		notice: null,
		connected: true,
		...handlers,
	});
	expect(unregistered).toContain("Not registered in Pi");
	expect(unregistered).not.toContain("Registered in Pi<");
	expect(unregistered).toContain("Unreachable");
	expect(unregistered).toContain("No tools reported.");
	expect(unregistered).toContain("The endpoint probe failed.");

	const disconnected = await renderSvelte(viewPath, {
		draft,
		status: null,
		loading: false,
		busy: null,
		error: null,
		notice: null,
		connected: false,
		...handlers,
	});
	expect(disconnected).toContain("Controller disconnected");
	expect(disconnected).toContain("leaves Pi's existing MCP configuration unchanged");
	expect(disconnected).toContain("disabled");
	expect(settingsTabs().map((tab) => tab.label)).toContain("Browser");
});
