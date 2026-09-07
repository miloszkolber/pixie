import type { Transport } from "@modelcontextprotocol/sdk/shared/transport.js";
import type { JSONRPCMessage } from "@modelcontextprotocol/sdk/types.js";

interface JsonRpcSchema {
	safeParse(
		value: unknown,
	): { success: true; data: JSONRPCMessage } | { success: false; error: unknown };
}

export class OriginPinnedAppTransport implements Transport {
	onclose?: () => void;
	onerror?: (error: Error) => void;
	onmessage?: <T extends JSONRPCMessage>(message: T) => void;

	private started = false;
	private readonly receive = (event: MessageEvent<unknown>) => {
		if (event.source !== this.target || event.origin !== this.origin) return;
		let parsed: ReturnType<JsonRpcSchema["safeParse"]>;
		try {
			parsed = this.schema.safeParse(event.data);
		} catch {
			this.onerror?.(new Error("The App sandbox sent an invalid JSON-RPC message."));
			return;
		}
		if (!parsed.success) {
			this.onerror?.(new Error("The App sandbox sent an invalid JSON-RPC message."));
			return;
		}
		this.onmessage?.(parsed.data);
	};

	constructor(
		private readonly target: Window,
		private readonly origin: string,
		private readonly schema: JsonRpcSchema,
		private readonly events: Window = window,
	) {}

	async start(): Promise<void> {
		if (this.started) throw new Error("The App transport has already started.");
		this.started = true;
		this.events.addEventListener("message", this.receive);
	}

	async send(message: JSONRPCMessage): Promise<void> {
		try {
			this.target.postMessage(message, this.origin);
		} catch (error) {
			const failure =
				error instanceof Error ? error : new Error("The App message could not be sent.");
			this.onerror?.(failure);
			throw failure;
		}
	}

	async close(): Promise<void> {
		if (!this.started) return;
		this.started = false;
		this.events.removeEventListener("message", this.receive);
		this.onclose?.();
	}
}
