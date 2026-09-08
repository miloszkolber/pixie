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

export function isSecondaryArea(value: unknown): value is SecondaryArea {
	return (
		value === "details" ||
		value === "files" ||
		value === "git" ||
		(typeof value === "string" && value.startsWith("module:"))
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

export interface WorkspaceSelectionState {
	workspaceSelection: WorkspaceSelectionSnapshot;
	dispatchWorkspaceSelection: (action: WorkspaceAction) => void;
}

export const createWorkspaceSelectionState: StateCreator<
	AppState,
	[],
	[],
	WorkspaceSelectionState
> = (set) => ({
	workspaceSelection: createInitialWorkspaceState(),
	dispatchWorkspaceSelection: (action) =>
		set((state) => {
			const workspaceSelection = workspaceReducer(state.workspaceSelection, action);
			return workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection };
		}),
});
