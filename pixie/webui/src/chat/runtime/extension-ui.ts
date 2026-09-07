import type { AgentEvent } from "@pixie/contracts";
import { omitKey } from "../../store/record";
import type { SessionRuntime } from "./session-runtime";

export function clearExtensionUi(rt: SessionRuntime): SessionRuntime {
	return {
		...rt,
		extensionStatuses: {},
		extensionWidgets: {},
		extensionTitle: "",
		extensionWorking: "",
	};
}

function keyed<T>(values: Record<string, T>, key: string, value: T | undefined): Record<string, T> {
	if (!key || key.length > 128) return values;
	if (value === undefined) return omitKey(values, key);
	if (!Object.hasOwn(values, key) && Object.keys(values).length >= 16) return values;
	return { ...values, [key]: value };
}

/** Ephemeral text only. These projections never rename a chat or alter its draft. */
export function reduceExtensionUi(rt: SessionRuntime, event: AgentEvent): SessionRuntime {
	switch (event.type) {
		case "ui_status":
			return {
				...rt,
				extensionStatuses: keyed(
					rt.extensionStatuses,
					event.key,
					event.text ? event.text.slice(0, 2000) : undefined,
				),
			};
		case "ui_working":
			return { ...rt, extensionWorking: (event.message ?? "").slice(0, 2000) };
		case "ui_title":
			return { ...rt, extensionTitle: event.title.slice(0, 2000) };
		case "ui_widget":
			return {
				...rt,
				extensionWidgets: keyed(
					rt.extensionWidgets,
					event.key,
					event.lines === undefined
						? undefined
						: {
								lines: event.lines.slice(0, 32).map((line) => line.slice(0, 2000)),
								placement: event.placement === "belowEditor" ? "belowEditor" : "aboveEditor",
								order:
									rt.extensionWidgets[event.key]?.order ??
									Math.max(
										0,
										...Object.values(rt.extensionWidgets).map((widget) => widget.order ?? 0),
									) + 1,
							},
				),
			};
		default:
			return rt;
	}
}
