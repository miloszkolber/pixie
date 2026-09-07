// Opt-in CPU/allocation probe: bun --tsconfig-override tests/tsconfig.json tests/performance/transcript.ts
// This measures projection work, not browser paint or deployment-host latency.
import type { TranscriptMessage } from "@pixie/contracts";
import { messagesToRuntime } from "../../webui/src/chat/runtime/hydrate";
import { deriveRows } from "../../webui/src/chat/runtime/rows";
import {
	createSessionRuntime,
	reduceSessionEvent,
	type SessionRuntime,
} from "../../webui/src/chat/runtime/session-runtime";

const percentile = (values: number[], fraction: number) =>
	[...values].sort((a, b) => a - b)[
		Math.min(values.length - 1, Math.floor(values.length * fraction))
	];
for (const count of [100, 1000, 10000]) {
	const messages: TranscriptMessage[] = Array.from({ length: count }, (_, index) =>
		index % 2 === 0
			? { role: "user", content: `Question ${index}`, timestamp: index }
			: {
					role: "assistant",
					messageId: `message-${index}`,
					content: [
						{
							type: "text",
							text: "Synthetic response with enough text to represent a normal paragraph. ".repeat(
								8,
							),
						},
					],
					timestamp: index,
				},
	);
	Bun.gc(true);
	const before = process.memoryUsage().heapUsed;
	const start = performance.now();
	let runtime: SessionRuntime = {
		...createSessionRuntime(null, "off"),
		...messagesToRuntime(messages, { isStreaming: true }),
		isStreaming: true,
	};
	const hydrationMs = performance.now() - start;
	const heapDelta = process.memoryUsage().heapUsed - before;
	const samples: number[] = [];
	let rows = 0;
	for (let index = 0; index < 120; index++) {
		const started = performance.now();
		runtime = reduceSessionEvent(runtime, {
			type: "text",
			messageId: `message-${count - 1}`,
			text: " delta",
		});
		rows = deriveRows(runtime.turns, runtime.toolResults, true).length;
		if (index >= 20) samples.push(performance.now() - started);
	}
	console.log(
		JSON.stringify({
			messages: count,
			rows,
			hydrationMs,
			heapDeltaBytes: heapDelta,
			chunkAndRowsMedianMs: percentile(samples, 0.5),
			chunkAndRowsP95Ms: percentile(samples, 0.95),
		}),
	);
}
