import { expect, test } from "bun:test";
import type { Project, SessionSummary } from "@pixie/contracts";
import {
	SESSION_CATALOG_RECENT_LIMIT,
	buildSessionCatalog,
	displaySessionTitle,
	parseCatalogView,
	selectRecentSubset,
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
	projectId: string,
	updatedAt: number,
	overrides: Partial<SessionSummary> = {},
): SessionSummary {
	return {
		sessionId,
		projectId,
		cwd: `/${projectId}`,
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

test("native titles keep their text with an explicit unresolved fallback", () => {
	expect(displaySessionTitle("  Hello  ")).toBe("Hello");
	expect(displaySessionTitle("Duplicate")).toBe("Duplicate");
	expect(displaySessionTitle("")).toBe("Untitled chat");
	expect(displaySessionTitle("   ")).toBe("Untitled chat");
	expect(displaySessionTitle(undefined)).toBe("Untitled chat");
	expect(displaySessionTitle(null)).toBe("Untitled chat");
	expect(displaySessionTitle(42)).toBe("Untitled chat");
});

test("catalog sorts newest first with a stable tie-break", () => {
	const items = [session("b", "p", 2), session("a", "p", 3), session("c", "p", 1)];
	expect(sortSessionsRecentFirst(items).map((item) => item.sessionId)).toEqual(["a", "b", "c"]);
	const tied = [session("b", "p", 5), session("a", "p", 5)];
	expect(sortSessionsRecentFirst(tied).map((item) => item.sessionId)).toEqual(["a", "b"]);
	// Input order is not mutated.
	expect(items[0]?.sessionId).toBe("b");
});

test("recent subset keeps the concise limit by default", () => {
	expect(SESSION_CATALOG_RECENT_LIMIT).toBe(6);
	const items = Array.from({ length: 8 }, (_, index) =>
		session(`s${index}`, "p", 100 - index),
	);
	const subset = selectRecentSubset(sortSessionsRecentFirst(items));
	expect(subset.visible.map((item) => item.sessionId)).toEqual([
		"s0",
		"s1",
		"s2",
		"s3",
		"s4",
		"s5",
	]);
	expect(subset.hiddenCount).toBe(2);
});

test("recent expansion never hides the selected or running session", () => {
	const items = sortSessionsRecentFirst(
		Array.from({ length: 8 }, (_, index) =>
			session(`s${index}`, "p", 100 - index, {
				isStreaming: index === 7,
			}),
		),
	);
	const pinned = selectRecentSubset(items, { selectedSessionId: "s7", limit: 6 });
	expect(pinned.visible.map((item) => item.sessionId)).toContain("s7");
	expect(pinned.hiddenCount).toBe(1);

	const runningPinned = selectRecentSubset(items, { selectedSessionId: null, limit: 6 });
	expect(runningPinned.visible.map((item) => item.sessionId)).toContain("s7");
	expect(runningPinned.hiddenCount).toBe(1);

	const expanded = selectRecentSubset(items, { selectedSessionId: "s7", expanded: true });
	expect(expanded.visible).toHaveLength(8);
	expect(expanded.hiddenCount).toBe(0);
});

test("grouped and flat views share one catalog without a hidden all-files project", () => {
	const projects = [project("a", "Alpha"), project("b", "Beta")];
	const catalog = buildSessionCatalog(
		projects,
		{
			a: [session("a1", "a", 30), session("a2", "a", 10)],
			b: [session("b1", "b", 20)],
		},
		[session("u1", "elsewhere", 40)],
	);
	expect(catalog.groups).toHaveLength(2);
	expect(catalog.groups[0]?.project.id).toBe("a");
	expect(catalog.groups[0]?.sessions.map((item) => item.sessionId)).toEqual(["a1", "a2"]);
	expect(catalog.groups[1]?.sessions.map((item) => item.sessionId)).toEqual(["b1"]);
	expect(catalog.ungrouped.map((item) => item.sessionId)).toEqual(["u1"]);
	expect(catalog.flat.map((item) => item.sessionId)).toEqual(["u1", "a1", "b1", "a2"]);
});

test("sessions whose project is not open land in ungrouped instead of hiding", () => {
	const projects = [project("a", "Alpha")];
	const catalog = buildSessionCatalog(projects, {
		a: [session("known", "a", 10), session("stray", "gone", 20)],
	});
	expect(catalog.groups[0]?.sessions.map((item) => item.sessionId)).toEqual(["known"]);
	expect(catalog.ungrouped.map((item) => item.sessionId)).toEqual(["stray"]);
	expect(catalog.flat.map((item) => item.sessionId)).toEqual(["stray", "known"]);
});

test("archived sessions stay out of the chats catalog", () => {
	const projects = [project("a", "Alpha")];
	const catalog = buildSessionCatalog(projects, {
		a: [session("active", "a", 10), session("old", "a", 20, { archived: true })],
	});
	expect(catalog.groups[0]?.sessions.map((item) => item.sessionId)).toEqual(["active"]);
	expect(catalog.flat.map((item) => item.sessionId)).toEqual(["active"]);
});

test("row labels expose running state and age beyond color alone", () => {
	const now = 100_000_000;
	const label = sessionRowAccessibleLabel(session("s", "p", now - 3_600_000, { title: "Hello" }), now);
	expect(label).toContain("Hello");
	expect(label).toContain("1h");
	const running = sessionRowAccessibleLabel(
		session("s", "p", now - 60_000, { title: "Hello", isStreaming: true }),
		now,
	);
	expect(running).toContain("Hello");
	expect(running).toContain("running");
	const fallback = sessionRowAccessibleLabel(session("s", "p", now, { title: "  " }), now);
	expect(fallback).toContain("Untitled chat");
});

test("catalog view parsing defaults to grouped", () => {
	expect(parseCatalogView("flat")).toBe("flat");
	expect(parseCatalogView("grouped")).toBe("grouped");
	expect(parseCatalogView("unknown")).toBe("grouped");
	expect(parseCatalogView(undefined)).toBe("grouped");
});

test("project sessions keep selected and running visibility with native titles", async () => {
	const sessions = await source("workspace/projects/project-sessions.svelte");
	for (const contract of [
		"selectRecentSubset",
		"sortSessionsRecentFirst",
		"displaySessionTitle",
		"sessionRowAccessibleLabel",
		'data-testid="project-session-row"',
		"data-active",
		"bg-control-bg-selected",
		"loader-circle",
		"sr-only",
		"Running",
		"Expand",
		'data-testid="project-sessions-toggle"',
		"aria-label={`Show ${hiddenCount} more chats`}",
		"Show less",
		"session.list",
		"archived: false",
	]) {
		expect(sessions).toContain(contract);
	}
});

test("catalog toggle uses one flat list and never invents a hidden project", async () => {
	const tree = await source("workspace/projects/project-tree.svelte");
	for (const contract of [
		'data-testid="session-catalog-view"',
		'data-testid="catalog-view-grouped"',
		'data-testid="catalog-view-flat"',
		'data-testid="project-expand"',
		"toggleProjectExpanded",
		"expandedProjectIds",
		"SessionFlatList",
		"parseCatalogView",
		"buildRemoveProjectRequest",
		"request.method",
		"pixie-guide",
		'data-testid="project-row"',
	]) {
		expect(tree).toContain(contract);
	}
	expect(tree).not.toContain('data-project-id="all-files"');
	expect(tree).not.toContain('id="all-files"');
	expect(tree).not.toContain("All files");
	const catalog = await source("workspace/projects/session-catalog.ts");
	expect(catalog).toContain('"project.close"');
	expect(catalog).toContain('"session.unarchive"');
	const flat = await source("workspace/projects/session-flat-list.svelte");
	for (const contract of [
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
