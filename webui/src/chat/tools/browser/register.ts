import { registerToolRenderer } from "../../render/tool-registry";
import { browserSummary } from "./browser-card";
import BrowserCard from "./browser-card.svelte";

registerToolRenderer("browser", BrowserCard, { summary: ({ args }) => browserSummary(args) });
registerToolRenderer("browser_command", BrowserCard, {
	summary: ({ args }) => browserSummary(args),
});
