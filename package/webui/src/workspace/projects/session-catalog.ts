import type { Project, SessionSummary } from "@pixie/contracts";
import { shortSessionAge } from "./session-age";

/**
 * UI-03 session catalog helpers (FC08).
 *
 * Grouped and flat views share one catalog built from the existing
 * `session.list` fixture transport. Sessions without a known project land in
 * an explicit `ungrouped` section; the catalog never invents a hidden
 * all-files project. Archive restore is metadata-only (`session.unarchive`,
 * no clone/fork). Removing a project uses `project.close` and never deletes
 * native chats.
 */

/** Concise per-project recent subset before Show more/Expand. */
export const SESSION_CATALOG_RECENT_LIMIT = 6;

/** Explicit fallback for native titles that are missing or blank. */
export const UNTITLED_SESSION_TITLE = "Untitled chat";

/** Nullable host-side project key. Null means ungrouped; never a hidden all-files project. */
export type HostProjectKey = string | null;

/**
 * Catalog session with a nullable host-side project key plus the optional
 * host association. `SessionSummary` stays assignable: a plain `projectId`
 * string narrows to `string | null` and `hostId` is optional.
 */
export type CatalogSession = Omit<SessionSummary, "projectId"> & {
	projectId: string | null;
	hostId?: string | null;
};

/** True for null/undefined/empty project keys. There is no hidden all-files project. */
export function isUngroupedProjectKey(projectId: unknown): boolean {
	return projectId === null || projectId === undefined || projectId === "";
}

/** Normalize an unknown project reference to a nullable host-side key. */
export function normalizeHostProjectKey(projectId: unknown): HostProjectKey {
	if (projectId === null || projectId === undefined) return null;
	if (typeof projectId !== "string") return null;
	if (projectId === "") return null;
	return projectId;
}

/**
 * Stable host/session association key. Duplicate native session IDs across
 * hosts never collapse by first match.
 */
export function sessionHostKey(session: { hostId?: unknown; sessionId: string }): string {
	const host = typeof session.hostId === "string" ? session.hostId : "";
	return `${host}\0${session.sessionId}`;
}

export type SessionCatalogView = "grouped" | "flat";

export function parseCatalogView(value: unknown): SessionCatalogView {
	return value === "flat" ? "flat" : "grouped";
}

/** Native title as-is with an explicit unresolved fallback. No model call. */
export function displaySessionTitle(title: unknown): string {
	if (typeof title !== "string") return UNTITLED_SESSION_TITLE;
	const trimmed = title.trim();
	return trimmed === "" ? UNTITLED_SESSION_TITLE : trimmed;
}

/** Newest first by native updatedAt with a stable sessionId tie-break. */
export function sortSessionsRecentFirst<T extends Pick<SessionSummary, "sessionId" | "updatedAt">>(
	sessions: readonly T[],
): T[] {
	return [...sessions].sort((left, right) => {
		if (right.updatedAt !== left.updatedAt) return right.updatedAt - left.updatedAt;
		return left.sessionId < right.sessionId ? -1 : left.sessionId > right.sessionId ? 1 : 0;
	});
}

export function filterActiveSessions<T extends Pick<SessionSummary, "archived">>(
	sessions: readonly T[],
): T[] {
	return sessions.filter((session) => session.archived !== true);
}

export function filterArchivedSessions<T extends Pick<SessionSummary, "archived">>(
	sessions: readonly T[],
): T[] {
	return sessions.filter((session) => session.archived === true);
}

export interface RecentSubsetOptions {
	selectedSessionId?: string | null;
	expanded?: boolean;
	limit?: number;
}

export interface RecentSubset<T> {
	visible: T[];
	hiddenCount: number;
}

/**
 * Concise recent subset that never hides the selected or running session
 * behind Show more/Expand. Preserves recent-first order.
 */
export function selectRecentSubset<T extends Pick<SessionSummary, "sessionId" | "isStreaming">>(
	sessions: readonly T[],
	options: RecentSubsetOptions = {},
): RecentSubset<T> {
	const limit = options.limit ?? SESSION_CATALOG_RECENT_LIMIT;
	const expanded = options.expanded ?? false;
	if (expanded || sessions.length <= limit || limit < 0) {
		return { visible: [...sessions], hiddenCount: 0 };
	}
	const selectedSessionId = options.selectedSessionId ?? null;
	const visible = sessions.filter(
		(session, index) =>
			index < limit || session.sessionId === selectedSessionId || session.isStreaming === true,
	);
	return { visible, hiddenCount: sessions.length - visible.length };
}

export interface SessionCatalogGroup {
	project: Project;
	sessions: CatalogSession[];
}

export interface SessionCatalog {
	groups: SessionCatalogGroup[];
	ungrouped: CatalogSession[];
	flat: CatalogSession[];
}

/**
 * Build grouped/flat/ungrouped views from one host/session-keyed catalog.
 *
 * Inputs mirror the existing fixture transport: one `session.list` result per
 * known project plus an optional explicit ungrouped list (empty against the
 * current project-required transport; populated by future host/session-keyed
 * metadata). Sessions carry nullable host-side project keys: null/empty or a
 * `projectId` with no open project lands in `ungrouped` rather than hiding.
 * Duplicate native session IDs across hosts are kept by their host/session
 * key. Archived sessions stay out of the Chats catalog; read them through
 * `filterArchivedSessions`. The catalog never invents a hidden all-files
 * project.
 */
export function buildSessionCatalog(
	projects: readonly Project[],
	sessionsByProject: Readonly<Record<string, readonly CatalogSession[]>>,
	ungroupedSessions: readonly CatalogSession[] = [],
): SessionCatalog {
	const known = new Set(projects.map((project) => project.id));
	const seen = new Set<string>();
	const groupedByProject = new Map<string, CatalogSession[]>();
	const ungroupedById = new Map<string, CatalogSession>();

	const place = (session: CatalogSession): void => {
		const key = sessionHostKey(session);
		if (seen.has(key)) return;
		seen.add(key);
		if (session.archived === true) return;
		const projectKey = (session as { projectId?: unknown }).projectId;
		if (typeof projectKey === "string" && projectKey !== "" && known.has(projectKey)) {
			const list = groupedByProject.get(projectKey) ?? [];
			list.push(session);
			groupedByProject.set(projectKey, list);
			return;
		}
		ungroupedById.set(key, session);
	};

	for (const project of projects) {
		for (const session of sessionsByProject[project.id] ?? []) place(session);
	}
	for (const session of ungroupedSessions) place(session);

	const groups: SessionCatalogGroup[] = projects.map((project) => ({
		project,
		sessions: sortSessionsRecentFirst(groupedByProject.get(project.id) ?? []),
	}));
	const ungrouped = sortSessionsRecentFirst([...ungroupedById.values()]);
	const flat = sortSessionsRecentFirst([
		...groups.flatMap((group) => group.sessions),
		...ungrouped,
	]);
	return { groups, ungrouped, flat };
}

/**
 * Accessible row text beyond color alone: full native title plus running
 * state and short age. Visible rows truncate with CSS; this label keeps the
 * full title available to assistive tech and tooltips.
 */
export function sessionRowAccessibleLabel(
	session: Pick<SessionSummary, "title" | "isStreaming" | "updatedAt">,
	now: number = Date.now(),
): string {
	const title = displaySessionTitle(session.title);
	const age = shortSessionAge(session.updatedAt, now);
	return session.isStreaming === true
		? `${title}, running, updated ${age}`
		: `${title}, updated ${age}`;
}

/** Metadata-only Archive restore request. Never a clone/fork. */
export function buildArchiveRestoreRequest(
	projectId: string,
	sessionId: string,
): { method: "session.unarchive"; params: { projectId: string; sessionId: string } } {
	return { method: "session.unarchive", params: { projectId, sessionId } };
}

export function isMetadataOnlyArchiveRestore(method: string): boolean {
	return method === "session.unarchive";
}

/** Drop the restored session from the archived list; order of the rest is kept. */
export function applyArchiveRestored(
	archived: readonly SessionSummary[],
	sessionId: string,
): SessionSummary[] {
	return archived.filter((session) => session.sessionId !== sessionId);
}

/**
 * Removing a project from Pixie only closes the project. It must never call
 * `session.delete`/`session.archive`; native chats stay intact.
 */
export function buildRemoveProjectRequest(projectId: string): {
	method: "project.close";
	params: { id: string };
} {
	return { method: "project.close", params: { id: projectId } };
}
