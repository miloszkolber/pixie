import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { clampSplitPercent, SPLIT_DEFAULT } from "@/workspace/split-range";

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
		set((state) => (state.shellLeftOpen === open ? {} : { shellLeftOpen: open })),
	toggleShellLeft: () => set((state) => ({ shellLeftOpen: !state.shellLeftOpen })),
	setShellRightOpen: (open) =>
		set((state) => (state.shellRightOpen === open ? {} : { shellRightOpen: open })),
	toggleShellRight: () => set((state) => ({ shellRightOpen: !state.shellRightOpen })),
	setShellRightView: (view) =>
		set((state) =>
			state.shellRightView === view ? {} : { shellRightView: normalizeRightView(view) },
		),
	setShellSplit: (split) =>
		set((state) => (state.shellSplit === split ? {} : { shellSplit: split })),
	toggleShellSplit: () => set((state) => ({ shellSplit: !state.shellSplit })),
	setShellSplitPercent: (percent) =>
		set((state) => {
			const next = clampSplitPercent(percent);
			return state.shellSplitPercent === next ? {} : { shellSplitPercent: next };
		}),
	hydrateShellLayout: (snapshot) =>
		set(() => ({
			shellLeftOpen: snapshot.shellLeftOpen ?? SHELL_LAYOUT_DEFAULTS.shellLeftOpen,
			shellRightOpen: snapshot.shellRightOpen ?? SHELL_LAYOUT_DEFAULTS.shellRightOpen,
			shellRightView: normalizeRightView(
				snapshot.shellRightView ?? SHELL_LAYOUT_DEFAULTS.shellRightView,
			),
			shellSplit: snapshot.shellSplit ?? SHELL_LAYOUT_DEFAULTS.shellSplit,
			shellSplitPercent:
				snapshot.shellSplitPercent === undefined
					? SHELL_LAYOUT_DEFAULTS.shellSplitPercent
					: clampSplitPercent(snapshot.shellSplitPercent),
		})),
});
