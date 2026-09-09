import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";

export type PrimaryArea = "chats" | "archive" | "schedules" | "settings";

export type SecondaryArea = "details" | "files" | "git" | `module:${string}`;

export type ResourceContext =
	| { scope: "instance"; instanceId: string }
	| { scope: "project"; projectId: string }
	| { scope: "session"; sessionId: string; projectId?: string };

export type PrimarySelection =
	| { kind: "session"; sessionId: string; projectId?: string }
	| { kind: "schedule"; scheduleId: string; projectId: string }
	| { kind: "settings"; sectionId: string }
	| null;

export type SecondarySelection =
	| { kind: "file"; projectId: string; resourceId: string }
	| { kind: "diff"; projectId: string; resourceId: string; reviewId: string }
	| { kind: "module"; moduleId: string; resourceId: string; context: ResourceContext }
	| null;

export interface WorkspaceLayout {
	leftCollapsed: boolean;
	rightCollapsed: boolean;
	focus: "none" | "primary" | "secondary";
	leftWidth: number;
	rightWidth: number;
	primaryFraction: number;
}

export interface WorkspaceSelectionSnapshot {
	primaryArea: PrimaryArea;
	secondaryArea: SecondaryArea;
	primarySelection: PrimarySelection;
	secondarySelection: SecondarySelection;
	layout: WorkspaceLayout;
}

export const WORKSPACE_SIDE_MIN = 200;
export const WORKSPACE_SIDE_MAX = 400;
export const WORKSPACE_PRIMARY_FRACTION_MIN = 0.2;
export const WORKSPACE_PRIMARY_FRACTION_MAX = 0.8;

export const WORKSPACE_LAYOUT_DEFAULTS: WorkspaceLayout = {
	leftCollapsed: false,
	rightCollapsed: false,
	focus: "none",
	leftWidth: 256,
	rightWidth: 256,
	primaryFraction: 0.5,
};

export function isPrimaryArea(value: unknown): value is PrimaryArea {
	return value === "chats" || value === "archive" || value === "schedules" || value === "settings";
}

function isValidModuleId(value: string): boolean {
	return isValidWorkspaceId(value);
}

export function isValidWorkspaceId(value: unknown): value is string {
	return (
		typeof value === "string" &&
		value.length > 0 &&
		value.length <= 512 &&
		!Array.from(value).some((character) => {
			const code = character.codePointAt(0) ?? 0;
			return code < 0x20 || code === 0x7f;
		})
	);
}

export function isSecondaryArea(value: unknown): value is SecondaryArea {
	return (
		value === "details" ||
		value === "files" ||
		value === "git" ||
		(typeof value === "string" &&
			value.startsWith("module:") &&
			isValidModuleId(value.slice("module:".length)))
	);
}

export function normalizePrimaryArea(value: unknown): PrimaryArea {
	return isPrimaryArea(value) ? value : "chats";
}

export function normalizeSecondaryArea(value: unknown): SecondaryArea {
	return isSecondaryArea(value) ? value : "details";
}

export function normalizeFocus(value: unknown): WorkspaceLayout["focus"] {
	return value === "primary" || value === "secondary" ? value : "none";
}

function clampNumber(value: unknown, min: number, max: number, fallback: number): number {
	if (typeof value !== "number" || Number.isNaN(value)) return fallback;
	return Math.min(max, Math.max(min, value));
}

function normalizedBaseLayout(base: WorkspaceLayout): WorkspaceLayout {
	return {
		leftCollapsed:
			typeof base.leftCollapsed === "boolean"
				? base.leftCollapsed
				: WORKSPACE_LAYOUT_DEFAULTS.leftCollapsed,
		rightCollapsed:
			typeof base.rightCollapsed === "boolean"
				? base.rightCollapsed
				: WORKSPACE_LAYOUT_DEFAULTS.rightCollapsed,
		focus: normalizeFocus(base.focus),
		leftWidth: clampNumber(
			base.leftWidth,
			WORKSPACE_SIDE_MIN,
			WORKSPACE_SIDE_MAX,
			WORKSPACE_LAYOUT_DEFAULTS.leftWidth,
		),
		rightWidth: clampNumber(
			base.rightWidth,
			WORKSPACE_SIDE_MIN,
			WORKSPACE_SIDE_MAX,
			WORKSPACE_LAYOUT_DEFAULTS.rightWidth,
		),
		primaryFraction: clampNumber(
			base.primaryFraction,
			WORKSPACE_PRIMARY_FRACTION_MIN,
			WORKSPACE_PRIMARY_FRACTION_MAX,
			WORKSPACE_LAYOUT_DEFAULTS.primaryFraction,
		),
	};
}

export function normalizeWorkspaceLayout(
	patch: Partial<WorkspaceLayout> | null | undefined = {},
	base: WorkspaceLayout = WORKSPACE_LAYOUT_DEFAULTS,
): WorkspaceLayout {
	const fallback = normalizedBaseLayout(base);
	const values = patch && typeof patch === "object" ? patch : {};
	return {
		leftCollapsed:
			typeof values.leftCollapsed === "boolean" ? values.leftCollapsed : fallback.leftCollapsed,
		rightCollapsed:
			typeof values.rightCollapsed === "boolean" ? values.rightCollapsed : fallback.rightCollapsed,
		focus: values.focus === undefined ? fallback.focus : normalizeFocus(values.focus),
		leftWidth: clampNumber(
			values.leftWidth,
			WORKSPACE_SIDE_MIN,
			WORKSPACE_SIDE_MAX,
			fallback.leftWidth,
		),
		rightWidth: clampNumber(
			values.rightWidth,
			WORKSPACE_SIDE_MIN,
			WORKSPACE_SIDE_MAX,
			fallback.rightWidth,
		),
		primaryFraction: clampNumber(
			values.primaryFraction,
			WORKSPACE_PRIMARY_FRACTION_MIN,
			WORKSPACE_PRIMARY_FRACTION_MAX,
			fallback.primaryFraction,
		),
	};
}

export const clampWorkspaceLayout = normalizeWorkspaceLayout;

export function createInitialWorkspaceState(): WorkspaceSelectionSnapshot {
	return {
		primaryArea: "chats",
		secondaryArea: "details",
		primarySelection: null,
		secondarySelection: null,
		layout: { ...WORKSPACE_LAYOUT_DEFAULTS },
	};
}

export const initialWorkspaceState = createInitialWorkspaceState();
export const INITIAL_WORKSPACE_STATE = initialWorkspaceState;

export type WorkspaceAction =
	| { type: "select-primary-area"; area: PrimaryArea }
	| { type: "select-secondary-area"; area: SecondaryArea }
	| { type: "select-primary"; selection: PrimarySelection; area?: PrimaryArea }
	| { type: "clear-primary" }
	| { type: "select-secondary"; selection: SecondarySelection; area?: SecondaryArea }
	| { type: "clear-secondary" }
	| { type: "set-layout"; layout: Partial<WorkspaceLayout> }
	| { type: "reset-layout" };

export function selectPrimaryArea(area: PrimaryArea): WorkspaceAction {
	return { type: "select-primary-area", area };
}

export function selectSecondaryArea(area: SecondaryArea): WorkspaceAction {
	return { type: "select-secondary-area", area };
}

export function selectPrimary(selection: PrimarySelection, area?: PrimaryArea): WorkspaceAction {
	return area === undefined
		? { type: "select-primary", selection }
		: { type: "select-primary", selection, area };
}

export function clearPrimary(): WorkspaceAction {
	return { type: "clear-primary" };
}

export function selectSecondary(
	selection: SecondarySelection,
	area?: SecondaryArea,
): WorkspaceAction {
	return area === undefined
		? { type: "select-secondary", selection }
		: { type: "select-secondary", selection, area };
}

export function clearSecondary(): WorkspaceAction {
	return { type: "clear-secondary" };
}

export function setLayout(layout: Partial<WorkspaceLayout>): WorkspaceAction {
	return { type: "set-layout", layout };
}

export function resetLayout(): WorkspaceAction {
	return { type: "reset-layout" };
}

export function workspaceSelectionForProject(
	state: WorkspaceSelectionSnapshot,
	projectId: string | null,
): WorkspaceSelectionSnapshot {
	let next = state;
	const primary = next.primarySelection;
	if (
		(primary?.kind === "session" &&
			primary.projectId !== undefined &&
			primary.projectId !== projectId) ||
		(primary?.kind === "schedule" && primary.projectId !== projectId)
	) {
		next = workspaceReducer(next, clearPrimary());
	}
	const secondary = next.secondarySelection;
	const secondaryProject =
		secondary?.kind === "file" || secondary?.kind === "diff"
			? secondary.projectId
			: secondary?.kind === "module" && secondary.context.scope === "project"
				? secondary.context.projectId
				: secondary?.kind === "module" && secondary.context.scope === "session"
					? secondary.context.projectId
					: undefined;
	if (secondaryProject !== undefined && secondaryProject !== projectId) {
		next = workspaceReducer(next, clearSecondary());
	}
	return next;
}

function sameLayout(left: WorkspaceLayout, right: WorkspaceLayout): boolean {
	return (
		left.leftCollapsed === right.leftCollapsed &&
		left.rightCollapsed === right.rightCollapsed &&
		left.focus === right.focus &&
		left.leftWidth === right.leftWidth &&
		left.rightWidth === right.rightWidth &&
		left.primaryFraction === right.primaryFraction
	);
}

export function workspaceReducer(
	state: WorkspaceSelectionSnapshot = createInitialWorkspaceState(),
	action: WorkspaceAction,
): WorkspaceSelectionSnapshot {
	switch (action.type) {
		case "select-primary-area": {
			const primaryArea = normalizePrimaryArea(action.area);
			return state.primaryArea === primaryArea ? state : { ...state, primaryArea };
		}
		case "select-secondary-area": {
			const secondaryArea = normalizeSecondaryArea(action.area);
			return state.secondaryArea === secondaryArea ? state : { ...state, secondaryArea };
		}
		case "select-primary": {
			const primaryArea =
				action.area === undefined ? state.primaryArea : normalizePrimaryArea(action.area);
			return state.primarySelection === action.selection && state.primaryArea === primaryArea
				? state
				: { ...state, primaryArea, primarySelection: action.selection };
		}
		case "clear-primary":
			return state.primarySelection === null ? state : { ...state, primarySelection: null };
		case "select-secondary": {
			const secondaryArea =
				action.area === undefined ? state.secondaryArea : normalizeSecondaryArea(action.area);
			return state.secondarySelection === action.selection && state.secondaryArea === secondaryArea
				? state
				: { ...state, secondaryArea, secondarySelection: action.selection };
		}
		case "clear-secondary":
			return state.secondarySelection === null ? state : { ...state, secondarySelection: null };
		case "set-layout": {
			const layout = normalizeWorkspaceLayout(action.layout, state.layout);
			return sameLayout(state.layout, layout) ? state : { ...state, layout };
		}
		case "reset-layout": {
			const layout = normalizeWorkspaceLayout(WORKSPACE_LAYOUT_DEFAULTS);
			return sameLayout(state.layout, layout) ? state : { ...state, layout };
		}
		default:
			return state;
	}
}

export const selectionReducer = workspaceReducer;

export function sanitizePrimarySelection(value: unknown): PrimarySelection {
	if (!value || typeof value !== "object") return null;
	const candidate = value as Record<string, unknown>;
	if (candidate.kind === "session" && isValidWorkspaceId(candidate.sessionId)) {
		return candidate.projectId === undefined || isValidWorkspaceId(candidate.projectId)
			? {
					kind: "session",
					sessionId: candidate.sessionId,
					...(candidate.projectId === undefined
						? {}
						: { projectId: candidate.projectId as string }),
				}
			: null;
	}
	if (
		candidate.kind === "schedule" &&
		isValidWorkspaceId(candidate.scheduleId) &&
		isValidWorkspaceId(candidate.projectId)
	) {
		return {
			kind: "schedule",
			scheduleId: candidate.scheduleId,
			projectId: candidate.projectId,
		};
	}
	if (candidate.kind === "settings" && isValidWorkspaceId(candidate.sectionId)) {
		return { kind: "settings", sectionId: candidate.sectionId };
	}
	return null;
}

export function sanitizeSecondarySelection(value: unknown): SecondarySelection {
	if (value === null || value === undefined) return null;
	if (!value || typeof value !== "object") return null;
	const candidate = value as Record<string, unknown>;
	if (!isValidWorkspaceId(candidate.resourceId)) return null;
	const resourceId = candidate.resourceId as string;
	if (candidate.kind === "file" && isValidWorkspaceId(candidate.projectId)) {
		return { kind: "file", projectId: candidate.projectId as string, resourceId };
	}
	if (
		candidate.kind === "diff" &&
		isValidWorkspaceId(candidate.projectId) &&
		isValidWorkspaceId(candidate.reviewId)
	) {
		return {
			kind: "diff",
			projectId: candidate.projectId as string,
			resourceId,
			reviewId: candidate.reviewId as string,
		};
	}
	if (candidate.kind === "module" && isValidWorkspaceId(candidate.moduleId)) {
		const context = candidate.context as Record<string, unknown> | undefined;
		if (!context || typeof context !== "object") return null;
		if (context.scope === "instance" && isValidWorkspaceId(context.instanceId)) {
			return {
				kind: "module",
				moduleId: candidate.moduleId as string,
				resourceId,
				context: { scope: "instance", instanceId: context.instanceId as string },
			};
		}
		if (context.scope === "project" && isValidWorkspaceId(context.projectId)) {
			return {
				kind: "module",
				moduleId: candidate.moduleId as string,
				resourceId,
				context: { scope: "project", projectId: context.projectId as string },
			};
		}
		if (
			context.scope === "session" &&
			isValidWorkspaceId(context.sessionId) &&
			(context.projectId === undefined || isValidWorkspaceId(context.projectId))
		) {
			return {
				kind: "module",
				moduleId: candidate.moduleId as string,
				resourceId,
				context: {
					scope: "session",
					sessionId: context.sessionId as string,
					...(context.projectId === undefined
						? {}
						: { projectId: context.projectId as string }),
				},
			};
		}
	}
	return null;
}

/** Drop invalid restored selections while keeping valid layout and areas. */
export function sanitizeWorkspaceSelection(value: unknown): WorkspaceSelectionSnapshot {
	const base = createInitialWorkspaceState();
	if (!value || typeof value !== "object") return base;
	const candidate = value as Record<string, unknown>;
	return {
		primaryArea: normalizePrimaryArea(candidate.primaryArea),
		secondaryArea: normalizeSecondaryArea(candidate.secondaryArea),
		primarySelection: sanitizePrimarySelection(candidate.primarySelection),
		secondarySelection: sanitizeSecondarySelection(candidate.secondarySelection),
		layout: normalizeWorkspaceLayout(
			(candidate.layout ?? {}) as Partial<WorkspaceLayout>,
			base.layout,
		),
	};
}

/**
 * Versioned persisted workspace state (X14/UI-07 slice).
 * v1 only carried layout; v2 carries the full six-slot snapshot.
 * Unknown future versions fail closed to safe defaults.
 */
export const WORKSPACE_PERSIST_VERSION = 2;

export interface PersistedWorkspaceStateV2 {
	version: typeof WORKSPACE_PERSIST_VERSION;
	snapshot: WorkspaceSelectionSnapshot;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null;
}

function layoutFromV1Record(value: Record<string, unknown>): Partial<WorkspaceLayout> {
	const layout: Partial<WorkspaceLayout> = {};
	if (typeof value.leftCollapsed === "boolean") layout.leftCollapsed = value.leftCollapsed;
	if (typeof value.rightCollapsed === "boolean") layout.rightCollapsed = value.rightCollapsed;
	if (value.focus === "none" || value.focus === "primary" || value.focus === "secondary")
		layout.focus = value.focus;
	if (typeof value.leftWidth === "number") layout.leftWidth = value.leftWidth;
	if (typeof value.rightWidth === "number") layout.rightWidth = value.rightWidth;
	if (typeof value.primaryFraction === "number") layout.primaryFraction = value.primaryFraction;
	if (typeof value.shellLeftOpen === "boolean") layout.leftCollapsed = !value.shellLeftOpen;
	if (typeof value.shellRightOpen === "boolean") layout.rightCollapsed = !value.shellRightOpen;
	if (typeof value.shellSplitPercent === "number")
		layout.primaryFraction = value.shellSplitPercent / 100;
	return layout;
}

export function encodeWorkspacePersist(snapshot: WorkspaceSelectionSnapshot): PersistedWorkspaceStateV2 {
	return { version: WORKSPACE_PERSIST_VERSION, snapshot: sanitizeWorkspaceSelection(snapshot) };
}

/**
 * Decode persisted state covering invalid/missing/stale shapes.
 * Returns null when nothing recoverable remains; otherwise a sanitized
 * snapshot with invalid selections dropped but valid layout/areas kept.
 * v1 layout records forward-migrate to a v2 snapshot with empty selections.
 */
export function decodeWorkspacePersist(raw: unknown): WorkspaceSelectionSnapshot | null {
	if (!isRecord(raw)) return null;
	if (raw.version === WORKSPACE_PERSIST_VERSION) {
		if (!isRecord(raw.snapshot)) return null;
		return sanitizeWorkspaceSelection(raw.snapshot);
	}
	if (raw.version === 1 && isRecord(raw.layout)) {
		const base = createInitialWorkspaceState();
		return {
			...base,
			layout: normalizeWorkspaceLayout(layoutFromV1Record(raw.layout), base.layout),
		};
	}
	if (isRecord((raw as Record<string, unknown>).layout) && raw.version === undefined) {
		const base = createInitialWorkspaceState();
		return {
			...base,
			layout: normalizeWorkspaceLayout(
				layoutFromV1Record(raw.layout as Record<string, unknown>),
				base.layout,
			),
		};
	}
	return null;
}

function projectIdOfSelection(
	selection: PrimarySelection | SecondarySelection,
): string | null {
	if (!selection) return null;
	if (selection.kind === "session") return selection.projectId ?? null;
	if (selection.kind === "schedule") return selection.projectId;
	if (selection.kind === "file" || selection.kind === "diff") return selection.projectId;
	if (selection.kind !== "module") return null;
	if (selection.context.scope === "instance") return null;
	return selection.context.projectId ?? null;
}

/**
 * New-server upgrade recovery: drop selections whose project no longer
 * exists while keeping layout, areas, and compatible selections.
 * Instance-scoped modules survive; drafts/runtimes are owned elsewhere.
 */
export function constrainWorkspaceSelectionToProjects(
	snapshot: WorkspaceSelectionSnapshot,
	availableProjectIds: ReadonlySet<string> | readonly string[],
): WorkspaceSelectionSnapshot {
	const available =
		availableProjectIds instanceof Set ? availableProjectIds : new Set(availableProjectIds);
	let next = snapshot;
	const primaryProject = projectIdOfSelection(next.primarySelection);
	if (primaryProject !== null && !available.has(primaryProject)) {
		next = workspaceReducer(next, { type: "clear-primary" });
	}
	const secondaryProject = projectIdOfSelection(next.secondarySelection);
	if (secondaryProject !== null && !available.has(secondaryProject)) {
		next = workspaceReducer(next, { type: "clear-secondary" });
	}
	return next;
}

export interface WorkspaceSelectionState {
	workspaceSelection: WorkspaceSelectionSnapshot;
	/** Monotonic owner token for async navigation and activation guards. */
	workspaceNavigationGeneration: number;
	dispatchWorkspaceSelection: (action: WorkspaceAction) => void;
}

export function bumpWorkspaceNavigationGeneration(
	state: Pick<WorkspaceSelectionState, "workspaceNavigationGeneration">,
): number {
	return state.workspaceNavigationGeneration + 1;
}

export const createWorkspaceSelectionState: StateCreator<
	AppState,
	[],
	[],
	WorkspaceSelectionState
> = (set) => ({
	workspaceSelection: createInitialWorkspaceState(),
	workspaceNavigationGeneration: 0,
	dispatchWorkspaceSelection: (action) =>
		set((state) => {
			const workspaceSelection = workspaceReducer(state.workspaceSelection, action);
			if (workspaceSelection === state.workspaceSelection) return {};
			const changesNavigation = action.type !== "set-layout" && action.type !== "reset-layout";
			return {
				workspaceSelection,
				...(changesNavigation
					? { workspaceNavigationGeneration: bumpWorkspaceNavigationGeneration(state) }
					: {}),
			};
		}),
});
