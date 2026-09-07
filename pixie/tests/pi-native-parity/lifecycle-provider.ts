// Native file extension shared by SDK sessions and real RPC children. No network.
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { type AssistantMessage, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";

const provider: ExtensionFactory = (pi) => {
	let cwd = "";
	let retried = false;
	let continued = false;
	const audit = (value: unknown) =>
		appendFileSync(join(cwd, "lifecycle-audit.jsonl"), `${JSON.stringify(value)}\n`);
	pi.on("session_start", (_event, ctx) => {
		cwd = ctx.cwd;
		audit({ event: "start", id: ctx.sessionManager.getSessionId(), pid: process.pid });
	});
	pi.on("session_shutdown", () => audit({ event: "shutdown", pid: process.pid }));
	pi.on("session_compact", (_event, ctx) => {
		audit({ event: "compact", id: ctx.sessionManager.getSessionId() });
	});
	pi.on("agent_end", (event) => {
		if (!continued && JSON.stringify(event.messages).includes("continue-fixture")) {
			continued = true;
			pi.sendUserMessage("continuation-dialog", { deliverAs: "followUp" });
		}
	});
	pi.registerTool({
		name: "lifecycle_dialog",
		label: "Lifecycle dialog",
		description: "Wait for native UI",
		parameters: { type: "object", properties: {} },
		execute: async (_id, _params, _signal, onUpdate, ctx) => {
			onUpdate?.({ content: [{ type: "text", text: "waiting" }], details: {} });
			const answer = await ctx.ui.input("Lifecycle continuation");
			return {
				content: [{ type: "text", text: answer ?? "cancelled" }],
				details: { answer: answer ?? null },
			};
		},
	});
	pi.registerProvider("lifecycle-fixture", {
		baseUrl: "http://unused.invalid",
		apiKey: "fixture-not-a-secret",
		api: "lifecycle-fixture-api",
		models: ["echo", "alternate"].map((id) => ({
			id,
			name: id,
			reasoning: false,
			input: ["text"],
			cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
			contextWindow: 64000,
			maxTokens: 1024,
		})),
		streamSimple: (model, context) => {
			const stream = createAssistantMessageEventStream();
			const last = context.messages.at(-1);
			const prompt = context.messages.filter((message) => message.role === "user").at(-1);
			const text =
				typeof prompt?.content === "string"
					? prompt.content
					: (prompt?.content
							.filter((part) => part.type === "text")
							.map((part) => part.text)
							.join("") ?? "");
			const summary = context.systemPrompt?.includes("context summarization assistant") ?? false;
			const error = text === "retry-fixture" && !retried;
			if (error) retried = true;
			const tool =
				!summary &&
				last?.role === "user" &&
				["make-todo", "continuation-dialog", "stop-dialog"].includes(text);
			const probes = !summary && last?.role === "user" && text === "probe-native";
			const message: AssistantMessage = {
				role: "assistant",
				api: model.api,
				provider: model.provider,
				model: model.id,
				content: probes
					? ["user", "project"].map((scope) => ({
							type: "toolCall",
							id: crypto.randomUUID(),
							name: `unknown_${scope}`,
							arguments: {},
						}))
					: tool
						? [
								{
									type: "toolCall",
									id: crypto.randomUUID(),
									name: text === "make-todo" ? "todo" : "lifecycle_dialog",
									arguments:
										text === "make-todo"
											? { action: "create", subject: "Survive real compaction" }
											: {},
								},
							]
						: [
								{
									type: "text",
									text: summary
										? "## Goal\nNative compaction fixture summary. Preserve todo and extension state."
										: "fixture-complete",
								},
							],
				stopReason: error ? "error" : tool || probes ? "toolUse" : "stop",
				...(error ? { errorMessage: "503 overloaded fixture" } : {}),
				timestamp: Date.now(),
				usage: {
					input: Math.ceil(JSON.stringify(context.messages).length / 4),
					output: 20,
					cacheRead: 0,
					cacheWrite: 0,
					totalTokens: Math.ceil(JSON.stringify(context.messages).length / 4) + 20,
					cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
				},
			};
			audit({
				event: "stream",
				summary,
				textLength: text.length,
				model: model.id,
				tools: context.tools?.map((tool) => tool.name),
				system: context.systemPrompt,
				error,
			});
			queueMicrotask(() => {
				if (message.stopReason === "error")
					stream.push({ type: "error", reason: "error", error: message });
				else
					stream.push({ type: "done", reason: message.stopReason as "stop" | "toolUse", message });
				stream.end(message);
			});
			return stream;
		},
	});
};
export default provider;
