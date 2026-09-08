#!/usr/bin/env bun
// Standalone agent entrypoint: packages the assistant service as a single
// self-contained executable. The build embeds the checked-out assistant
// sources, so the binary always matches the working tree; no vendoring step
// exists. The binary is a plain headless Pi host: native settings, sessions,
// agent definitions and extensions stay operator-managed files on disk and
// nothing is baked in. Heavy service imports stay dynamic so `--version`
// never evaluates Pi loading.
import { realpath } from "node:fs/promises";
import { resolve } from "node:path";
import { parseArgs } from "node:util";
import assistantManifest from "../assistant/package.json" with { type: "json" };

const { values } = parseArgs({
	options: {
		"agent-dir": { type: "string" },
		host: { type: "string" },
		port: { type: "string" },
		llama: { type: "boolean" },
		version: { type: "boolean" },
	},
});

if (values.version) {
	const version = (assistantManifest as { version?: string }).version ?? "0.0.0-dev";
	console.log(`pi-agent (pixie-assistant ${version})`);
	process.exit(0);
}

const { getAgentDir } = await import("@earendil-works/pi-coding-agent");
const { startHost } = await import("../assistant/src/server.ts");

const agentDir = resolve(values["agent-dir"] ?? process.env.PI_CODING_AGENT_DIR ?? getAgentDir());
// Native extensions use Pi's process-level directory convention.
process.env.PI_CODING_AGENT_DIR = await realpath(agentDir).catch(() => agentDir);
process.env.MCP_UI_VIEWER ??= "none";

const host = await startHost({
	agentDir: process.env.PI_CODING_AGENT_DIR,
	hostname: values.host ?? "127.0.0.1",
	port: Number(values.port ?? 3284),
	llama: values.llama,
	secret: process.env.PIXIE_PI_SECRET_KEY ?? "",
	allowSelfRestart: process.env.PIXIE_ALLOW_SELF_RESTART === "1",
});
console.log(`pi-agent listening on ${host.server.hostname}:${host.server.port}`);

let closing = false;
const close = () => {
	if (closing) return;
	closing = true;
	void host.close().then(
		() => process.exit(0),
		() => process.exit(1),
	);
};
process.on("SIGTERM", close);
process.on("SIGINT", close);
