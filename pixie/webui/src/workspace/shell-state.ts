import type { AgentProfile, ProviderStatusReport } from "@pixie/contracts";
import type { ConnectionStatus } from "../connection";

export type ShellAvailability =
	| "loading"
	| "ready"
	| "unconfigured"
	| "incompatible"
	| "disconnected"
	| "error";

export function resolveShellAvailability(
	status: ConnectionStatus,
	agentProfile: AgentProfile | null,
	providerConfigured: boolean | null,
	providerError: boolean,
): ShellAvailability {
	if (status !== "connected") return status === "disconnected" ? "disconnected" : "loading";
	if (!agentProfile) return providerError ? "error" : "loading";
	if (!agentProfile.compatible) return "incompatible";
	if (!agentProfile.operations.administration) return "ready";
	if (providerError) return "error";
	if (providerConfigured === null) return "loading";
	return providerConfigured ? "ready" : "unconfigured";
}

export function hasConfiguredProvider(report: ProviderStatusReport): boolean {
	return report.providers.some((provider) => provider.configured);
}
