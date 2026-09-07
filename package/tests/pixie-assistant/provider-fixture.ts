import { type AssistantMessage, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
export function makeProvider(
	pause?: Promise<void>,
	finalOnly = false,
	output = "Hello from Pi",
	chunkSize = output.length,
): ExtensionFactory {
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
			streamSimple: () => {
				const stream = createAssistantMessageEventStream();
				const message: AssistantMessage = {
					role: "assistant",
					api: "fixture-api",
					provider: "fixture",
					model: "echo",
					content: [{ type: "text", text: output }],
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
				queueMicrotask(async () => {
					if (!finalOnly) {
						stream.push({ type: "start", partial: { ...message, content: [] } });
						stream.push({
							type: "text_start",
							contentIndex: 0,
							partial: { ...message, content: [{ type: "text", text: "" }] },
						});
						for (let offset = 0; offset < output.length; offset += chunkSize) {
							stream.push({
								type: "text_delta",
								contentIndex: 0,
								delta: output.slice(offset, offset + chunkSize),
								partial: {
									...message,
									content: [{ type: "text", text: output.slice(0, offset + chunkSize) }],
								},
							});
						}
						stream.push({
							type: "text_end",
							contentIndex: 0,
							content: output,
							partial: message,
						});
					}
					await pause;
					stream.push({ type: "done", reason: "stop", message });
					stream.end(message);
				});
				return stream;
			},
		});
	};
}

export default makeProvider();
