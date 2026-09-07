import { parseArgs } from "node:util";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import { startHost } from "./server.ts";

const { values } = parseArgs({
	options: {
		"agent-dir": { type: "string" },
		host: { type: "string" },
		port: { type: "string" },
		extensions: { type: "string" },
		version: { type: "boolean" },
	},
});
if (values.version) {
	console.log("pixie-pi 0.1.0 (Pi SDK 0.85.1)");
	process.exit(0);
}
const host = await startHost({
	agentDir: values["agent-dir"] ?? getAgentDir(),
	hostname: values.host ?? "127.0.0.1",
	port: Number(values.port ?? 3284),
	extensions: values.extensions ? values.extensions.split(",") : [],
	secret: process.env.PIXIE_PI_SECRET_KEY ?? "",
});
console.log(`Pi host listening on ${host.server.hostname}:${host.server.port}`);
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
