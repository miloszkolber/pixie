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
 * Activate the primary-area Settings view. The request is recorded first so
 * the shell can render the standalone settings surface when no project area is
 * active (unconfigured/incompatible/disconnected/error or no admitted project).
 * With a project available the ready path is unchanged: ProjectWorkArea owns
 * the Settings area.
 */
export async function openSettingsArea(section?: SettingsSection): Promise<void> {
	if (section) openAreaWithSection(section);
	else openAreaWithoutSection();
	if (appStoreApi.getState().activeProjectAreaId !== null) return;
	const projectId = selectedProjectId();
	if (!projectId) return;
	await enterDefaultProjectArea(projectId);
}
