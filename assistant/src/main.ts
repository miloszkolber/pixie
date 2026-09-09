#!/usr/bin/env bun
import { resolve } from "node:path";
import { parseArgs } from "node:util";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import manifest from "../package.json" with { type: "json" };
import { DEFAULT_SERVICE_DRAIN_DEADLINE_MS } from "./lifecycle.ts";
import { startHost } from "./server.ts";
import { parseAssistantPort, validateAssistantHost, validateAssistantSecret } from "./startup.ts";
import { NativeJsonlTransport, spawnNativeChild } from "./transport/jsonl-transport.ts";

const { values } = parseArgs({
	options: {
		"agent-dir": { type: "string" },
		host: { type: "string" },
		port: { type: "string" },
		llama: { type: "boolean" },
		"native-transport": { type: "string" },
		"native-arg": { type: "string", multiple: true },
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
const hostname = validateAssistantHost(values.host);
const port = parseAssistantPort(values.port);
const secret = validateAssistantSecret(process.env.PIXIE_PI_SECRET_KEY);
const agentDir = resolve(values["agent-dir"] ?? getAgentDir());
// Native extensions use Pi's process-level directory convention. The service
// owns one agent directory, including when selected through the CLI flag.
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.MCP_UI_VIEWER ??= "none";

const nativeTransportCommand = values["native-transport"] ?? process.env.PIXIE_NATIVE_TRANSPORT_COMMAND;
const nativeTransportArgs = values["native-arg"] ?? [];
const nativeTransport = nativeTransportCommand
	? new NativeJsonlTransport({
			child: () =>
				spawnNativeChild({
					command: nativeTransportCommand,
					args: nativeTransportArgs,
					cwd: agentDir,
					// Do not inherit PIXIE_* or provider credentials. Native Pi
					// state remains owned by the selected agent directory.
					env: {
						...(process.env.PATH ? { PATH: process.env.PATH } : {}),
						...(process.env.HOME ? { HOME: process.env.HOME } : {}),
						PI_CODING_AGENT_DIR: agentDir,
					},
				}),
		})
	: undefined;

const requestedRestartExitCode = 75;
const drainDeadlineMs = DEFAULT_SERVICE_DRAIN_DEADLINE_MS;
let host: Awaited<ReturnType<typeof startHost>> | undefined;
let closing = false;
const close = (exitCode: number) => {
	if (closing) return;
	closing = true;
	const current = host;
	if (!current) {
		process.exit(1);
		return;
	}
	const deadline = setTimeout(() => {
		console.error(`pixie-assistant shutdown exceeded ${drainDeadlineMs}ms`);
		process.exit(exitCode === requestedRestartExitCode ? requestedRestartExitCode : 1);
	}, drainDeadlineMs);
	void current.close().then(
		() => {
			clearTimeout(deadline);
			process.exit(exitCode);
		},
		(error) => {
			clearTimeout(deadline);
			console.error("pixie-assistant shutdown failed", error);
			process.exit(exitCode === requestedRestartExitCode ? requestedRestartExitCode : 1);
		},
	);
};

host = await startHost({
	agentDir: process.env.PI_CODING_AGENT_DIR,
	hostname,
	port,
	llama: values.llama,
	secret,
	nativeTransport,
	allowSelfRestart: process.env.PIXIE_ALLOW_SELF_RESTART === "1",
	onRestart: () => close(requestedRestartExitCode),
});
console.log(`pixie-assistant listening on ${host.server.hostname}:${host.server.port}`);
process.on("SIGTERM", () => close(0));
process.on("SIGINT", () => close(0));
