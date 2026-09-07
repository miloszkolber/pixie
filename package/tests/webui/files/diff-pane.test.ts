import { afterEach, expect, spyOn, test } from "bun:test";
import type { GitDiffFile } from "@pixie/contracts";
import { initTransport, resetTransport } from "@/connection";
import { WsTransport } from "@/connection/transport";
import { diffIsUnavailable, diffUnavailableNotice } from "@/files/changes/diff-pane-model";
import { simpleUnifiedDiff } from "@/files/changes/line-diff";
import { openDiffInTab } from "@/files/tabs/open-tabs";
import { appStoreApi, type DiffTab } from "@/store";

afterEach(() => {
	resetTransport();
	appStoreApi.setState(appStoreApi.getInitialState(), true);
});

test("initial diff tabs retain unavailable reasons and rename source paths", async () => {
	const location = Object.getOwnPropertyDescriptor(globalThis, "location");
	Object.defineProperty(globalThis, "location", {
		value: new URL("http://localhost:7312"),
		configurable: true,
	});
	const connect = spyOn(WsTransport.prototype, "connect").mockImplementation(() => {});
	const transport = initTransport();
	const preview: GitDiffFile = {
		original: "",
		modified: "",
		originalPath: "old.bin",
		unavailable: true,
		binary: true,
		message: "Binary files cannot be previewed",
	};
	const request = spyOn(transport, "request").mockResolvedValue(preview);
	try {
		expect(
			await openDiffInTab(
				"project",
				{ kind: "uncommitted" },
				"new.bin",
				"keep",
				undefined,
				"/repo",
			),
		).toBe(true);
		expect(request).toHaveBeenCalledWith("git.diffFile", {
			projectId: "project",
			repository: "/repo",
			path: "new.bin",
			scope: { kind: "uncommitted" },
		});
		const tab = appStoreApi.getState().tabsByProjectArea.project?.[0];
		expect(tab).toMatchObject(preview);
		if (tab?.kind !== "diff") throw new Error("diff tab missing");
		expect(diffIsUnavailable(tab)).toBe(true);
		expect(diffUnavailableNotice(tab)).toBe("Binary files cannot be previewed");
		expect(`${tab.originalPath} → ${tab.path}`).toBe("old.bin → new.bin");
	} finally {
		request.mockRestore();
		connect.mockRestore();
		if (location) Object.defineProperty(globalThis, "location", location);
		else Reflect.deleteProperty(globalThis, "location");
	}
});

test("refreshed diff notices replace each other and clear when text becomes available", () => {
	const initial: DiffTab = {
		kind: "diff",
		id: "diff",
		projectAreaId: "project",
		repository: "/repo",
		path: "new.txt",
		name: "new.txt",
		scope: { kind: "uncommitted" },
		loadedTarget: "",
		original: "before\n",
		modified: "after\n",
	};
	appStoreApi.getState().openTab(initial, "keep");
	appStoreApi.setState({ activeProjectAreaId: "project" });
	appStoreApi.getState().setDiffTabIgnoreWhitespace(initial.id, true);
	appStoreApi.getState().updateDiffTabContent("project", initial.id, initial, 1, "");
	expect(appStoreApi.getState().tabsByProjectArea.project?.[0]).toMatchObject({
		ignoreWhitespace: true,
	});
	let tick = 1;
	const refresh = (preview: GitDiffFile): DiffTab => {
		appStoreApi.getState().updateDiffTabContent("project", initial.id, preview, ++tick, "");
		const tab = appStoreApi.getState().tabsByProjectArea.project?.[0];
		if (tab?.kind !== "diff") throw new Error("diff tab missing");
		return tab;
	};
	for (const [preview, notice] of [
		[
			{ original: "", modified: "", unavailable: true, binary: true, originalPath: "old.bin" },
			"Binary files cannot be previewed",
		],
		[
			{ original: "", modified: "", unavailable: true, tooLarge: true },
			"File is too large to preview",
		],
		[
			{ original: "", modified: "", unavailable: true, message: "File does not exist" },
			"File does not exist",
		],
	] satisfies [GitDiffFile, string][]) {
		const tab = refresh(preview);
		expect(diffIsUnavailable(tab)).toBe(true);
		expect(diffUnavailableNotice(tab)).toBe(notice);
	}
	const tab = refresh({ original: "before\n", modified: "after\n", originalPath: "old.txt" });
	expect(diffIsUnavailable(tab)).toBe(false);
	expect(tab.unavailable).toBeUndefined();
	expect(tab.message).toBeUndefined();
	const diff = simpleUnifiedDiff(
		tab.path,
		tab.original,
		tab.modified,
		tab.ignoreWhitespace,
		tab.originalPath,
	);
	expect(diff).toContain("--- a/old.txt");
	expect(diff).toContain("+++ b/new.txt");
});
