import { registerToolRenderer } from "../../render/tool-registry";
import { strArg } from "../tool-helpers";
import { subagentSummary } from "./subagent-card";
import SubagentCard from "./subagent-card.svelte";

registerToolRenderer("subagent", SubagentCard, { summary: ({ args }) => subagentSummary(args) });
registerToolRenderer("delegate", SubagentCard, { summary: ({ args }) => subagentSummary(args) });
registerToolRenderer("load", SubagentCard, { summary: ({ args }) => strArg(args, "source") });
