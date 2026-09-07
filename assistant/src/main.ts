#!/usr/bin/env bun
import { resolve } from "node:path";
import { parseArgs } from "node:util";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import manifest from "../package.json" with { type: "json" };
import { startHost } from "./server.ts";

const { values } = parseArgs({
	options: {
		"agent-dir": { type: "string" },
		host: { type: "string" },
		port: { type: "string" },
		llama: { type: "boolean" },
		version: { type: "boolean" },
	},
});
const assistantVersion: string = manifest.version ?? "0.0.0-dev";
const piSdkVersion: string =
	manifest.dependencies?.["@earendil-works/pi-coding-agent"] ?? "unknown";
if (values.version) {
	console.log(`pixie-assistant ${assistantVersion} (Pi SDK ${piSdkVersion})`);
	process.exit(0);
}
const agentDir = resolve(values["agent-dir"] ?? getAgentDir());
// Native extensions use Pi's process-level directory convention. The service
// owns one agent directory, including when selected through the CLI flag.
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.MCP_UI_VIEWER ??= "none";
const host = await startHost({
	agentDir,
	hostname: values.host ?? "127.0.0.1",
	port: Number(values.port ?? 3284),
	llama: values.llama,
	secret: process.env.PIXIE_PI_SECRET_KEY ?? "",
});
console.log(`pixie-assistant listening on ${host.server.hostname}:${host.server.port}`);
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
