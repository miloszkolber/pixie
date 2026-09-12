import { expect, test } from "bun:test";
import type { Project } from "@pixie/contracts";
import {
	buildSessionCatalog,
	type CatalogSession,
	displaySessionTitle,
	isUngroupedProjectKey,
	normalizeHostProjectKey,
	selectRecentSubset,
	sessionHostKey,
	sessionRowAccessibleLabel,
	sortSessionsRecentFirst,
} from "@/workspace/projects/session-catalog";

const webuiRoot = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiRoot)).text();
}

function project(id: string, name: string): Project {
	return { id, name, roots: [`/${id}`], slug: id, lastOpened: 1 };
}

function session(
	sessionId: string,
	projectId: string | null,
	updatedAt: number,
	overrides: Partial<CatalogSession> = {},
): CatalogSession {
	return {
		sessionId,
		projectId,
		cwd: projectId ? `/${projectId}` : "/tmp/ungrouped",
		title: sessionId,
		model: null,
		thinkingLevel: "off",
		isStreaming: false,
		messageCount: 0,
		updatedAt,
		live: true,
		archived: false,
		...overrides,
	};
}

test("nullable host-side project keys mean ungrouped without a hidden project", () => {
	expect(isUngroupedProjectKey(null)).toBeTrue();
	expect(isUngroupedProjectKey(undefined)).toBeTrue();
	expect(isUngroupedProjectKey("")).toBeTrue();
	expect(isUngroupedProjectKey("alpha")).toBeFalse();
	expect(normalizeHostProjectKey(null)).toBeNull();
	expect(normalizeHostProjectKey(undefined)).toBeNull();
	expect(normalizeHostProjectKey("")).toBeNull();
	expect(normalizeHostProjectKey("alpha")).toBe("alpha");
	expect(normalizeHostProjectKey(42)).toBeNull();
});

test("host/session keys keep duplicate native IDs across hosts", () => {
	expect(sessionHostKey({ sessionId: "dup" })).toBe("\0dup");
	expect(sessionHostKey({ hostId: "host-a", sessionId: "dup" })).toBe("host-a\0dup");
	expect(sessionHostKey({ hostId: "host-b", sessionId: "dup" })).toBe("host-b\0dup");
	expect(sessionHostKey({ hostId: "host-a", sessionId: "dup" })).not.toBe(
		sessionHostKey({ hostId: "host-b", sessionId: "dup" }),
	);
});

test("null and empty project keys land in the ungrouped partition", () => {
	const projects = [project("a", "Alpha")];
	const catalog = buildSessionCatalog(projects, { a: [session("grouped", "a", 30)] }, [
		session("null-key", null, 40),
		session("empty-key", "", 20),
	]);
	expect(catalog.groups[0]?.sessions.map((item) => item.sessionId)).toEqual(["grouped"]);
	expect(catalog.ungrouped.map((item) => item.sessionId)).toEqual(["null-key", "empty-key"]);
	expect(catalog.flat.map((item) => item.sessionId)).toEqual(["null-key", "grouped", "empty-key"]);
});

test("duplicate native session IDs across hosts do not collapse", () => {
	const projects = [project("a", "Alpha")];
	const catalog = buildSessionCatalog(
		projects,
		{ a: [session("dup", "a", 10, { hostId: "host-a" })] },
		[session("dup", "a", 20, { hostId: "host-b" }), session("dup", "a", 5, { hostId: "host-a" })],
	);
	// Same host/session collapses; same native ID on another host is kept.
	expect(catalog.flat).toHaveLength(2);
	expect(catalog.flat.map((item) => `${item.hostId ?? ""}:${item.sessionId}`)).toEqual([
		"host-b:dup",
		"host-a:dup",
	]);
});

test("ungrouped partition stays recent-first and excludes archived chats", () => {
	const projects = [project("a", "Alpha")];
	const catalog = buildSessionCatalog(projects, {}, [
		session("old", null, 10),
		session("new", null, 30),
		session("mid", "gone", 20),
		session("archived", null, 40, { archived: true }),
	]);
	expect(catalog.ungrouped.map((item) => item.sessionId)).toEqual(["new", "mid", "old"]);
	expect(catalog.flat.map((item) => item.sessionId)).toEqual(["new", "mid", "old"]);
});

test("flat/ungrouped view keeps native titles with an explicit fallback", () => {
	expect(displaySessionTitle("  Hello  ")).toBe("Hello");
	expect(displaySessionTitle("Duplicate")).toBe("Duplicate");
	expect(displaySessionTitle("")).toBe("Untitled chat");
	expect(displaySessionTitle(null)).toBe("Untitled chat");
	const now = 100_000_000;
	const label = sessionRowAccessibleLabel(session("s", null, now - 60_000, { title: "  " }), now);
	expect(label).toContain("Untitled chat");
	const running = sessionRowAccessibleLabel(
		session("s", null, now - 60_000, { title: "Hello", isStreaming: true }),
		now,
	);
	expect(running).toContain("running");
});

test("recent subset pins the selected and running ungrouped sessions", () => {
	const items = sortSessionsRecentFirst(
		Array.from({ length: 8 }, (_, index) =>
			session(`u${index}`, null, 100 - index, { isStreaming: index === 7 }),
		),
	);
	const pinned = selectRecentSubset(items, { selectedSessionId: "u7", limit: 6 });
	expect(pinned.visible.map((item) => item.sessionId)).toContain("u7");
	expect(pinned.hiddenCount).toBe(1);
	const runningPinned = selectRecentSubset(items, { selectedSessionId: null, limit: 6 });
	expect(runningPinned.visible.map((item) => item.sessionId)).toContain("u7");
	expect(runningPinned.hiddenCount).toBe(1);
});

test("flat list renders ungrouped metadata without a hidden all-files project", async () => {
	const catalog = await source("workspace/projects/session-catalog.ts");
	for (const contract of [
		"isUngroupedProjectKey",
		"normalizeHostProjectKey",
		"sessionHostKey",
		"HostProjectKey",
		"CatalogSession",
		"hostId",
		"ungrouped",
	]) {
		expect(catalog).toContain(contract);
	}
	expect(catalog).not.toContain('data-project-id="all-files"');
	expect(catalog).not.toContain('id="all-files"');
	expect(catalog).not.toContain("All files");
	const flat = await source("workspace/projects/session-flat-list.svelte");
	for (const contract of [
		"ungroupedSessions",
		"sessionHostKey",
		"buildSessionCatalog",
		"displaySessionTitle",
		"sessionRowAccessibleLabel",
		'data-testid="session-catalog-flat"',
		'data-testid="catalog-flat-row"',
		'data-testid="ungrouped-sessions"',
		"Ungrouped",
		"hidden catch-all project",
		"loader-circle",
		"sr-only",
		"session.list",
	]) {
		expect(flat).toContain(contract);
	}
	expect(flat).not.toContain('data-project-id="all-files"');
	expect(flat).not.toContain("All files");
});
