import type { ProjectFsChange } from "@pixie/contracts";

export type LiveTab = {
	projectAreaId: string;
	path: string;
	root?: string;
	repository?: string;
	loadedTick?: number;
};

function normalizedResource(root: string, path: string): string {
	return `${root.replace(/\/$/, "")}/${path.replace(/^\//, "")}`;
}

function changeNamesPath(change: ProjectFsChange, root: string | undefined, path: string): boolean {
	return (
		root !== undefined &&
		normalizedResource(change.root, change.path) === normalizedResource(root, path)
	);
}

export function changeNamesResource(change: ProjectFsChange, tab: LiveTab): boolean {
	return changeNamesPath(change, tab.root ?? tab.repository, tab.path);
}

export type LiveTabChange = {
	tick: number;
	changes: readonly ProjectFsChange[];
	truncated: boolean;
};

export type LiveTabDecision =
	| { kind: "none" }
	| { kind: "acknowledge"; tick: number }
	| { kind: "reload"; tick: number };

export function decideLiveTabChange(
	change: LiveTabChange | undefined,
	tab: LiveTab,
): LiveTabDecision {
	if (!change || change.tick <= (tab.loadedTick ?? 0)) return { kind: "none" };
	const resourceRoot = tab.root ?? tab.repository;
	const namesOtherFiles =
		change.changes.length > 0 &&
		!change.changes.some((item) => changeNamesPath(item, resourceRoot, tab.path));
	if (change.tick === (tab.loadedTick ?? 0) + 1 && !change.truncated && namesOtherFiles) {
		return { kind: "acknowledge", tick: change.tick };
	}
	return { kind: "reload", tick: change.tick };
}

export type ReadSequencer = { begin: () => () => boolean };

export function createReadSequencer(): ReadSequencer {
	let latest = 0;
	return {
		begin: () => {
			const seq = ++latest;
			return () => seq === latest;
		},
	};
}

export interface RefreshAttemptGate {
	reset: (key?: string, revision?: number) => void;
	claim: (key: string, revision: number) => boolean;
}

export function createRefreshAttemptGate(): RefreshAttemptGate {
	let lastKey: string | undefined;
	let lastRevision = -1;
	return {
		reset: (key, revision = -1) => {
			lastKey = key;
			lastRevision = revision;
		},
		claim: (key, revision) => {
			if (key === lastKey && revision === lastRevision) return false;
			lastKey = key;
			lastRevision = revision;
			return true;
		},
	};
}

export type LiveTabRefreshResult = "applied" | "failed" | "stale";

export async function runLiveTabRefresh<T>(
	read: () => Promise<T>,
	isCurrent: () => boolean,
	onSuccess: (value: T) => void,
	onFailure: (cause: unknown) => void,
): Promise<LiveTabRefreshResult> {
	try {
		const value = await read();
		if (!isCurrent()) return "stale";
		onSuccess(value);
		return "applied";
	} catch (cause) {
		if (!isCurrent()) return "stale";
		onFailure(cause);
		return "failed";
	}
}
