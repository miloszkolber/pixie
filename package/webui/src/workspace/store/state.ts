import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { type ContentWorkspaceState, createContentWorkspaceState } from "./content-state";
import {
	createProjectWorkspaceState,
	type ProjectWorkspaceState,
	projectSnapshot,
} from "./project-state";
import { createWorkspaceSelectionState, type WorkspaceSelectionState } from "./selection-state";
import { createSessionWorkspaceState, type SessionWorkspaceState } from "./session-state";
import { createShellLayoutState, type ShellLayoutState } from "./shell-layout-state";

export interface WorkspaceState
	extends ProjectWorkspaceState,
		ContentWorkspaceState,
		SessionWorkspaceState,
		ShellLayoutState,
		WorkspaceSelectionState {}

export { projectSnapshot };

export const createWorkspaceState: StateCreator<AppState, [], [], WorkspaceState> = (...args) => ({
	...createProjectWorkspaceState(...args),
	...createContentWorkspaceState(...args),
	...createSessionWorkspaceState(...args),
	...createShellLayoutState(...args),
	...createWorkspaceSelectionState(...args),
});
