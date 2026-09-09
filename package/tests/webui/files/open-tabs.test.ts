import { afterEach, expect, spyOn, test } from "bun:test";
import { PROTOCOL_VERSION, type WsResult } from "@pixie/contracts";
import { initTransport, resetTransport } from "@/connection";
import { WsTransport } from "@/connection/transport";
import { BINARY_FILE_NOTICE, filePreviewKind } from "@/files/tabs/file-pane-model";
import { openFileInTab } from "@/files/tabs/open-tabs";
import { isImagePath } from "@/files/tree/file-kind";
import { appStoreApi, projectArea } from "@/store";

afterEach(() => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	resetTransport();
});

const project = {
	id: "files-project",
	name: "Files",
	roots: ["/work/project"],
	slug: "files-project",
	lastOpened: 1,
};

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((resolvePromise) => {
		resolve = resolvePromise;
	});
	return { promise, resolve };
}

function installTransport(): { transport: WsTransport; restore: () => void } {
	const previousLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
	Object.defineProperty(globalThis, "location", {
		value: new URL("http://localhost:7312"),
		configurable: true,
	});
	const connect = spyOn(WsTransport.prototype, "connect").mockImplementation(() => {});
	const transport = initTransport();
	return {
		transport,
		restore: () => {
			resetTransport();
			connect.mockRestore();
			if (previousLocation) Object.defineProperty(globalThis, "location", previousLocation);
			else Reflect.deleteProperty(globalThis, "location");
		},
	};
}

function prepareStore(): void {
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().setStatus("connected");
	appStoreApi.setState({
		projectAreas: { [project.id]: [projectArea(project)] },
		selectedProjectId: project.id,
		activeProjectAreaId: project.id,
	});
}

test("image tabs use the project root without reading binary data through the text transport", async () => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	const project = {
		id: "images",
		name: "Images",
		roots: ["/work/a"],
		slug: "images",
		lastOpened: 1,
	};
	appStoreApi.setState({ projects: [project], projectAreas: { images: [projectArea(project)] } });
	// No transport is initialized: image bytes belong to the authenticated HTTP route.
	expect(await openFileInTab("images", "picture.PNG", "keep")).toBe(true);
	expect(await openFileInTab("images", "picture.PNG", "keep")).toBe(true);
	const tabs = appStoreApi.getState().tabsByProjectArea.images ?? [];
	expect(tabs).toHaveLength(1);
	expect(tabs[0]).toMatchObject({ root: "/work/a", path: "picture.PNG", content: "" });
	for (const path of ["photo.jpeg", "photo.jpg", "animation.gif", "picture.webp"]) {
		expect(isImagePath(path)).toBe(true);
		expect(filePreviewKind(path, "")).toBe("image");
	}
	for (const path of ["vector.svg", "image.png.ts", "README.md"])
		expect(isImagePath(path)).toBe(false);
	appStoreApi.setState({ removedProjectAreaIds: { images: true } });
	expect(await openFileInTab("images", "picture.PNG", "keep")).toBe(false);
});

test("binary text responses get a notice instead of a source-highlighting workload", () => {
	expect(filePreviewKind("archive.zip", "PK\0binary bytes")).toBe("binary");
	expect(BINARY_FILE_NOTICE).toBe("Binary file — text preview is unavailable.");
	expect(filePreviewKind("README.md", "# Read me")).toBe("markdown");
	expect(filePreviewKind("main.ts", "export {};\n")).toBe("source");
});

test("a file reply after project navigation does not replace the new project's secondary selection", async () => {
	prepareStore();
	const other = { ...project, id: "other-files-project", name: "Other", slug: "other-files" };
	appStoreApi.setState({
		projects: [project, other],
		projectAreas: { [project.id]: [projectArea(project)], [other.id]: [projectArea(other)] },
	});
	const { transport, restore } = installTransport();
	const pending = deferred<WsResult<"fs.readFile">>();
	const request = spyOn(transport, "request").mockReturnValue(pending.promise);
	try {
		const opening = openFileInTab(project.id, "src/late.ts", "keep");
		appStoreApi.getState().selectProject(other.id);
		pending.resolve({ content: "late" });
		expect(await opening).toBeFalse();
		expect(appStoreApi.getState().workspaceSelection.secondarySelection).toBeNull();
		expect(appStoreApi.getState().activeProjectAreaId).toBeNull();
		expect(request).toHaveBeenCalledWith("fs.readFile", {
			projectId: project.id,
			path: "src/late.ts",
		});
	} finally {
		request.mockRestore();
		restore();
	}
});

test("a file reply after a same-project selection switch stays inactive", async () => {
	prepareStore();
	const { transport, restore } = installTransport();
	const pending = deferred<WsResult<"fs.readFile">>();
	const request = spyOn(transport, "request").mockReturnValue(pending.promise);
	try {
		const opening = openFileInTab(project.id, "src/late.ts", "keep");
		appStoreApi.getState().openTab(
			{
				kind: "file",
				id: "newer-file",
				projectAreaId: project.id,
				root: "/work/project",
				name: "newer.ts",
				path: "src/newer.ts",
				content: "newer",
			},
			"keep",
		);
		pending.resolve({ content: "late" });
		expect(await opening).toBeFalse();
		expect(appStoreApi.getState().workspaceSelection.secondarySelection).toEqual({
			kind: "file",
			projectId: project.id,
			resourceId: "newer-file",
		});
		expect(appStoreApi.getState().activeTabByProjectArea[project.id]).toBe("newer-file");
	} finally {
		request.mockRestore();
		restore();
	}
});
