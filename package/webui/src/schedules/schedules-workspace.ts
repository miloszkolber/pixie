import type { Schedule } from "@pixie/contracts";
import type { PrimarySelection } from "../workspace/store/selection-state";

export const WORKSPACE_SETTINGS_SECTIONS = [
	"agent",
	"pi",
	"providers",
	"models",
	"tools",
	"extensions",
	"schedules",
	"system",
] as const;

export type WorkspaceSettingsSectionId = (typeof WORKSPACE_SETTINGS_SECTIONS)[number];

function normalizeQuery(query: string): string {
	return query.trim().toLocaleLowerCase();
}

/** Filter schedules by prompt, cron, timezone or id. Empty query returns all jobs. */
export function filterSchedules(jobs: readonly Schedule[], query: string): Schedule[] {
	const normalized = normalizeQuery(query);
	if (!normalized) return [...jobs];
	return jobs.filter((job) =>
		[job.prompt, job.cron, job.timezone, job.id]
			.join("\n")
			.toLocaleLowerCase()
			.includes(normalized),
	);
}

export interface ResolvedScheduleSelection {
	job: Schedule | null;
	requestedId: string | null;
	missing: boolean;
}

/**
 * Resolve the workspace schedule selection without redirecting.
 * A requested id that is absent stays missing so the detail column can render
 * recovery instead of replacing the selection with the first job.
 */
export function resolveScheduleSelection(
	jobs: readonly Schedule[],
	primarySelection: PrimarySelection | undefined,
	projectId: string,
): ResolvedScheduleSelection {
	if (primarySelection?.kind !== "schedule" || primarySelection.projectId !== projectId) {
		return { job: null, requestedId: null, missing: false };
	}
	const requestedId = primarySelection.scheduleId;
	const job = jobs.find((candidate) => candidate.id === requestedId) ?? null;
	return { job, requestedId, missing: job === null };
}

/** Status text reuses the ledger outcome without inventing new states. */
export function scheduleStatusLabel(job: Schedule, running: boolean): string {
	const outcome = running ? "Running" : (job.runs[0]?.status ?? "Not run yet");
	return `${job.paused ? "Paused" : "Enabled"} · ${outcome}`;
}

function isWorkspaceSettingsSectionId(value: unknown): value is WorkspaceSettingsSectionId {
	return (
		typeof value === "string" && (WORKSPACE_SETTINGS_SECTIONS as readonly string[]).includes(value)
	);
}

/**
 * Resolve the Settings section for the primary view.
 * A workspace settings selection wins when it names a known section;
 * otherwise the existing settings fallback (dialog section) is reused.
 */
export function resolveWorkspaceSettingsSection(
	primarySelection: PrimarySelection | undefined,
	fallbackSection: string,
): WorkspaceSettingsSectionId {
	if (
		primarySelection?.kind === "settings" &&
		isWorkspaceSettingsSectionId(primarySelection.sectionId)
	) {
		return primarySelection.sectionId;
	}
	if (isWorkspaceSettingsSectionId(fallbackSection)) return fallbackSection;
	return "system";
}
