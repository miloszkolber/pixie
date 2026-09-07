import { afterEach, expect, test } from "bun:test";
import { BINARY_FILE_NOTICE, filePreviewKind } from "@/files/tabs/file-pane-model";
import { openFileInTab } from "@/files/tabs/open-tabs";
import { isImagePath } from "@/files/tree/file-kind";
import { appStoreApi, projectArea } from "@/store";

afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

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
