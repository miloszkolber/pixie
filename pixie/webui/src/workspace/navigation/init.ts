import { appStoreApi, projectArea } from "../../store";
import { browserNavigationDriver, type NavigationDriver } from "./driver";
import { startNavigation } from "./restore";

export function initNavigation(driver: NavigationDriver = browserNavigationDriver()): () => void {
	return startNavigation({
		driver,
		listProjectAreas: async (projectId) => {
			const project = appStoreApi
				.getState()
				.projects.find((candidate) => candidate.id === projectId);
			return project ? [projectArea(project)] : [];
		},
	});
}
