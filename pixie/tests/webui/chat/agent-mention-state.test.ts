import { expect, test } from "bun:test";
import type { AgentMentionInfo } from "@pixie/contracts";
import {
	agentMentionIdentity,
	fileMentionCandidateIdentity,
	visibleAgentMentions,
	visibleFileMentionCandidates,
} from "@/chat/composer/agent-mention-state";

const mentions: AgentMentionInfo[] = [
	{ name: "Reviewer", description: "Review", sourceType: "agent", mention: "@reviewer" },
];

test("deferred mention results remain scoped to their project and session identity", () => {
	const first = agentMentionIdentity("project-a", "session-a");
	const second = agentMentionIdentity("project-b", "session-b");
	const deferredFirstResponse = { identity: first, mentions };

	expect(visibleAgentMentions(deferredFirstResponse, second)).toEqual([]);
	expect(visibleAgentMentions(deferredFirstResponse, first)).toEqual(mentions);
});

test("deferred file candidates cannot cross a project, root, session, or query boundary", () => {
	const first = fileMentionCandidateIdentity("project-a", "/workspace/a", "session-a", "src");
	const deferredFirstResponse = {
		identity: first,
		candidates: [{ kind: "file", name: "a.ts", path: "src/a.ts" }],
	};
	const second = fileMentionCandidateIdentity("project-b", "/workspace/b", "session-b", "src");

	expect(visibleFileMentionCandidates(deferredFirstResponse, second)).toEqual([]);
	expect(visibleFileMentionCandidates(deferredFirstResponse, first)).toEqual(
		deferredFirstResponse.candidates,
	);
});
