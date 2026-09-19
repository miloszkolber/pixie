import { expect, test } from "bun:test";
import {
	type SessionCommandSyncContext,
	sessionCommandSyncInputs,
} from "@/chat/session/session-command-sync";
import { appStoreApi } from "@/store";

const context: SessionCommandSyncContext = {
	sessionId: "missing-session",
	projectAreaId: "project-area",
};

test("command synchronization stays inert until a Pi session is connected", () => {
	expect(sessionCommandSyncInputs(appStoreApi.getInitialState(), context)).toEqual({
		sessionReady: false,
		connectedGeneration: 0,
		commandCatalogGeneration: 0,
		piAgent: false,
		skillVersion: 0,
	});
});

test("the framework-neutral controller preserves abort and stale-response guards", async () => {
	const component = new URL(
		"../../../webui/src/chat/session/session-command-sync.ts",
		import.meta.url,
	);
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["']react/);
	expect(source).toContain("appStoreApi.subscribe((state)");
	expect(source).toContain("abort?.abort()");
	expect(source).toContain("requestAbort.signal.aborted");
	expect(source).toContain("sameContext(context, requestedContext)");
	expect(source).toContain("sameInputs(currentInputs, requestedInputs)");
	expect(source).toContain(
		"state.setCommands(requestedContext.sessionId, commands, commandRevision)",
	);
	expect(source).toContain("state.markSkillsSynced(requestedContext.sessionId, syncedTick)");
});
