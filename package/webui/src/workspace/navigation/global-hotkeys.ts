import { hasPlatformModifier } from "../../lib";
import { appStoreApi, selectHistoryTarget } from "../../store";

export interface GlobalHotkeyActions {
	onProjects: () => void;
	onProjectArea?: () => void;
}

export function initGlobalHotkeys(actions: GlobalHotkeyActions): () => void {
	const onKeyDown = (event: KeyboardEvent): void => {
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
		event.preventDefault();
		event.stopPropagation();
		const target = selectHistoryTarget(appStoreApi.getState());
		if (target) appStoreApi.getState().requestHistoryOpen(target);
	};
	window.addEventListener("keydown", onKeyDown, true);
	return () => window.removeEventListener("keydown", onKeyDown, true);
}
