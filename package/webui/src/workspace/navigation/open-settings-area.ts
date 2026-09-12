import { appStoreApi, type SettingsSection, selectPrimary, selectPrimaryArea } from "../../store";
import { enterDefaultProjectArea } from "./default-project-area";

function openAreaWithoutSection(): void {
	appStoreApi.getState().dispatchWorkspaceSelection(selectPrimaryArea("settings"));
}

function openAreaWithSection(section: SettingsSection): void {
	const state = appStoreApi.getState();
	state.setSettingsSection(section);
	state.dispatchWorkspaceSelection(
		selectPrimary({ kind: "settings", sectionId: section }, "settings"),
	);
}

function selectedProjectId(): string | null {
	const state = appStoreApi.getState();
	const project =
		state.projects.find((item) => item.id === state.selectedProjectId) ?? state.projects[0] ?? null;
	return project?.id ?? null;
}

/**
 * Activate the primary-area Settings view. Settings lives in the project work
 * area, so an unopened project is entered first when one is available; the
 * primary-area view is the only settings surface.
 */
export async function openSettingsArea(section?: SettingsSection): Promise<void> {
	if (appStoreApi.getState().activeProjectAreaId === null) {
		const projectId = selectedProjectId();
		if (!projectId) return;
		const area = await enterDefaultProjectArea(projectId);
		if (!area) return;
	}
	if (section) openAreaWithSection(section);
	else openAreaWithoutSection();
}
