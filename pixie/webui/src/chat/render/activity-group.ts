import type { ActivityStep } from "../runtime/rows";
import type { ToolRenderProps } from "./tool-registry";

export function summarizeSteps(steps: ActivityStep[]): string {
	const counts = new Map<string, number>();
	for (const step of steps) {
		const name = step.kind === "thinking" ? "thinking" : step.toolName;
		counts.set(name, (counts.get(name) ?? 0) + 1);
	}
	const names = [...counts.entries()].map(([name, count]) =>
		count > 1 ? `${name} ×${count}` : name,
	);
	const shown = names.slice(0, 4).join(", ");
	const more = names.length - 4;
	const count = `${steps.length} ${steps.length === 1 ? "step" : "steps"}`;
	return `${count} · ${shown}${more > 0 ? `, +${more} more` : ""}`;
}

export function activityToolRenderProps(
	step: Extract<ActivityStep, { kind: "tool" }>,
	projectAreaRoot: string | undefined,
): ToolRenderProps {
	return {
		toolCallId: step.toolCallId,
		toolName: step.toolName,
		args: step.args,
		result: step.tool?.raw,
		app: step.tool?.app,
		subagentActivity: step.tool?.subagentActivity,
		status: step.tool?.status ?? (step.dead ? "error" : "running"),
		projectAreaRoot,
		streaming: step.streaming,
	};
}

export function formatActivityChars(count: number): string {
	return count >= 1000 ? `${(count / 1000).toFixed(1)}k` : String(count);
}
