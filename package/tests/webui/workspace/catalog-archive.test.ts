import { expect, test } from "bun:test";
import {
	applyArchiveRestored,
	buildArchiveRestoreRequest,
	buildRemoveProjectRequest,
	filterActiveSessions,
	filterArchivedSessions,
	isMetadataOnlyArchiveRestore,
} from "@/workspace/projects/session-catalog";
import type { SessionSummary } from "@pixie/contracts";

const webuiRoot = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiRoot)).text();
}

function session(sessionId: string, archived: boolean): SessionSummary {
	return {
		sessionId,
		projectId: "project",
		cwd: "/project",
		title: sessionId,
		model: null,
		thinkingLevel: "off",
		isStreaming: false,
		messageCount: 0,
		updatedAt: 1,
		live: true,
		archived,
	};
}

test("chats and archived sessions filter through the same catalog shape", () => {
	const items = [session("active", false), session("old", true)];
	expect(filterActiveSessions(items).map((item) => item.sessionId)).toEqual(["active"]);
	expect(filterArchivedSessions(items).map((item) => item.sessionId)).toEqual(["old"]);
});

test("archive restore is metadata-only and never a clone", () => {
	const request = buildArchiveRestoreRequest("project", "session-1");
	expect(request).toEqual({
		method: "session.unarchive",
		params: { projectId: "project", sessionId: "session-1" },
	});
	expect(isMetadataOnlyArchiveRestore(request.method)).toBeTrue();
	expect(isMetadataOnlyArchiveRestore("session.fork")).toBeFalse();
	expect(isMetadataOnlyArchiveRestore("session.clone")).toBeFalse();
	expect(isMetadataOnlyArchiveRestore("session.delete")).toBeFalse();
	expect(request.method).not.toContain("fork");
	expect(request.method).not.toContain("clone");
});

test("restoring drops only the restored session from the archived list", () => {
	const archived = [session("keep", true), session("restore-me", true)];
	expect(applyArchiveRestored(archived, "restore-me").map((item) => item.sessionId)).toEqual([
		"keep",
	]);
	expect(archived).toHaveLength(2);
	expect(applyArchiveRestored(archived, "missing")).toHaveLength(2);
});

test("removing a project keeps chats by closing only the project", () => {
	const request = buildRemoveProjectRequest("project-1");
	expect(request).toEqual({ method: "project.close", params: { id: "project-1" } });
	expect(JSON.stringify(request)).not.toContain("sessionId");
	expect(request.method).not.toContain("session.delete");
});

test("archive sidebar restores metadata-only with no clone path", async () => {
	const archive = await source("workspace/projects/archive-list.svelte");
	for (const contract of [
		"buildArchiveRestoreRequest",
		"isMetadataOnlyArchiveRestore",
		"applyArchiveRestored",
		"displaySessionTitle",
		"sessionRowAccessibleLabel",
		"sortSessionsRecentFirst",
		'data-testid="archive-restore"',
		"request.method",
		"session.list",
		"archived: true",
		"never clones it",
		"loader-circle",
		"sr-only",
	]) {
		expect(archive).toContain(contract);
	}
	expect(archive).not.toContain("session.fork");
	expect(archive).not.toContain("session.clone");
	expect(archive).not.toContain("session.delete");
	const catalog = await source("workspace/projects/session-catalog.ts");
	expect(catalog).toContain('"session.unarchive"');
	const history = await source("workspace/projects/project-chat-history.svelte");
	expect(history).toContain("session.unarchive");
	expect(history).toContain("operation: \"unarchived\"");
});

test("work-area archive regions distinguish close, archive, and delete", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	for (const contract of [
		'data-testid="archive-sidebar"',
		'data-testid="archive-detail"',
		"ArchiveList",
		"never clones it",
		"Closing a view is not archiving",
		"Recently closed chats",
		'deleting moves a chat to trash',
		'deleting is separate from both',
	]) {
		expect(workArea).toContain(contract);
	}
	expect(workArea).not.toContain("session.clone");
});

test("project removal never deletes native chats", async () => {
	const tree = await source("workspace/projects/project-tree.svelte");
	expect(tree).toContain("buildRemoveProjectRequest");
	expect(tree).toContain("request.method");
	expect(tree).toContain("native chats are kept");
	expect(tree).not.toContain('"session.delete"');
	expect(tree).not.toContain("'session.delete'");
	const catalog = await source("workspace/projects/session-catalog.ts");
	expect(catalog).toContain('"project.close"');
});
