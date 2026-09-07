import type { SessionPlanState } from "@pixie/contracts";

export type PlanEntryStatus = SessionPlanState["entries"][number]["status"];
export type PlanIconStatus = "pending" | "active" | "done";

export function planProgress(planState: SessionPlanState): { completed: number; total: number } {
	return {
		completed: planState.entries.filter((entry) => entry.status === "completed").length,
		total: planState.entries.length,
	};
}

export function planStatusLabel(status: PlanEntryStatus): string {
	if (status === "in_progress") return "In progress";
	if (status === "completed") return "Completed";
	return "Pending";
}

export function planIconStatus(status: PlanEntryStatus): PlanIconStatus {
	if (status === "in_progress") return "active";
	if (status === "completed") return "done";
	return "pending";
}

export function sessionPlanLabel(planState: SessionPlanState): string {
	if (planState.entries.length === 0) return "Session plan, shortened to fit display limits";
	const progress = planProgress(planState);
	return `Session plan, ${progress.completed} of ${progress.total} complete${planState.truncated ? ", shortened to fit display limits" : ""}`;
}
