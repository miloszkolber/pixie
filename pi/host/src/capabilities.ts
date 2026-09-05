import type { AgentSession, ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { RecordValue } from "./storage.ts";

export const CAPABILITY_EVENT = "pixie:capability:v1";
export interface CapabilityContext {
	cwd: string;
	agentDir: string;
	session: AgentSession;
	signal: AbortSignal;
	notify: (event: RecordValue) => void;
}
export interface Capability {
	id: string;
	version: 1;
	operations: Record<
		string,
		(params: RecordValue, context: CapabilityContext) => unknown | Promise<unknown>
	>;
	close?: () => void | Promise<void>;
}
// Extensions remain ordinary Pi extensions. Registration only exposes additive
// services to a compatible host; no model hooks or tool interception are used.
export function registerCapability(pi: ExtensionAPI, capability: Capability): void {
	pi.events.emit(CAPABILITY_EVENT, capability);
}
const requiredOperations: Record<string, string[]> = {
	agents: [
		"pi.sources.list",
		"pi.sources.create",
		"pi.sources.update",
		"pi.sources.delete",
		"pi.agent-mentions.list",
	],
	mcp: [
		"mcp.attach",
		"pi.config.extensions.list",
		"pi.config.extensions.add",
		"pi.config.extensions.set-enabled",
		"pi.config.extensions.remove",
		"pi.session.extensions.list",
		"pi.session.extensions.add",
		"pi.session.extensions.remove",
		"pi.resources.read",
		"pi.tools.call",
	],
	plans: ["plans.read"],
};
export class Capabilities {
	private conflicts = new Set<string>();
	private entries = new Map<string, Capability>();
	private cleanups = new Set<NonNullable<Capability["close"]>>();
	register(value: unknown): void {
		const c = value as Capability;
		if (
			!c ||
			c.version !== 1 ||
			!/^[a-z][a-z0-9-]{0,63}$/.test(c.id) ||
			!c.operations ||
			Object.values(c.operations).some((f) => typeof f !== "function")
		)
			return;
		if (
			this.conflicts.has(c.id) ||
			(requiredOperations[c.id] ?? []).some((method) => typeof c.operations[method] !== "function")
		)
			return;
		if (typeof c.close === "function") this.cleanups.add(c.close);
		if (this.entries.has(c.id)) {
			this.entries.delete(c.id);
			this.conflicts.add(c.id);
			return;
		}
		this.entries.set(c.id, c);
	}
	snapshot(): Record<string, number> {
		return Object.fromEntries(
			[...this.entries]
				.filter(([id]) => id !== "mcp-apps" || this.entries.has("mcp"))
				.map(([id, c]) => [id, c.version]),
		);
	}
	async call(method: string, params: RecordValue, context: CapabilityContext): Promise<unknown> {
		for (const c of this.entries.values())
			if (Object.hasOwn(c.operations, method)) return c.operations[method](params, context);
		throw new Error(`Unsupported capability operation: ${method}`);
	}
	async close(): Promise<void> {
		await Promise.allSettled([...this.cleanups].map((close) => close()));
		this.cleanups.clear();
		this.entries.clear();
	}
}
