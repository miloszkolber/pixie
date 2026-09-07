import { registerToolRenderer } from "../../render/tool-registry";
import { signetSummary } from "./signet-card";
import SignetCard from "./signet-card.svelte";

registerToolRenderer("signet_recall", SignetCard, { summary: ({ args }) => signetSummary(args) });
registerToolRenderer("signet_source_search", SignetCard, {
	summary: ({ args }) => signetSummary(args),
});
registerToolRenderer("signet_session_search", SignetCard, {
	summary: ({ args }) => signetSummary(args),
});
registerToolRenderer("signet_remember", SignetCard, { summary: ({ args }) => signetSummary(args) });
