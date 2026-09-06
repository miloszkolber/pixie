// Loaded by the native child through its agent-directory settings, not by a
// replacement RPC runner. This provider never makes a network request.
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { type AssistantMessage, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import { type ExtensionFactory, getAgentDir } from "@earendil-works/pi-coding-agent";

const provider: ExtensionFactory = (pi) => {
	let dismissed = false;
	pi.on("before_agent_start", async (_event, ctx) => {
		dismissed = !(await ctx.ui.confirm("Child fixture", "This must be cancelled headlessly"));
	});
	pi.registerProvider("child-fixture", {
		baseUrl: "http://unused.invalid",
		apiKey: "fixture-only-not-a-credential",
		api: "child-fixture-api",
		models: [
			{
				id: "echo",
				name: "Child fixture",
				reasoning: false,
				input: ["text"],
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
				contextWindow: 64000,
				maxTokens: 1024,
			},
		],
		streamSimple: (model, context) => {
			const prompts = context.messages
				.filter((message) => message.role === "user")
				.map((message) =>
					typeof message.content === "string"
						? message.content
						: message.content
								.filter((part) => part.type === "text")
								.map((part) => part.text)
								.join(""),
				);
			appendFileSync(
				join(getAgentDir(), "child-audit.jsonl"),
				`${JSON.stringify({
					pid: process.pid,
					argv: process.argv,
					execPath: process.execPath,
					bun: process.versions.bun,
					agentDir: getAgentDir(),
					dismissed,
					prompts,
				})}\n`,
			);
			const stream = createAssistantMessageEventStream();
			// Keep an actual provider turn active until the parent cancels its child.
			if (prompts.at(-1) === "hold") return stream;
			const message: AssistantMessage = {
				role: "assistant",
				api: model.api,
				provider: model.provider,
				model: model.id,
				content: [{ type: "text", text: JSON.stringify(prompts) }],
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
export default provider;
