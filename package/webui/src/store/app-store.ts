import type { AgentProfile, AppConfig, Project } from "@pixie/contracts";
import { type ChatState, createChatState } from "@/chat/runtime/state";
import { type ConnectionState, createConnectionState } from "@/connection/state";
import { randomId } from "@/lib";
import { createSettingsState, type SettingsState } from "@/settings/state";
import {
	createWorkspaceState,
	projectSnapshot,
	type WorkspaceState,
} from "@/workspace/store/state";
import { createExternalStore, toReadableStore } from "./external-store";

export { SettingsSection } from "@/settings/state";
export {
	EMPTY_RUNTIME,
	reduceSessionEvent,
	type SessionGoalRuntime,
	type SessionRuntime,
} from "../chat/runtime/session-runtime";
export {
	type BrowserPanelViewState,
	type BrowserTab,
	type ChatLocationRequest,
	type ChatTab,
	type ClosedChat,
	type ContentOpenOptions,
	type ContentTab,
	chatTabId,
	type DiffTab,
	type FileTab,
	newBrowserPanelViewState,
	type ProjectArea,
	type ProjectAreaActivity,
	projectArea,
	type RouteChatTarget,
	type TabIntent,
} from "../workspace/store/model";

export interface Toast {
	id: string;
	variant: "error" | "success" | "info";
	message: string;
	title?: string;
}
const MAX_TOASTS = 5;

export interface AppState extends WorkspaceState, ChatState, SettingsState, ConnectionState {
	toasts: Toast[];
	installWelcomeSnapshot: (
		protocolVersion: number,
		projects: Project[],
		recentProjects: Project[],
		config?: AppConfig,
		agentProfile?: AgentProfile,
	) => void;
	pushToast: (toast: Omit<Toast, "id">) => string;
	dismissToast: (id: string) => void;
}

export const appStoreApi = createExternalStore<AppState>((...args) => {
	const [set, get] = args;
	return {
		...createWorkspaceState(...args),
		...createChatState(...args),
		...createSettingsState(...args),
		...createConnectionState(...args),
		toasts: [],
		installWelcomeSnapshot: (protocolVersion, projects, recentProjects, config, agentProfile) =>
			set((state) => ({
				...projectSnapshot(state, projects, recentProjects),
				protocolVersion,
				...(config ? { config } : {}),
				agentProfile: agentProfile ?? null,
				welcomeGeneration: state.welcomeGeneration + 1,
				welcomeConnectionGeneration: state.connectionGeneration,
			})),
		pushToast: (toast) => {
			const twin = get().toasts.find(
				(t) =>
					t.variant === toast.variant && t.title === toast.title && t.message === toast.message,
			);
			if (twin) return twin.id;
			const id = randomId("toast");
			set((s) => ({ toasts: [...s.toasts, { ...toast, id }].slice(-MAX_TOASTS) }));
			return id;
		},
		dismissToast: (id) =>
			set((s) =>
				s.toasts.some((t) => t.id === id) ? { toasts: s.toasts.filter((t) => t.id !== id) } : {},
			),
	};
});

export const appStore = toReadableStore(appStoreApi);

export const toast = {
	error: (message: string, title?: string) =>
		appStoreApi.getState().pushToast({ variant: "error", message, ...(title ? { title } : {}) }),
	success: (message: string, title?: string) =>
		appStoreApi.getState().pushToast({ variant: "success", message, ...(title ? { title } : {}) }),
	info: (message: string, title?: string) =>
		appStoreApi.getState().pushToast({ variant: "info", message, ...(title ? { title } : {}) }),
};
