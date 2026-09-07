import { expect, test } from "bun:test";
import { type JSONRPCMessage, JSONRPCMessageSchema } from "@modelcontextprotocol/sdk/types.js";
import { readMcpAppHTML } from "@/chat/tools/apps/mcp-app-client";
import { OriginPinnedAppTransport } from "@/chat/tools/apps/mcp-app-transport";

type AppClientTransport = NonNullable<Parameters<typeof readMcpAppHTML>[3]>;

test("App content reads ordered byte chunks before decoding UTF-8", async () => {
	const bytes = new TextEncoder().encode("<p>é</p>");
	const offsets: number[] = [];
	const transport = {
		request: async (_method: string, params: { offset: number }) => {
			offsets.push(params.offset);
			const end = params.offset === 0 ? 4 : bytes.length;
			return {
				offset: params.offset,
				data: Buffer.from(bytes.subarray(params.offset, end)).toString("base64"),
				nextOffset: end,
			};
		},
	} as unknown as AppClientTransport;
	const html = await readMcpAppHTML(
		{
			viewId: "a".repeat(64),
			url: "https://sandbox.example/view",
			resource: { byteLength: bytes.length },
		},
		{ projectId: "project", sessionId: "session", toolCallId: "tool" },
		new AbortController().signal,
		transport,
	);

	expect(html).toBe("<p>é</p>");
	expect(offsets).toEqual([0, 4]);
});

test("App content rejects out-of-order and non-canonical chunks", async () => {
	for (const chunk of [
		{ offset: 1, data: "Zg==", nextOffset: 2 },
		{ offset: 0, data: "Zh==", nextOffset: 1 },
	]) {
		const transport = {
			request: async () => chunk,
		} as unknown as AppClientTransport;
		await expect(
			readMcpAppHTML(
				{
					viewId: "a".repeat(64),
					url: "https://sandbox.example/view",
					resource: { byteLength: 1 },
				},
				{ projectId: "project", sessionId: "session", toolCallId: "tool" },
				new AbortController().signal,
				transport,
			),
		).rejects.toThrow("invalid");
	}
});

test("App transport pins both the sandbox window and its exact origin", async () => {
	const listeners = new Set<(event: MessageEvent<unknown>) => void>();
	const events = {
		addEventListener: (_type: string, listener: (event: MessageEvent<unknown>) => void) => {
			listeners.add(listener);
		},
		removeEventListener: (_type: string, listener: (event: MessageEvent<unknown>) => void) => {
			listeners.delete(listener);
		},
	};
	const sent: { message: JSONRPCMessage; origin: string }[] = [];
	const target = {
		postMessage: (message: JSONRPCMessage, origin: string) => sent.push({ message, origin }),
	};
	const transport = new OriginPinnedAppTransport(
		target as unknown as Window,
		"https://sandbox.example",
		JSONRPCMessageSchema,
		events as unknown as Window,
	);
	const received: JSONRPCMessage[] = [];
	const errors: Error[] = [];
	transport.onmessage = (message) => received.push(message);
	transport.onerror = (error) => errors.push(error);
	await transport.start();

	const dispatch = (source: unknown, origin: string, data: unknown) => {
		for (const listener of listeners) listener({ source, origin, data } as MessageEvent<unknown>);
	};
	const valid = { jsonrpc: "2.0", method: "ui/notifications/initialized" } as const;
	dispatch({}, "https://sandbox.example", valid);
	dispatch(target, "https://evil.example", valid);
	dispatch(target, "https://sandbox.example", { jsonrpc: "2.0", id: {} });
	dispatch(target, "https://sandbox.example", valid);

	expect(received).toEqual([valid]);
	expect(errors).toHaveLength(1);
	await transport.send({ jsonrpc: "2.0", id: 1, result: {} });
	expect(sent).toEqual([
		{
			message: { jsonrpc: "2.0", id: 1, result: {} },
			origin: "https://sandbox.example",
		},
	]);

	await transport.close();
	dispatch(target, "https://sandbox.example", valid);
	expect(received).toHaveLength(1);
});
