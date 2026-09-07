import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { type ContentWorkspaceState, createContentWorkspaceState } from "./content-state";
import {
	createProjectWorkspaceState,
	type ProjectWorkspaceState,
	projectSnapshot,
} from "./project-state";
import { createSessionWorkspaceState, type SessionWorkspaceState } from "./session-state";

export interface WorkspaceState
	extends ProjectWorkspaceState,
		ContentWorkspaceState,
		SessionWorkspaceState {}

export { projectSnapshot };

export const createWorkspaceState: StateCreator<AppState, [], [], WorkspaceState> = (...args) => ({
	...createProjectWorkspaceState(...args),
	...createContentWorkspaceState(...args),
	...createSessionWorkspaceState(...args),
});
