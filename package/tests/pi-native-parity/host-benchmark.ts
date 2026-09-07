// Run with the locked runtime:
// bun x bun@1.4.0 pixie/tests/pi-native-parity/host-benchmark.ts
// Each sample is a fresh process. Fixture-only local measurements: host
// startup/session latency, memory delta and tool-surface size for the
// baseline (no optional factories) versus the overlay set. Not provider
// token counts, not production timings, not arm64 (x86-64 only here).
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import rpivAsk from "@juicesharp/rpiv-ask-user-question";
import rpivTodo from "@juicesharp/rpiv-todo";
import rpivWeb from "@juicesharp/rpiv-web-tools";
import { Sessions } from "../../../assistant/src/sessions.ts";
import { piSubagent } from "./upstream.ts";

const config = process.argv[2];
if (!config) {
	for (const name of ["baseline", "overlay"]) {
		for (let sample = 0; sample < 3; sample++) {
			const child = Bun.spawn([process.execPath, import.meta.path, name], {
				stdout: "pipe",
				stderr: "pipe",
			});
			const [stdout, stderr, code] = await Promise.all([
				new Response(child.stdout).text(),
				new Response(child.stderr).text(),
				child.exited,
			]);
			if (code !== 0) throw new Error(`Benchmark child failed: ${stderr}`);
			console.log(JSON.stringify({ config: name, sample, ...JSON.parse(stdout) }));
		}
	}
} else {
	const factories = config === "overlay" ? [rpivTodo, rpivWeb, rpivAsk, piSubagent] : [];
	const dir = await mkdtemp(join(tmpdir(), "pixie-host-benchmark-"));
	let sessions: Sessions | undefined;
	try {
		const rssBefore = process.memoryUsage().rss;
		const started = performance.now();
		sessions = new Sessions(dir, factories, () => {});
		const entry = await sessions.create(dir);
		const createMs = Math.round((performance.now() - started) * 10) / 10;
		const rssDeltaMb = Math.round(((process.memoryUsage().rss - rssBefore) / 1048576) * 10) / 10;
		const names = entry.session.getActiveToolNames();
		const schemas = entry.session.agent.state.tools.map((tool) =>
			JSON.stringify({ name: tool.name, description: tool.description }),
		);
		console.log(
			JSON.stringify({
				createMs,
				rssDeltaMb,
				toolCount: names.length,
				toolSurfaceBytes: schemas.join("").length,
			}),
		);
	} finally {
		await sessions?.close();
		await rm(dir, { recursive: true, force: true });
	}
}
