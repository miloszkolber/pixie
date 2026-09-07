import type { AppConfig, LoginPush, RefreshedModels, WireModel } from "@pixie/contracts";
import { DEFAULT_CONFIG } from "@pixie/contracts";
import type { AppState } from "../store/app-store";
import type { StateCreator } from "../store/external-store";
import { foldLoginFrame, type LoginState, newLoginState } from "./login/login-state";

export function selectCatalogModel(
	models: readonly WireModel[],
	ref: Pick<WireModel, "provider" | "id"> | null,
): WireModel | null {
	if (!ref) return null;
	return models.find((m) => m.provider === ref.provider && m.id === ref.id) ?? null;
}

export function hiddenModelRevision(config: Pick<AppConfig, "hiddenModels">): string {
	return (config.hiddenModels ?? [])
		.map(({ provider, id }) => `${provider}\0${id}`)
		.sort()
		.join("\u0001");
}

export const SettingsSection = {
	Agent: "agent",
	Pi: "pi",
	Providers: "providers",
	Models: "models",
	Tools: "tools",
	Signet: "signet",
	System: "system",
} as const;
export type SettingsSection = (typeof SettingsSection)[keyof typeof SettingsSection];

export interface SettingsState {
	models: WireModel[];
	providerVersion: number;
	providerConfigured: boolean | null;
	modelsRefreshing: boolean;
	modelsFresh: boolean;
	activeLogin: LoginState | null;
	settingsOpen: boolean;
	settingsSection: SettingsSection;
	config: AppConfig;
	setModelsForProviderVersion: (providerVersion: number, models: WireModel[]) => void;
	noteProviderChanged: () => void;
	setProviderConfigured: (configured: boolean | null) => void;
	beginModelsRefresh: () => number;
	finishModelsRefresh: (providerVersion: number, result: RefreshedModels | null) => void;
	dropModelsFreshness: () => void;
	beginLogin: (loginId: string, providerId: string) => void;
	applyLoginFrame: (push: LoginPush) => void;
	clearLoginInput: () => void;
	clearLogin: () => void;
	openSettings: (section?: SettingsSection) => void;
	closeSettings: () => void;
	setSettingsSection: (section: SettingsSection) => void;
	applyConfig: (config: AppConfig) => void;
}

export const createSettingsState: StateCreator<AppState, [], [], SettingsState> = (set, get) => ({
	models: [],
	providerVersion: 0,
	providerConfigured: null,
	modelsRefreshing: false,
	modelsFresh: false,
	activeLogin: null,
	settingsOpen: false,
	settingsSection: SettingsSection.Providers,
	config: DEFAULT_CONFIG,
	setModelsForProviderVersion: (providerVersion, models) =>
		set((s) => (s.providerVersion === providerVersion ? { models, modelsFresh: false } : s)),
	noteProviderChanged: () =>
		set((s) => ({
			models: [],
			modelsFresh: false,
			modelsRefreshing: false,
			providerVersion: s.providerVersion + 1,
			providerConfigured: null,
		})),
	setProviderConfigured: (providerConfigured) =>
		set((state) =>
			state.providerConfigured === providerConfigured ? state : { providerConfigured },
		),
	beginModelsRefresh: () => {
		const providerVersion = get().providerVersion;
		set({ modelsRefreshing: true });
		return providerVersion;
	},
	dropModelsFreshness: () => set({ modelsFresh: false }),
	finishModelsRefresh: (providerVersion, result) =>
		set((s) =>
			s.providerVersion === providerVersion
				? {
						modelsRefreshing: false,
						models: result?.models ?? s.models,
						modelsFresh: result ? result.complete : s.modelsFresh,
					}
				: s,
		),
	beginLogin: (loginId, providerId) =>
		set((s) =>
			s.activeLogin?.loginId === loginId ? {} : { activeLogin: newLoginState(loginId, providerId) },
		),
	applyLoginFrame: (push) =>
		set((s) => {
			const cur = s.activeLogin;
			if (cur && cur.loginId !== push.loginId && cur.status === "active") return {};
			const base =
				cur && cur.loginId === push.loginId ? cur : newLoginState(push.loginId, push.providerId);
			return { activeLogin: foldLoginFrame(base, push.frame) };
		}),
	clearLoginInput: () =>
		set((s) => {
			if (!s.activeLogin?.input) return {};
			const { input: _drop, ...rest } = s.activeLogin;
			return { activeLogin: rest };
		}),
	clearLogin: () => set({ activeLogin: null }),
	openSettings: (section) => {
		const profile = get().agentProfile;
		set({
			settingsOpen: true,
			settingsSection:
				section ??
				(profile === null
					? SettingsSection.System
					: !profile.pi || !profile.operations.administration
						? SettingsSection.Agent
						: SettingsSection.Providers),
		});
	},
	closeSettings: () => set({ settingsOpen: false }),
	setSettingsSection: (section) => set({ settingsSection: section }),
	applyConfig: (config) => set({ config }),
});
