import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { clampSplitPercent, SPLIT_DEFAULT } from "@/workspace/split-range";
import {
	bumpWorkspaceNavigationGeneration,
	selectSecondaryArea,
	setLayout as setWorkspaceLayout,
	workspaceReducer,
} from "./selection-state";

export type ShellRightView = "files" | "changes";

export interface ShellLayoutSnapshot {
	shellLeftOpen: boolean;
	shellRightOpen: boolean;
	shellRightView: ShellRightView;
	shellSplit: boolean;
	shellSplitPercent: number;
}

export interface ShellLayoutState extends ShellLayoutSnapshot {
	setShellLeftOpen: (open: boolean) => void;
	toggleShellLeft: () => void;
	setShellRightOpen: (open: boolean) => void;
	toggleShellRight: () => void;
	setShellRightView: (view: ShellRightView) => void;
	setShellSplit: (split: boolean) => void;
	toggleShellSplit: () => void;
	setShellSplitPercent: (percent: number) => void;
	hydrateShellLayout: (snapshot: Partial<ShellLayoutSnapshot>) => void;
}

export const SHELL_LAYOUT_DEFAULTS: ShellLayoutSnapshot = {
	shellLeftOpen: true,
	shellRightOpen: true,
	shellRightView: "files",
	shellSplit: false,
	shellSplitPercent: SPLIT_DEFAULT,
};

function normalizeRightView(view: unknown): ShellRightView {
	return view === "changes" ? "changes" : "files";
}

export const createShellLayoutState: StateCreator<AppState, [], [], ShellLayoutState> = (set) => ({
	...SHELL_LAYOUT_DEFAULTS,
	setShellLeftOpen: (open) =>
		set((state) => {
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				setWorkspaceLayout({ leftCollapsed: !open }),
			);
			return {
				...(state.shellLeftOpen === open ? {} : { shellLeftOpen: open }),
				...(workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection }),
			};
		}),
	toggleShellLeft: () =>
		set((state) => {
			const open = !state.shellLeftOpen;
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				setWorkspaceLayout({ leftCollapsed: !open }),
			);
			return {
				shellLeftOpen: open,
				...(workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection }),
			};
		}),
	setShellRightOpen: (open) =>
		set((state) => {
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				setWorkspaceLayout({ rightCollapsed: !open }),
			);
			return {
				...(state.shellRightOpen === open ? {} : { shellRightOpen: open }),
				...(workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection }),
			};
		}),
	toggleShellRight: () =>
		set((state) => {
			const open = !state.shellRightOpen;
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				setWorkspaceLayout({ rightCollapsed: !open }),
			);
			return {
				shellRightOpen: open,
				...(workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection }),
			};
		}),
	setShellRightView: (view) =>
		set((state) => {
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				selectSecondaryArea(view === "changes" ? "git" : "files"),
			);
			return {
				...(state.shellRightView === view ? {} : { shellRightView: normalizeRightView(view) }),
				...(workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection }),
				...(workspaceSelection === state.workspaceSelection
					? {}
					: { workspaceNavigationGeneration: bumpWorkspaceNavigationGeneration(state) }),
			};
		}),
	setShellSplit: (split) =>
		set((state) => (state.shellSplit === split ? {} : { shellSplit: split })),
	toggleShellSplit: () => set((state) => ({ shellSplit: !state.shellSplit })),
	setShellSplitPercent: (percent) =>
		set((state) => {
			const next = clampSplitPercent(percent);
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				setWorkspaceLayout({ primaryFraction: next / 100 }),
			);
			return {
				...(state.shellSplitPercent === next ? {} : { shellSplitPercent: next }),
				...(workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection }),
			};
		}),
	hydrateShellLayout: (snapshot) =>
		set((state) => {
			const shellLeftOpen = snapshot.shellLeftOpen ?? SHELL_LAYOUT_DEFAULTS.shellLeftOpen;
			const shellRightOpen = snapshot.shellRightOpen ?? SHELL_LAYOUT_DEFAULTS.shellRightOpen;
			const shellRightView = normalizeRightView(
				snapshot.shellRightView ?? SHELL_LAYOUT_DEFAULTS.shellRightView,
			);
			const shellSplit = snapshot.shellSplit ?? SHELL_LAYOUT_DEFAULTS.shellSplit;
			const shellSplitPercent =
				snapshot.shellSplitPercent === undefined
					? SHELL_LAYOUT_DEFAULTS.shellSplitPercent
					: clampSplitPercent(snapshot.shellSplitPercent);
			const workspaceSelection = workspaceReducer(
				workspaceReducer(
					state.workspaceSelection,
					setWorkspaceLayout({
						leftCollapsed: !shellLeftOpen,
						rightCollapsed: !shellRightOpen,
						primaryFraction: shellSplitPercent / 100,
					}),
				),
				selectSecondaryArea(shellRightView === "changes" ? "git" : "files"),
			);
			return {
				shellLeftOpen,
				shellRightOpen,
				shellRightView,
				shellSplit,
				shellSplitPercent,
				...(workspaceSelection === state.workspaceSelection ? {} : { workspaceSelection }),
				...(workspaceSelection === state.workspaceSelection
					? {}
					: { workspaceNavigationGeneration: bumpWorkspaceNavigationGeneration(state) }),
			};
		}),
});
