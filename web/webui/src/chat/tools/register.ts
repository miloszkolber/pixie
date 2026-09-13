import { projectRelativePath } from "@/lib";
import { registerToolRenderer } from "../render/tool-registry";
import AskUserQuestionCard from "./ask-user-question-card.svelte";
import BashCard from "./bash-card.svelte";
import EditCard from "./edit-card.svelte";
import ReadCard from "./read-card.svelte";
import { strArg } from "./tool-helpers";
import "./web/register";
import WriteCard from "./write-card.svelte";
import "./browser/register";
import "./subagent/register";
import "./signet/register";

registerToolRenderer("bash", BashCard, { summary: ({ args }) => strArg(args, "command") });
registerToolRenderer("shell", BashCard, { summary: ({ args }) => strArg(args, "command") });
registerToolRenderer("read", ReadCard, {
	summary: ({ args, projectAreaRoot }) =>
		projectRelativePath(strArg(args, "path"), projectAreaRoot),
});
registerToolRenderer("edit", EditCard, {
	summary: ({ args, projectAreaRoot }) =>
		projectRelativePath(strArg(args, "path"), projectAreaRoot),
});
registerToolRenderer("write", WriteCard, {
	summary: ({ args, projectAreaRoot }) =>
		projectRelativePath(strArg(args, "path"), projectAreaRoot),
});

registerToolRenderer("ask_user_question", AskUserQuestionCard, { chrome: "bare" });
