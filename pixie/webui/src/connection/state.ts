import type { AgentProfile } from "@pixie/contracts";
import type { AppState } from "../store/app-store";
import type { StateCreator } from "../store/external-store";
import type { ConnectionStatus } from "./transport";

export function isConnectedGeneration(
	state: { status: string; connectionGeneration: number },
	connectionGeneration: number,
): boolean {
	return state.status === "connected" && state.connectionGeneration === connectionGeneration;
}

export interface ConnectionState {
	authenticationEnabled: boolean;
	status: ConnectionStatus;
	connectionGeneration: number;
	welcomeGeneration: number;
	welcomeConnectionGeneration: number;
	protocolVersion: number | null;
	agentProfile: AgentProfile | null;
	setStatus: (status: ConnectionStatus) => void;
	setAuthenticationEnabled: (enabled: boolean) => void;
	replaceAgentProfile: (profile: AgentProfile | null) => void;
}

export const createConnectionState: StateCreator<AppState, [], [], ConnectionState> = (set) => ({
	authenticationEnabled: false,
	status: "connecting",
	connectionGeneration: 0,
	welcomeGeneration: 0,
	welcomeConnectionGeneration: 0,
	protocolVersion: null,
	agentProfile: null,
	setStatus: (status) =>
		set((state) => ({
			status,
			...(status === "connecting" ? { agentProfile: null } : {}),
			connectionGeneration:
				status === "connected" ? state.connectionGeneration + 1 : state.connectionGeneration,
		})),
	setAuthenticationEnabled: (authenticationEnabled) => set({ authenticationEnabled }),
	replaceAgentProfile: (agentProfile) => set({ agentProfile }),
});
