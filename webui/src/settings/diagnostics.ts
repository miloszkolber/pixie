import type { AgentProfile, DeletionReconciliation, RuntimeDiagnosticsReport } from "@pixie/shared";

export interface DiagnosticsCapabilities {
	compatible: boolean | null;
	missingRequired: string[] | null;
	operations: AgentProfile["operations"] | null;
	capabilities: Record<string, number> | null;
	operationSet: Record<string, boolean> | null;
}

export interface DiagnosticsHost {
	configured: boolean | null;
	reachable: boolean | null;
	applicationReady: boolean | null;
	error: string | null;
	applicationError: string | null;
}

export interface DiagnosticsRuns {
	activeCount: number | null;
}

export interface DiagnosticsDeletionReconciliation {
	count: number | null;
	records: DeletionReconciliation[] | null;
}

export interface DiagnosticsSchedule {
	state: "healthy" | "degraded" | "unknown";
	reason: string | null;
}

export interface DiagnosticsViewModel {
	capabilities: DiagnosticsCapabilities;
	host: DiagnosticsHost;
	runs: DiagnosticsRuns;
	deletionReconciliation: DiagnosticsDeletionReconciliation;
	schedule: DiagnosticsSchedule;
	remediation: string[] | null;
}

export interface SupportSnapshotExportAvailability {
	available: boolean;
	reason: string | null;
}

/**
 * Support exports require explicitly enabled controller authentication. This
 * deliberately does not treat a trusted loopback connection as equivalent.
 */
export function supportSnapshotExportAvailability(
	connected: boolean,
	authenticationEnabled: boolean,
): SupportSnapshotExportAvailability {
	if (!connected)
		return { available: false, reason: "Connect to the controller to export a snapshot." };
	if (!authenticationEnabled) {
		return {
			available: false,
			reason: "Export unavailable: controller authentication is disabled.",
		};
	}
	return { available: true, reason: null };
}

/**
 * Build the diagnostics view model from the typed authenticated response.
 * Missing fields remain unknown: no absent count becomes zero and no absent
 * remediation becomes a "no action" claim.
 */
export function toDiagnosticsViewModel(
	report: RuntimeDiagnosticsReport | null | undefined,
): DiagnosticsViewModel {
	const capabilities = report?.capabilities;
	const host = report?.host;
	const reconciliation = report?.deletionReconciliation;
	const schedule = report?.schedule;
	return {
		capabilities: {
			compatible: booleanOrUnknown(capabilities?.compatible),
			missingRequired: stringArrayOrUnknown(capabilities?.missingRequired),
			operations: agentOperationsOrUnknown(capabilities?.operations),
			capabilities: numberRecordOrUnknown(capabilities?.capabilities),
			operationSet: booleanRecordOrUnknown(capabilities?.operationSet),
		},
		host: {
			configured: booleanOrUnknown(host?.configured),
			reachable: booleanOrUnknown(host?.reachable),
			applicationReady: booleanOrUnknown(host?.applicationReady),
			error: nonemptyStringOrNull(host?.reason),
			applicationError: nonemptyStringOrNull(host?.applicationReason),
		},
		runs: {
			activeCount: nonnegativeCountOrUnknown(report?.runs?.activeCount),
		},
		deletionReconciliation: {
			count: nonnegativeCountOrUnknown(reconciliation?.count),
			records: reconciliationRecordsOrUnknown(reconciliation?.records),
		},
		schedule: {
			state:
				schedule?.state === "healthy" ||
				schedule?.state === "degraded" ||
				schedule?.state === "unknown"
					? schedule.state
					: "unknown",
			reason: nonemptyStringOrNull(schedule?.reason),
		},
		remediation: stringArrayOrUnknown(report?.remediation),
	};
}

/** One-line host health summary for the diagnostics header. */
export function hostHealthSummary(host: DiagnosticsHost): string {
	if (host.configured === null) return "Assistant-host configuration is unknown.";
	if (!host.configured) return "Assistant host is not configured.";
	if (host.reachable === null) return "Assistant-host reachability is unknown.";
	if (!host.reachable) return "Assistant host is unreachable.";
	if (host.applicationReady === null) return "Controller readiness is unknown.";
	if (!host.applicationReady) return "Controller application is not ready.";
	return "Host is reachable and the application is ready.";
}

function booleanOrUnknown(value: unknown): boolean | null {
	return typeof value === "boolean" ? value : null;
}

function nonnegativeCountOrUnknown(value: unknown): number | null {
	return typeof value === "number" && Number.isFinite(value) && value >= 0
		? Math.floor(value)
		: null;
}

function nonemptyStringOrNull(value: unknown): string | null {
	return typeof value === "string" && value !== "" ? value : null;
}

function stringArrayOrUnknown(value: unknown): string[] | null {
	if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) return null;
	return [...value];
}

function numberRecordOrUnknown(value: unknown): Record<string, number> | null {
	if (!isRecord(value)) return null;
	const result: Record<string, number> = {};
	for (const [key, item] of Object.entries(value)) {
		if (typeof item !== "number" || !Number.isFinite(item)) return null;
		result[key] = item;
	}
	return result;
}

function booleanRecordOrUnknown(value: unknown): Record<string, boolean> | null {
	if (!isRecord(value)) return null;
	const result: Record<string, boolean> = {};
	for (const [key, item] of Object.entries(value)) {
		if (typeof item !== "boolean") return null;
		result[key] = item;
	}
	return result;
}

function agentOperationsOrUnknown(value: unknown): AgentProfile["operations"] | null {
	if (!isRecord(value)) return null;
	const {
		deleteSession,
		forkSession,
		promptImage,
		promptEmbeddedContext,
		httpMcp,
		steer,
		renameSession,
		archiveSession,
		administration,
	} = value;
	if (
		typeof deleteSession !== "boolean" ||
		typeof forkSession !== "boolean" ||
		typeof promptImage !== "boolean" ||
		typeof promptEmbeddedContext !== "boolean" ||
		typeof httpMcp !== "boolean" ||
		typeof steer !== "boolean" ||
		typeof renameSession !== "boolean" ||
		typeof archiveSession !== "boolean" ||
		typeof administration !== "boolean"
	) {
		return null;
	}
	return {
		deleteSession,
		forkSession,
		promptImage,
		promptEmbeddedContext,
		httpMcp,
		steer,
		renameSession,
		archiveSession,
		administration,
	};
}

function reconciliationRecordsOrUnknown(value: unknown): DeletionReconciliation[] | null {
	if (!Array.isArray(value)) return null;
	const records: DeletionReconciliation[] = [];
	for (const item of value) {
		if (!isRecord(item)) return null;
		const { projectId, sessionId, phase, reason, remediation, uncertain } = item;
		if (
			typeof projectId !== "string" ||
			typeof sessionId !== "string" ||
			typeof phase !== "string" ||
			typeof reason !== "string" ||
			typeof remediation !== "string" ||
			typeof uncertain !== "boolean"
		) {
			return null;
		}
		records.push({ projectId, sessionId, phase, reason, remediation, uncertain });
	}
	return records;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}
