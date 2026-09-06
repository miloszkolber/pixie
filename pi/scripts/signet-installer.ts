// Private subprocess entry: never run the upstream installer in the operator's HOME.
import { resolve } from "node:path";

const argument = process.argv[2];
if (process.argv.length !== 3 || !argument || !process.env.PIXIE_SIGNET_STAGING) {
	throw new Error("Use overlay.ts configure, not signet-installer.ts directly");
}
const stage = resolve(argument);
if (
	process.env.HOME !== stage ||
	process.env.XDG_CONFIG_HOME !== `${stage}/config` ||
	process.env.PI_CODING_AGENT_DIR !== `${stage}/agent`
) {
	throw new Error("Signet staging environment mismatch");
}
const { PiConnector } = await import(
	import.meta.resolve(
		"@signetai/connector-pi",
		new URL("../host/package.json", import.meta.url).href,
	)
);
const connector = new PiConnector();
if (connector.getConfigPath() !== `${stage}/agent/extensions/signet-pi.js`) {
	throw new Error("Upstream Signet destination changed");
}
const result = await connector.install("");
if (!result.success) throw new Error("Signet installation failed");
