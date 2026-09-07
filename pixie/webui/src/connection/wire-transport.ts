import type {
	AgentProfile,
	AppConfig,
	LoginPush,
	Project,
	ProjectFsChangedPayload,
	ServerWelcome,
	SessionDeletedPayload,
	SessionEventPayload,
	SessionGoal,
	SessionLifecycleChangedPayload,
} from "@pixie/contracts";
import { WS_CHANNELS } from "@pixie/contracts";
import { appStoreApi } from "../store";
import { WsTransport } from "./transport";

let transport: WsTransport | null = null;

export function initTransport(): WsTransport {
	if (transport) return transport;

	transport = new WsTransport({
		onStatus: (status) => appStoreApi.getState().setStatus(status),
		onAuthenticationLoss: () => window.dispatchEvent(new Event("pixie-auth-lost")),
		isAuthenticated: async () => {
			try {
				const response = await fetch("/auth/status", {
					credentials: "same-origin",
					cache: "no-store",
				});
				if (!response.ok) return false;
				const status = (await response.json()) as { authenticated?: unknown };
				return status.authenticated === true;
			} catch {
				return true;
			}
		},
	});

	transport.subscribe(WS_CHANNELS.serverWelcome, (data) => {
		const welcome = data as Partial<ServerWelcome>;
		if (typeof welcome.protocolVersion !== "number" || !Array.isArray(welcome.projects)) return;
		appStoreApi
			.getState()
			.installWelcomeSnapshot(
				welcome.protocolVersion,
				welcome.projects,
				Array.isArray(welcome.recentProjects) ? welcome.recentProjects : welcome.projects,
				welcome.config,
				welcome.agentProfile,
			);
	});
	transport.subscribe(WS_CHANNELS.agentProfileChanged, (data) => {
		appStoreApi.getState().replaceAgentProfile(data as AgentProfile);
	});

	transport.subscribe(WS_CHANNELS.projectUpdated, (data) => {
		appStoreApi.getState().applyProjectUpdated(data as Project);
	});

	transport.subscribe(WS_CHANNELS.agentEvent, (data) => {
		const { sessionId, event } = data as SessionEventPayload;
		appStoreApi.getState().handleAgentEvent(event, sessionId);
	});

	transport.subscribe(WS_CHANNELS.sessionDeleted, (data) => {
		const { projectId, sessionId } = data as SessionDeletedPayload;
		appStoreApi.getState().deleteChat(projectId, sessionId, false);
	});
	transport.subscribe(WS_CHANNELS.sessionLifecycleChanged, (data) => {
		appStoreApi.getState().applySessionLifecycle(data as SessionLifecycleChangedPayload);
	});
	transport.subscribe(WS_CHANNELS.sessionObjectiveChanged, (data) => {
		const objective = data as SessionGoal;
		appStoreApi.getState().setSessionGoal(objective.sessionId, objective);
	});

	transport.subscribe(WS_CHANNELS.projectFsChanged, (data) => {
		appStoreApi.getState().noteFsChanged(data as ProjectFsChangedPayload);
	});
	transport.subscribe(WS_CHANNELS.commandCatalogChanged, () => {
		appStoreApi.getState().noteCommandCatalogChanged();
	});

	transport.subscribe(WS_CHANNELS.settingsChanged, (data) => {
		appStoreApi.getState().applyConfig(data as AppConfig);
	});

	transport.subscribe(WS_CHANNELS.providerLogin, (data) => {
		appStoreApi.getState().applyLoginFrame(data as LoginPush);
	});

	transport.connect();
	return transport;
}

export function getTransport(): WsTransport {
	if (!transport) throw new Error("transport not initialized — call initTransport() first");
	return transport;
}

export function resetTransport(): void {
	transport?.stop();
	transport = null;
}
