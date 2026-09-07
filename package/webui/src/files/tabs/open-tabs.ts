import type { GitDiffScope } from "@pixie/contracts";
import { errorText, getTransport } from "../../connection";
import { DOUBLE_CLICK_SETTLE_MS, projectRelativePath, tupleKey } from "../../lib";
import {
	appStoreApi,
	type ContentTab,
	selectProjectAreaById,
	selectProjectAreaTick,
	type TabIntent,
} from "../../store";
import { diffTabId, diffTabName, scopeKey } from "../changes/changes-model";
import { isImagePath } from "../tree/file-kind";

function baseName(path: string): string {
	return path.split("/").pop() || path;
}

type ReadContentTab = Extract<ContentTab, { kind: "file" | "diff" }>;

function resourceIdentity(tab: ReadContentTab): string {
	return tab.kind === "diff"
		? tupleKey("diff", tab.projectAreaId, tab.repository, scopeKey(tab.scope), tab.path)
		: tupleKey("file", tab.projectAreaId, tab.root, tab.path);
}

const inFlight = new Map<string, { intent: TabIntent; claimPreview: boolean }>();
const previewEpochByProjectArea = new Map<string, number>();

function previewEpoch(projectAreaId: string, intent: TabIntent): number {
	if (intent !== "preview") return previewEpochByProjectArea.get(projectAreaId) ?? 0;
	const next = (previewEpochByProjectArea.get(projectAreaId) ?? 0) + 1;
	previewEpochByProjectArea.set(projectAreaId, next);
	return next;
}

async function openReadTab<T>(
	projectAreaId: string,
	id: string,
	identity: string,
	intent: TabIntent,
	read: () => Promise<T>,
	build: (payload: T, loadedTick: number) => ContentTab,
): Promise<boolean> {
	const store = appStoreApi.getState();
	if (store.removedProjectAreaIds[projectAreaId]) return false;
	const pending = inFlight.get(id);
	if (pending) {
		pending.claimPreview ||= intent === "preview";
		if (intent === "keep") pending.intent = "keep";
		return false;
	}
	const epoch = previewEpoch(projectAreaId, intent);
	const flight = { intent, claimPreview: intent === "preview", epoch };
	inFlight.set(id, flight);
	try {
		if (intent === "preview")
			await new Promise((resolve) => setTimeout(resolve, DOUBLE_CLICK_SETTLE_MS));
		if (
			flight.intent === "preview" &&
			flight.epoch !== (previewEpochByProjectArea.get(projectAreaId) ?? 0)
		) {
			return false;
		}
		const latest = appStoreApi.getState();
		if (latest.removedProjectAreaIds[projectAreaId]) return false;
		const cached = (latest.tabsByProjectArea[projectAreaId] ?? []).find(
			(tab) => (tab.kind === "file" || tab.kind === "diff") && resourceIdentity(tab) === identity,
		);
		if (cached) {
			latest.openTab(
				cached,
				flight.intent,
				flight.intent === "keep" && flight.claimPreview ? { claimPreview: true } : undefined,
			);
			return true;
		}
		const loadedTick = selectProjectAreaTick(latest, projectAreaId);
		const payload = await read();
		const current = appStoreApi.getState();
		if (
			flight.intent === "preview" &&
			flight.epoch !== (previewEpochByProjectArea.get(projectAreaId) ?? 0)
		) {
			return false;
		}
		const installed = (current.tabsByProjectArea[projectAreaId] ?? []).find(
			(tab) => (tab.kind === "file" || tab.kind === "diff") && resourceIdentity(tab) === identity,
		);
		current.openTab(
			installed ?? build(payload, loadedTick),
			flight.intent,
			flight.intent === "keep" && flight.claimPreview ? { claimPreview: true } : undefined,
		);
		return !appStoreApi.getState().removedProjectAreaIds[projectAreaId];
	} catch (error) {
		if (
			!appStoreApi.getState().removedProjectAreaIds[projectAreaId] &&
			(flight.intent !== "preview" || flight.epoch === previewEpochByProjectArea.get(projectAreaId))
		) {
			appStoreApi.getState().pushToast({
				variant: "error",
				message: errorText(error),
				title: "Couldn't open the file",
			});
		}
		return false;
	} finally {
		inFlight.delete(id);
	}
}

export function openFileInTab(
	projectAreaId: string,
	reported: string,
	intent: TabIntent,
	_requestedNavigation?: unknown,
): Promise<boolean> {
	const projectArea = selectProjectAreaById(appStoreApi.getState(), projectAreaId);
	const selectedRoot = projectArea?.root ?? "";
	const path = projectRelativePath(reported, selectedRoot);
	const id = tupleKey("file", projectAreaId, selectedRoot, path);
	return openReadTab(
		projectAreaId,
		id,
		tupleKey("file", projectAreaId, selectedRoot, path),
		intent,
		() =>
			isImagePath(path)
				? Promise.resolve({ content: "" })
				: getTransport().request("fs.readFile", {
						projectId: projectAreaId,
						path,
					}),
		({ content }, loadedTick) => ({
			kind: "file",
			id,
			projectAreaId,
			root: selectedRoot,
			path,
			name: baseName(path),
			content,
			loadedTick,
		}),
	);
}

export function openDiffInTab(
	projectAreaId: string,
	scope: GitDiffScope,
	path: string,
	intent: TabIntent,
	_requestedNavigation?: unknown,
	repository?: string,
	targetComparison?: string,
): Promise<boolean> {
	const selectedRepository = repository ?? "";
	const canonicalPath = projectRelativePath(path, selectedRepository);
	const id = diffTabId(projectAreaId, selectedRepository, scope, canonicalPath);
	return openReadTab(
		projectAreaId,
		id,
		tupleKey("diff", projectAreaId, selectedRepository, scopeKey(scope), canonicalPath),
		intent,
		() =>
			getTransport().request("git.diffFile", {
				projectId: projectAreaId,
				repository: selectedRepository,
				path: canonicalPath,
				scope,
			}),
		(preview, loadedTick) => {
			const comparison = preview.comparisonId ?? targetComparison ?? "";
			return {
				...preview,
				kind: "diff",
				id,
				projectAreaId,
				repository: selectedRepository,
				path: canonicalPath,
				scope,
				name: diffTabName(scope, canonicalPath),
				loadedTick,
				loadedTarget: comparison,
				...(scope.kind === "branch" ? { targetComparison: comparison } : {}),
			};
		},
	);
}
