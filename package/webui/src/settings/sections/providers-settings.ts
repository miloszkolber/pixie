import type { ProviderAuthKind, ProviderStatus } from "@pixie/contracts";

export const KIND_LABEL: Record<ProviderAuthKind, string> = {
	oauth: "OAuth subscription",
	"api-key": "API key",
	env: "environment",
	other: "configured",
};

export type ProviderReadinessState = "checking" | "ready" | "issue" | "not-ready" | "failed";

export interface ProviderReadinessSnapshot {
	revision: number;
	status: ProviderReadinessState | null;
}

export function invalidateProviderReadiness(
	snapshot: ProviderReadinessSnapshot,
): ProviderReadinessSnapshot {
	return { revision: snapshot.revision + 1, status: null };
}

export function settleProviderReadiness(
	snapshot: ProviderReadinessSnapshot,
	revision: number,
	status: Exclude<ProviderReadinessState, "checking">,
): ProviderReadinessSnapshot {
	return snapshot.revision === revision ? { revision, status } : snapshot;
}

export function readinessStatusText(readiness: ProviderReadinessState | null): string | null {
	if (readiness === "checking") return "Checking configuration…";
	if (readiness === "ready") return "Configured";
	if (readiness === "issue") return "Configuration has an issue";
	if (readiness === "not-ready") return "Not configured";
	if (readiness === "failed") return "Couldn't check configuration.";
	return null;
}

export function modelSummary(provider: ProviderStatus): string {
	if (provider.modelCount === 0) return "No catalogued models";
	if (!provider.configured)
		return `${provider.modelCount} model${provider.modelCount === 1 ? "" : "s"}`;
	return `${provider.availableModelCount} of ${provider.modelCount} models available`;
}

export function providerAvailability(
	provider: ProviderStatus,
	readiness: ProviderReadinessState | null,
) {
	const readinessConfirmed = readiness === "ready" || readiness === "issue";
	const usable = provider.configured && provider.available !== false && readinessConfirmed;
	const qualifier = readinessConfirmed
		? "readiness confirmed"
		: readiness === "not-ready"
			? "not ready"
			: readiness === "failed"
				? "readiness check failed"
				: readiness === "checking"
					? "checking readiness"
					: "readiness not checked";
	return { usable, qualifier };
}
