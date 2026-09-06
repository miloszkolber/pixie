import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { type AssistantMessage, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { Sessions } from "../../../pi/host/src/sessions.ts";

export const cleanups: (() => Promise<unknown>)[] = [];

export async function tempDir(prefix: string): Promise<string> {
	const dir = await mkdtemp(`${tmpdir()}/${prefix}`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	return dir;
}

export async function fixture(factories: ExtensionFactory[] = []) {
	const dir = await tempDir("pixie-pi-parity-");
	const events: unknown[] = [];
	const sessions = new Sessions(dir, factories, (_id, event) => events.push(event));
	cleanups.push(() => sessions.close());
	return { dir, sessions, events };
}

// Minimal echo provider so sessions can prompt without network credentials.
export function echoProvider(): ExtensionFactory {
	return (pi) => {
		pi.registerProvider("fixture", {
			baseUrl: "http://localhost/unused",
			apiKey: "fixture-only-key",
			api: "fixture-api",
			models: [
				{
					id: "echo",
					name: "Echo",
					reasoning: false,
					input: ["text", "image"],
					cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
					contextWindow: 64000,
					maxTokens: 1024,
				},
			],
			streamSimple: (_model, _context) => {
				const stream = createAssistantMessageEventStream();
				const message: AssistantMessage = {
					role: "assistant",
					api: "fixture-api",
					provider: "fixture",
					model: "echo",
					content: [{ type: "text", text: "Hello from Pi" }],
					stopReason: "stop",
					timestamp: Date.now(),
					usage: {
						input: 5,
						output: 4,
						cacheRead: 0,
						cacheWrite: 0,
						totalTokens: 9,
						cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
					},
				};
				queueMicrotask(() => {
					stream.push({ type: "done", reason: "stop", message });
					stream.end(message);
				});
				return stream;
			},
		});
	};
}

export function toolNames(entry: { session: { getActiveToolNames(): string[] } }): string[] {
	return entry.session.getActiveToolNames();
}

export function findTool(entry: any, name: string): any {
	const tool = entry.session.agent.state.tools.find((t: any) => t.name === name);
	if (!tool) throw new Error(`Tool unavailable: ${name}`);
	return tool;
}
