import { registerToolRenderer } from "../../render/tool-registry";
import { strArg } from "../tool-helpers";
import WebFetchCard from "./web-fetch-card.svelte";
import WebSearchCard from "./web-search-card.svelte";

registerToolRenderer("web_search", WebSearchCard, { summary: ({ args }) => strArg(args, "query") });
registerToolRenderer("fetch_content", WebFetchCard, { summary: ({ args }) => strArg(args, "url") });

registerToolRenderer("web_fetch", WebFetchCard, { summary: ({ args }) => strArg(args, "url") });
