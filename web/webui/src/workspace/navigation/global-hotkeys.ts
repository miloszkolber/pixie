import { hasPlatformModifier } from "../../lib";
import { appStoreApi, selectHistoryTarget } from "../../store";

export interface GlobalHotkeyActions {
	onProjects: () => void;
	onProjectArea?: () => void;
}

export function initGlobalHotkeys(actions: GlobalHotkeyActions): () => void {
	const onKeyDown = (event: KeyboardEvent): void => {
		if (event.isComposing) return;
		const target = event.target;
		const inEditable =
			target instanceof HTMLElement &&
			(target.isContentEditable ||
				target instanceof HTMLInputElement ||
				target instanceof HTMLTextAreaElement ||
				target instanceof HTMLSelectElement);
		if (inEditable) return;
		const isPanelCommand =
			!event.altKey &&
			!event.shiftKey &&
			hasPlatformModifier(event) &&
			(event.code === "KeyB" || event.code === "KeyJ");
		if (isPanelCommand) {
			event.preventDefault();
			event.stopPropagation();
			if (!event.repeat) {
				if (event.code === "KeyB") actions.onProjects();
				else actions.onProjectArea?.();
			}
			return;
		}

		if (
			event.code !== "KeyR" ||
			!event.ctrlKey ||
			event.metaKey ||
			event.altKey ||
			event.shiftKey
		) {
			return;
		}
		const historyTarget = selectHistoryTarget(appStoreApi.getState());
		if (!historyTarget) return;
		event.preventDefault();
		event.stopPropagation();
		appStoreApi.getState().requestHistoryOpen(historyTarget);
	};
	window.addEventListener("keydown", onKeyDown, true);
	return () => window.removeEventListener("keydown", onKeyDown, true);
}
