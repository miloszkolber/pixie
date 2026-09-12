import type {
	PrimaryArea,
	PrimarySelection,
	ResourceContext,
	SecondaryArea,
	SecondarySelection,
} from "../store/selection-state";

export type LegacyNavigationLocation =
	| { kind: "main" }
	| { kind: "project"; projectId: string }
	| { kind: "projectArea"; projectId: string; projectAreaId: string }
	| { kind: "chat"; projectId: string; projectAreaId: string; sessionId: string };

export interface NavigationLocationV2 {
	version: 2;
	primaryArea: PrimaryArea;
	primarySelection: PrimarySelection;
	secondaryArea: SecondaryArea;
	secondarySelection: SecondarySelection;
	projectId: string | null;
	projectAreaId: string | null;
}

export type NavigationLocation = LegacyNavigationLocation | NavigationLocationV2;
export const MAIN_LOCATION: LegacyNavigationLocation = { kind: "main" };

const validId = (value: unknown): value is string =>
	typeof value === "string" &&
	value.length > 0 &&
	value.length <= 512 &&
	!Array.from(value).some((character) => {
		const code = character.codePointAt(0) ?? 0;
		return code < 0x20 || code === 0x7f;
	});
const primaryArea = (value: unknown): value is PrimaryArea =>
	value === "chats" || value === "archive" || value === "schedules" || value === "settings";
const secondaryArea = (value: unknown): value is SecondaryArea =>
	value === "details" ||
	value === "files" ||
	value === "git" ||
	(typeof value === "string" && value.startsWith("module:") && validId(value.slice(7)));

function decode(value: string | undefined): string | null {
	if (!value) return null;
	try {
		const decoded = decodeURIComponent(value);
		return validId(decoded) ? decoded : null;
	} catch {
		return null;
	}
}

function projectOf(selection: PrimarySelection | SecondarySelection): string | null {
	if (!selection) return null;
	if (selection.kind === "module")
		return selection.context.scope === "instance" ? null : (selection.context.projectId ?? null);
	return "projectId" in selection ? (selection.projectId ?? null) : null;
}

function secondaryForUrl(selection: SecondarySelection): SecondarySelection {
	if (!selection) return null;
	if (selection.kind === "file" && validId(selection.projectId) && validId(selection.resourceId))
		return { kind: "file", projectId: selection.projectId, resourceId: selection.resourceId };
	if (
		selection.kind === "diff" &&
		validId(selection.projectId) &&
		validId(selection.resourceId) &&
		validId(selection.reviewId)
	)
		return {
			kind: "diff",
			projectId: selection.projectId,
			resourceId: selection.resourceId,
			reviewId: selection.reviewId,
		};
	if (selection.kind !== "module" || !validId(selection.moduleId) || !validId(selection.resourceId))
		return null;
	const context = selection.context;
	if (context.scope === "instance" && validId(context.instanceId))
		return {
			kind: "module",
			moduleId: selection.moduleId,
			resourceId: selection.resourceId,
			context: { scope: "instance", instanceId: context.instanceId },
		};
	if (context.scope === "project" && validId(context.projectId))
		return {
			kind: "module",
			moduleId: selection.moduleId,
			resourceId: selection.resourceId,
			context: { scope: "project", projectId: context.projectId },
		};
	if (
		context.scope === "session" &&
		validId(context.sessionId) &&
		(context.projectId === undefined || validId(context.projectId))
	)
		return {
			kind: "module",
			moduleId: selection.moduleId,
			resourceId: selection.resourceId,
			context: {
				scope: "session",
				sessionId: context.sessionId,
				...(context.projectId === undefined ? {} : { projectId: context.projectId }),
			},
		};
	return null;
}

function serializeV2(location: NavigationLocationV2): string {
	const area = primaryArea(location.primaryArea) ? location.primaryArea : "chats";
	const primary = location.primarySelection;
	const secondary = secondaryForUrl(location.secondarySelection);
	const secondaryAreaValue = secondaryArea(location.secondaryArea)
		? location.secondaryArea
		: "details";
	const project = validId(location.projectId)
		? location.projectId
		: ([projectOf(primary), projectOf(secondary)].find(validId) ?? null);
	const path = ["", "v2", area];
	const primaryId =
		primary?.kind === "session"
			? primary.sessionId
			: primary?.kind === "schedule"
				? primary.scheduleId
				: primary?.kind === "settings"
					? primary.sectionId
					: null;
	if (validId(primaryId)) path.push(primaryId);
	const params = new URLSearchParams();
	if (project) params.set("project", project);
	// An area without its project cannot be resolved back; omitting it keeps
	// the route parseable instead of falling back to the main location.
	if (project && validId(location.projectAreaId)) params.set("area", location.projectAreaId);
	if (secondaryAreaValue !== "details") params.set("secondaryArea", secondaryAreaValue);
	if (secondary) params.set("secondary", JSON.stringify(secondary));
	const query = params.toString();
	return `#${path.map((part, index) => (index ? encodeURIComponent(part) : part)).join("/")}${query ? `?${query}` : ""}`;
}

export function isNavigationLocationV2(
	location: NavigationLocation,
): location is NavigationLocationV2 {
	return "version" in location && location.version === 2;
}

export function serializeLocation(location: NavigationLocation): string {
	if (isNavigationLocationV2(location)) return serializeV2(location);
	switch (location.kind) {
		case "main":
			return "#/v1";
		case "project":
			return `#/v1/projects/${encodeURIComponent(location.projectId)}`;
		case "projectArea":
			return `#/v1/projects/${encodeURIComponent(location.projectId)}/projectAreas/${encodeURIComponent(location.projectAreaId)}`;
		case "chat":
			return `#/v1/projects/${encodeURIComponent(location.projectId)}/projectAreas/${encodeURIComponent(location.projectAreaId)}/chats/${encodeURIComponent(location.sessionId)}`;
	}
}

const queryKeys = new Set(["project", "area", "secondaryArea", "secondary"]);
function parseParams(raw: string): URLSearchParams | null {
	try {
		const params = new URLSearchParams(raw);
		const seen = new Set<string>();
		for (const [key, value] of params) {
			if (
				!queryKeys.has(key) ||
				seen.has(key) ||
				(key === "secondary" ? value.length === 0 || value.length > 4096 : !validId(value))
			)
				return null;
			seen.add(key);
		}
		return params;
	} catch {
		return null;
	}
}

function contextOf(value: unknown): ResourceContext | null {
	if (!value || typeof value !== "object") return null;
	const context = value as Record<string, unknown>;
	if (context.scope === "instance" && validId(context.instanceId))
		return { scope: "instance", instanceId: context.instanceId };
	if (context.scope === "project" && validId(context.projectId))
		return { scope: "project", projectId: context.projectId };
	if (
		context.scope === "session" &&
		validId(context.sessionId) &&
		(context.projectId === undefined || validId(context.projectId))
	)
		return {
			scope: "session",
			sessionId: context.sessionId,
			...(context.projectId === undefined ? {} : { projectId: context.projectId }),
		};
	return null;
}

function parseSecondary(
	params: URLSearchParams,
): { area: SecondaryArea; selection: SecondarySelection } | null {
	const area = params.get("secondaryArea");
	if (area !== null && !secondaryArea(area)) return null;
	const raw = params.get("secondary");
	if (raw === null) return { area: area ?? "details", selection: null };
	if (raw.length > 4096) return null;
	try {
		const value = JSON.parse(raw) as Record<string, unknown>;
		const kind = value.kind;
		const resourceId = value.resourceId;
		if (!validId(resourceId)) return null;
		if (kind === "file" || kind === "diff") {
			if (!validId(value.projectId)) return null;
			if (kind === "file")
				return area !== null && area !== "files"
					? null
					: { area: "files", selection: { kind, projectId: value.projectId, resourceId } };
			return !validId(value.reviewId) || (area !== null && area !== "git")
				? null
				: {
						area: "git",
						selection: { kind, projectId: value.projectId, resourceId, reviewId: value.reviewId },
					};
		}
		if (
			kind !== "module" ||
			!validId(value.moduleId) ||
			(area !== null && area !== `module:${value.moduleId}`)
		)
			return null;
		const context = contextOf(value.context);
		return context
			? {
					area: `module:${value.moduleId}`,
					selection: { kind, moduleId: value.moduleId, resourceId, context },
				}
			: null;
	} catch {
		return null;
	}
}

function parseV2(raw: string): NavigationLocation | null {
	const split = raw.indexOf("?");
	const path = split < 0 ? raw : raw.slice(0, split);
	const params = parseParams(split < 0 ? "" : raw.slice(split + 1));
	if (!params || raw.includes("#")) return null;
	const segments = path.split("/");
	if (segments.length < 3 || segments.length > 4 || segments[0] !== "" || segments[1] !== "v2")
		return null;
	const area = segments[2] as PrimaryArea;
	if (!primaryArea(area)) return null;
	const id = segments.length === 4 ? decode(segments[3]) : null;
	if (segments.length === 4 && !id) return null;
	const projectId = params.get("project");
	const projectAreaId = params.get("area");
	if (projectAreaId !== null && projectId === null) return null;
	let primarySelection: PrimarySelection = null;
	if (id !== null) {
		if (area === "chats" || area === "archive")
			primarySelection = { kind: "session", sessionId: id, ...(projectId ? { projectId } : {}) };
		else if (area === "schedules") {
			if (!projectId) return null;
			primarySelection = { kind: "schedule", scheduleId: id, projectId };
		} else primarySelection = { kind: "settings", sectionId: id };
	}
	const secondary = parseSecondary(params);
	if (!secondary) return null;
	const secondaryProject = projectOf(secondary.selection);
	if (secondaryProject !== null && projectId !== secondaryProject) return null;
	return {
		version: 2,
		primaryArea: area,
		primarySelection,
		secondaryArea: secondary.area,
		secondarySelection: secondary.selection,
		projectId,
		projectAreaId,
	};
}

export function parseFragment(fragment: string): NavigationLocation {
	const raw = fragment.startsWith("#") ? fragment.slice(1) : fragment;
	if (raw.startsWith("/v2")) return parseV2(raw) ?? MAIN_LOCATION;
	if (raw === "" || raw === "/v1") return MAIN_LOCATION;
	const segments = raw.split("/");
	if (segments[0] !== "" || segments[1] !== "v1" || segments[2] !== "projects")
		return MAIN_LOCATION;
	const projectId = decode(segments[3]);
	if (!projectId) return MAIN_LOCATION;
	if (segments.length === 4) return { kind: "project", projectId };
	if (segments[4] !== "projectAreas") return MAIN_LOCATION;
	const projectAreaId = decode(segments[5]);
	if (!projectAreaId) return MAIN_LOCATION;
	if (segments.length === 6) return { kind: "projectArea", projectId, projectAreaId };
	if (segments[6] !== "chats" || segments.length !== 8) return MAIN_LOCATION;
	const sessionId = decode(segments[7]);
	return sessionId ? { kind: "chat", projectId, projectAreaId, sessionId } : MAIN_LOCATION;
}
