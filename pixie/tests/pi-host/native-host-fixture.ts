import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { startHost } from "../../pi-host/src/server.ts";

const [dir, project, profile] = process.argv.slice(2);
if (!dir || !project) throw new Error("Fixture directories required");
await mkdir(join(dir, "extensions"), { recursive: true });
await writeFile(
	join(dir, "extensions", "provider.ts"),
	`export { default } from ${JSON.stringify(new URL("./provider-fixture.ts", import.meta.url).pathname)};`,
);
if (profile === "project") {
	await mkdir(join(project, ".pi", "extensions"), { recursive: true });
	await writeFile(
		join(project, ".pi", "extensions", "agents.ts"),
		`import agents from ${JSON.stringify(new URL("../../pi-host/src/extensions/agents.ts", import.meta.url).pathname)}; export default pi => agents(pi, ${JSON.stringify(dir)});`,
	);
}
const host = await startHost({
	agentDir: dir,
	secret: "native-fixture-secret",
	port: 0,
	extensions: profile === "optional" ? ["agents", "plans", "mcp"] : [],
});
const entry = await host.sessions.create(project);
await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
const manager = entry.session.sessionManager;
const kept = manager.appendMessage({ role: "user", content: "Native seed", timestamp: 1 });
manager.appendCompaction("Native summary retained", kept, 4000);
manager.appendCustomMessageEntry("hidden", "Hidden native entry", false);
console.log(
	JSON.stringify({
		url: `ws://127.0.0.1:${host.server.port}/pi`,
		sessionId: entry.session.sessionId,
	}),
);
process.on("SIGTERM", async () => {
	await host.close();
	process.exit(0);
});
