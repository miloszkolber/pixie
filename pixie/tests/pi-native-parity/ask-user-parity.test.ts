import { afterEach, expect, test } from "bun:test";
import {
	ASK_USER_BLOCKED_EVENT,
	ASK_USER_PROMPT_EVENT,
} from "@juicesharp/rpiv-ask-user-question/events";
import rpivAsk from "@juicesharp/rpiv-ask-user-question";
import { Sessions } from "../../../pi/pixie-assistant/src/sessions.ts";
import { cleanups, findTool, fixture } from "./helpers.ts";

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

const QUESTION = {
	question: "Which color?",
	header: "Color",
	options: [
		{ label: "Red", description: "Warm" },
		{ label: "Blue", description: "Cool" },
	],
};

function uiRequests(events: unknown[]): Record<string, any>[] {
	return (events as Record<string, any>[]).filter((event) => event?.type === "pixie:ui:request");
}

function sessionEvents(events: unknown[], type: string): Record<string, any>[] {
	return (events as Record<string, any>[]).filter((event) => event?.type === type);
}

async function waitForUiRequest(events: unknown[]): Promise<Record<string, any>> {
	for (let attempt = 0; attempt < 200; attempt++) {
		const found = uiRequests(events).at(-1);
		if (found) return found;
		await Bun.sleep(10);
	}
	throw new Error("UI request was never published");
}

async function answer(
	sessions: Sessions,
	sessionId: string,
	request: Record<string, any>,
	value: string | boolean,
	cancelled = false,
): Promise<unknown> {
	return sessions.call("session.uiResponse", {
		sessionId,
		requestId: request.requestId,
		...(cancelled ? { cancelled: true } : { value }),
	});
}

test("the profile registers exactly one ask_user_question tool", async () => {
	const { dir, sessions } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const matches = entry.session.getActiveToolNames().filter((name) => name === "ask_user_question");
	expect(matches).toHaveLength(1);
	expect(entry.session.getActiveToolNames()).toContain("ask_user_question");
});

test("an option answer returns the answered envelope", async () => {
	const { dir, sessions, events } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const pending = findTool(entry, "ask_user_question").execute(
		"parity-ask-answer",
		{ questions: [QUESTION] },
		new AbortController().signal,
	);
	const request = await waitForUiRequest(events);
	expect(request.primitive).toBe("select");
	expect(request.options[0]).toContain("Red");
	expect(
		await answer(sessions, entry.session.sessionId, request, request.options[0]),
	).toMatchObject({
		ok: true,
	});
	const result = await pending;
	expect((result.content[0] as { text: string }).text).toMatch(
		/User has answered your questions:.*Which color\?.*Red.*continue with the user's answers/s,
	);
	expect(result.details).toMatchObject({
		answers: [{ questionIndex: 0, question: "Which color?", kind: "option", answer: "Red" }],
		cancelled: false,
	});
});

test("the sentinel row leads to a custom typed answer", async () => {
	const { dir, sessions, events } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const pending = findTool(entry, "ask_user_question").execute(
		"parity-ask-custom",
		{ questions: [QUESTION] },
		new AbortController().signal,
	);
	const select = await waitForUiRequest(events);
	const sentinel = select.options.at(-1) as string;
	expect(sentinel).toMatch(/type something/i);
	expect(await answer(sessions, entry.session.sessionId, select, sentinel)).toMatchObject({
		ok: true,
	});
	const input = await waitForUiRequest(events.filter((event) => event !== select));
	expect(input.primitive).toBe("input");
	expect(await answer(sessions, entry.session.sessionId, input, "Green")).toMatchObject({
		ok: true,
	});
	const result = await pending;
	expect(result.details).toMatchObject({
		answers: [{ questionIndex: 0, question: "Which color?", kind: "custom", answer: "Green" }],
		cancelled: false,
	});
});

test("a multi-select answer returns selected labels", async () => {
	const { dir, sessions, events } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const pending = findTool(entry, "ask_user_question").execute(
		"parity-ask-multi",
		{ questions: [{ ...QUESTION, multiSelect: true }] },
		new AbortController().signal,
	);
	const request = await waitForUiRequest(events);
	expect(request.primitive).toBe("input");
	expect(await answer(sessions, entry.session.sessionId, request, "1,2")).toMatchObject({
		ok: true,
	});
	const result = await pending;
	expect(result.details).toMatchObject({
		answers: [{ questionIndex: 0, kind: "multi", answer: null, selected: ["Red", "Blue"] }],
		cancelled: false,
	});
});

test("dismissing the dialog returns the cancelled envelope", async () => {
	const { dir, sessions, events } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const pending = findTool(entry, "ask_user_question").execute(
		"parity-ask-cancel",
		{ questions: [QUESTION] },
		new AbortController().signal,
	);
	const request = await waitForUiRequest(events);
	expect(await answer(sessions, entry.session.sessionId, request, "", true)).toMatchObject({
		ok: true,
	});
	const result = await pending;
	expect((result.content[0] as { text: string }).text).toContain(
		"User declined to answer questions",
	);
	expect(result.details).toMatchObject({ answers: [], cancelled: true });
});

test("invalid questionnaires return error envelopes without prompting", async () => {
	const { dir, sessions, events } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const signal = new AbortController().signal;
	const empty = await findTool(entry, "ask_user_question").execute(
		"parity-ask-empty",
		{ questions: [] },
		signal,
	);
	expect(empty.details).toMatchObject({ answers: [], cancelled: true });
	expect(empty.details.error).toBeString();
	const reserved = await findTool(entry, "ask_user_question").execute(
		"parity-ask-reserved",
		{
			questions: [
				{
					...QUESTION,
					options: [
						{ label: "Other", description: "Reserved" },
						{ label: "Blue", description: "Cool" },
					],
				},
			],
		},
		signal,
	);
	expect(reserved.details).toMatchObject({ answers: [], cancelled: true });
	expect(reserved.details.error).toBe("reserved_label");
	expect(uiRequests(events)).toHaveLength(0);
});

test("a headless tool call returns the no_ui error envelope", async () => {
	const { dir, sessions } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const definition = entry.session.getToolDefinition("ask_user_question");
	if (!definition) throw new Error("Tool unavailable: ask_user_question");
	const result = await definition.execute(
		"parity-ask-noui",
		{ questions: [QUESTION] },
		new AbortController().signal,
		undefined,
		{ hasUI: false } as never,
	);
	expect((result.content[0] as { text: string }).text).toContain("UI not available");
	expect(result.details).toMatchObject({ answers: [], cancelled: true, error: "no_ui" });
});

test("the tool emits prompt and blocked events around the wait", async () => {
	const upstreamEvents: unknown[] = [];
	const { dir, sessions, events } = await fixture([rpivAsk, (pi) => {
		for (const type of [ASK_USER_PROMPT_EVENT, ASK_USER_BLOCKED_EVENT]) {
			pi.events.on(type, (payload) => upstreamEvents.push({ type, ...(payload as object) }));
		}
	}]);
	const entry = await sessions.create(dir);
	const pending = findTool(entry, "ask_user_question").execute(
		"parity-ask-events",
		{ questions: [QUESTION] },
		new AbortController().signal,
	);
	const request = await waitForUiRequest(events);
	const prompts = sessionEvents(upstreamEvents, ASK_USER_PROMPT_EVENT);
	expect(prompts).toHaveLength(1);
	expect(prompts[0].questions).toMatchObject([
		{ question: "Which color?", header: "Color", multiSelect: false },
	]);
	expect(prompts[0].questions[0].options).toMatchObject([
		{ label: "Red", description: "Warm", hasPreview: false },
		{ label: "Blue", description: "Cool", hasPreview: false },
	]);
	expect(
		await answer(sessions, entry.session.sessionId, request, request.options[0]),
	).toMatchObject({
		ok: true,
	});
	await pending;
	const blocked = sessionEvents(upstreamEvents, ASK_USER_BLOCKED_EVENT);
	expect(sessionEvents(events, ASK_USER_PROMPT_EVENT)).toEqual([]);
	expect(blocked.map((event) => event.active)).toEqual([true, false]);
});

test("the native tool survives session reload without a capability marker", async () => {
	const { dir, sessions, events } = await fixture([rpivAsk]);
	const entry = await sessions.create(dir);
	const id = entry.session.sessionId;
	const pending = findTool(entry, "ask_user_question").execute(
		"parity-ask-reload",
		{ questions: [QUESTION] },
		new AbortController().signal,
	);
	const request = await waitForUiRequest(events);
	expect(await answer(sessions, id, request, request.options[1])).toMatchObject({ ok: true });
	const result = await pending;
	expect(result.details).toMatchObject({
		answers: [{ kind: "option", answer: "Blue" }],
		cancelled: false,
	});
	const reloaded = new Sessions(dir, [rpivAsk], () => {});
	cleanups.push(() => reloaded.close());
	const loaded = await reloaded.get(id);
	expect(
		loaded.session.getActiveToolNames().filter((name) => name === "ask_user_question"),
	).toHaveLength(1);
	expect(loaded.session.getActiveToolNames()).toContain("ask_user_question");
});
