import { appStoreApi, type ProjectArea, projectArea, toast } from "../../store";

export async function enterDefaultProjectArea(projectId: string): Promise<ProjectArea | null> {
	const title = "Couldn't open the project folder";
	const project = appStoreApi.getState().projects.find((candidate) => candidate.id === projectId);
	if (!project || project.roots.length === 0) {
		toast.error("This project has no available directory root.", title);
		return null;
	}
	const def = projectArea(project);
	const store = appStoreApi.getState();
	store.setProjectAreas(projectId, [def]);
	store.activateProjectArea(def);
	return def;
}
