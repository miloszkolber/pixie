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

export type ShellPrimarySurface = "project-work-area" | "standalone-settings" | "content";

/**
 * Choose the primary surface for the shell. A mounted project work area always
 * wins because it owns the Settings area in the ready state. When no project
 * area is active, a requested Settings view falls back to the standalone
 * settings surface so unconfigured, incompatible, disconnected and error
 * states can still reach provider/system configuration.
 */
export function resolveShellPrimarySurface(
	hasActiveProjectArea: boolean,
	settingsRequested: boolean,
): ShellPrimarySurface {
	if (hasActiveProjectArea) return "project-work-area";
	if (settingsRequested) return "standalone-settings";
	return "content";
}
