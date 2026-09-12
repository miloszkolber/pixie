import { expect, test } from "bun:test";
import {
	beginCloneFork,
	completeCloneFork,
	findCloneForkMutation,
	rememberCloneForkMutation,
	transitionCloneFork,
} from "../../../assistant/src/history/continuity.ts";

const source = {
	sessionKey: "source-key",
	sessionId: "source-id",
	nativeSessionId: "native-source",
	bootId: "boot-1",
	childGeneration: 2,
} as const;

const target = {
	sessionKey: "child-key",
	sessionId: "child-id",
	nativeSessionId: "native-child",
	bootId: "boot-2",
	childGeneration: 0,
} as const;

test("clone/fork keeps explicit source and child identity and transfers ownership only on success", () => {
	const operation = beginCloneFork({ operationId: "fork-1", kind: "fork", source, target });
	const accepted = completeCloneFork(
		transitionCloneFork(transitionCloneFork(operation, { type: "dispatch" }), { type: "accept" }),
	);
	const sourceAfter = { ...accepted.source };
	expect(accepted).toMatchObject({ status: "completed", owner: "target" });
	expect(accepted.source).toEqual(source);
	expect(accepted.target).toEqual(target);
	expect(sourceAfter).toEqual(source);
});

test("cancellation after dispatch is uncertain and never changes the owner to a guessed child", () => {
	const operation = beginCloneFork({ operationId: "clone-1", kind: "clone", source, target });
	const cancelled = transitionCloneFork(transitionCloneFork(operation, { type: "dispatch" }), {
		type: "cancel.result",
		result: "timed-out",
	});
	expect(cancelled).toMatchObject({
		status: "uncertain",
		cancellation: "timed-out",
		owner: "source",
	});
});

test("accepted clone/fork cancellation keeps a native-accepted result uncertain", () => {
	const operation = beginCloneFork({ operationId: "fork-accepted", kind: "fork", source, target });
	const accepted = transitionCloneFork(
		transitionCloneFork(transitionCloneFork(operation, { type: "dispatch" }), { type: "accept" }),
		{ type: "cancel.result", result: "accepted" },
	);
	expect(accepted).toMatchObject({
		status: "uncertain",
		cancellation: "accepted",
		owner: "source",
	});
});

test("known clone/fork mutation IDs are status lookups rather than replay permission", () => {
	const mutation = {
		mutationId: "m-1",
		operationId: "fork-1",
		kind: "fork" as const,
		status: "completed" as const,
		sourceSessionKey: source.sessionKey,
		targetSessionKey: target.sessionKey,
	};
	const ledger = rememberCloneForkMutation([], mutation, 2);
	expect(findCloneForkMutation(ledger, "m-1")).toEqual(mutation);
	expect(rememberCloneForkMutation(ledger, { ...mutation, status: "uncertain" }, 2)).toBe(ledger);
});
