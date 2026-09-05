import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { registerCapability } from "../capabilities.ts";

// Planning is a tool and presentation capability. It does not disable tools,
// intercept commands, modify Pi's prompts, or change execution behavior.
export default function plansExtension(pi: ExtensionAPI): void {
	let entries: unknown[] = [];
	registerCapability(pi, {
		id: "plans",
		version: 1,
		operations: { "plans.read": () => ({ entries }) },
	});
	pi.registerTool({
		name: "update_plan",
		label: "Update plan",
		description: "Record a short plan and update its progress.",
		parameters: Type.Object({
			entries: Type.Array(
				Type.Object({
					content: Type.String(),
					priority: Type.Union([Type.Literal("low"), Type.Literal("medium"), Type.Literal("high")]),
					status: Type.Union([
						Type.Literal("pending"),
						Type.Literal("in_progress"),
						Type.Literal("completed"),
					]),
				}),
				{ maxItems: 100 },
			),
		}),
		execute: async (_id, p) => {
			entries = p.entries;
			pi.appendEntry("pixie-plan", { entries });
			pi.events.emit("pixie:plan", { entries });
			return { content: [{ type: "text", text: "Plan updated." }], details: { plan: { entries } } };
		},
	});
	pi.on("session_start", (_event, ctx) => {
		for (const e of ctx.sessionManager.getBranch())
			if (e.type === "custom" && e.customType === "pixie-plan")
				entries = (e.data as { entries: unknown[] }).entries;
	});
}
