// Run with the locked runtime:
// bun x bun@1.4.0 pixie/tests/pi-native-parity/mcp-benchmark.ts
// Each sample is a fresh process with an empty metadata cache. These are local
// fixture measurements, not provider token counts or production-host timings.
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Sessions } from "../../../pi/pixie-assistant/src/sessions.ts";
import { piMcpAdapterWithConfig } from "./upstream.ts";

const engine = process.argv[2];
if (!engine) {
	for (let sample = 0; sample < 3; sample++) {
		const child = Bun.spawn([process.execPath, import.meta.path, "adapter"], {
			stdout: "pipe",
			stderr: "pipe",
		});
		const [stdout, stderr, code] = await Promise.all([
			new Response(child.stdout).text(),
			new Response(child.stderr).text(),
			child.exited,
		]);
		if (code !== 0) throw new Error(`Benchmark child failed: ${stderr}`);
		console.log(JSON.stringify({ sample, ...JSON.parse(stdout) }));
	}
} else {
	if (engine !== "adapter")
		throw new Error(
			"The legacy transport was removed. Historical baseline measurements are in docs/mcp-client-verification.md",
		);
	const dir = await mkdtemp(join(tmpdir(), "pixie-mcp-benchmark-"));
	let sessions: Sessions | undefined;
	try {
		process.env.PI_CODING_AGENT_DIR = dir;
		const fixture = join(dir, "server.mjs");
		await writeFile(
			fixture,
			`import readline from 'node:readline';
const tools = Array.from({length: 40}, (_, i) => ({name: 'echo_'+i, description: 'Local benchmark tool returning a small text payload for a fixed input schema.', inputSchema: {type:'object', properties:{text:{type:'string',description:'Text to echo'}, count:{type:'integer',minimum:0}},required:['text']}}));
readline.createInterface({input:process.stdin}).on('line', line => {
 const m=JSON.parse(line); if(m.id===undefined)return;
 const result=m.method==='initialize'?{protocolVersion:m.params.protocolVersion,capabilities:{tools:{}},serverInfo:{name:'benchmark',version:'1'}}:m.method==='tools/list'?{tools}: {content:[{type:'text',text:m.params.arguments.text}]};
 process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:m.id,result})+'\\n');
});`,
		);
		const server = { command: process.execPath, args: [fixture] };
		const rssBefore = process.memoryUsage().rss;
		const started = performance.now();
		sessions = new Sessions(
			dir,
			[
				piMcpAdapterWithConfig({
					agentDir: dir,
					config: { mcpServers: { bench: server }, settings: { scriptMode: false } },
				}),
			],
			() => {},
		);
		const entry = await sessions.create(dir);
		const sessionCreateMs = performance.now() - started;
		const tools = entry.session.agent.state.tools;
		const tool = tools.find((t) => t.name === "mcp");
		if (!tool) throw new Error("Benchmark MCP tool is missing");
		const call = () =>
			tool.execute(
				"benchmark",
				{ server: "bench", tool: "echo_0", args: { text: "ok" } },
				new AbortController().signal,
			);
		const coldStarted = performance.now();
		const cold = await call();
		if (cold.content[0]?.type !== "text" || cold.content[0].text !== "ok")
			throw new Error("Benchmark did not execute fixture tool");
		const coldCallMs = performance.now() - coldStarted;
		const warmCallMs: number[] = [];
		for (let i = 0; i < 20; i++) {
			const start = performance.now();
			await call();
			warmCallMs.push(performance.now() - start);
		}
		const selected = entry.session.agent.state.tools.filter(
			(t) => t.name === "mcp" || t.name.startsWith("bench__"),
		);
		console.log(
			JSON.stringify({
				engine,
				bun: Bun.version,
				fixtureTools: 40,
				mcpVisibleTools: selected.length,
				contextSchemaBytes: Buffer.byteLength(
					JSON.stringify(
						selected.map(({ name, description, parameters }) => ({
							name,
							description,
							parameters,
						})),
					),
				),
				sessionCreateMs,
				coldCallMs,
				startupThroughFirstCallMs: sessionCreateMs + coldCallMs,
				warmCallMs,
				rssBefore,
				rssAfterWarm: process.memoryUsage().rss,
				processPeakRssKiB: process.resourceUsage().maxRSS,
			}),
		);
	} finally {
		await sessions?.close();
		await rm(dir, { recursive: true, force: true });
	}
}
