import { getTransport } from "../../connection";
import { STORAGE_PREFIX } from "../../constants/branding";
import { appStoreApi } from "../../store";

function storageKey(): string {
	return `${STORAGE_PREFIX}expanded-projects:${getTransport().httpBase()}`;
}

function readPersistedExpansion(): string[] {
	try {
		const raw = localStorage.getItem(storageKey());
		if (!raw) return [];
		const parsed = JSON.parse(raw) as unknown;
		if (!Array.isArray(parsed)) return [];
		return parsed.filter((id): id is string => typeof id === "string");
	} catch {
		return [];
	}
}

function persistExpansion(expanded: Record<string, true>): void {
	try {
		localStorage.setItem(storageKey(), JSON.stringify(Object.keys(expanded)));
	} catch {}
}

export function initProjectExpansionPersistence(): () => void {
	appStoreApi.getState().hydrateExpandedProjects(readPersistedExpansion());
	let previous = appStoreApi.getState().expandedProjectIds;
	return appStoreApi.subscribe((state) => {
		if (state.expandedProjectIds === previous) return;
		previous = state.expandedProjectIds;
		persistExpansion(previous);
	});
}
