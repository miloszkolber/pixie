import type { GitBranchRef, GitCommit, GitHead } from "@pixie/contracts";

export type CommitHistory = { commits: GitCommit[] } | { error: string } | null;
export type BranchCatalog =
	| { branches: GitBranchRef[]; truncated: boolean }
	| { error: string }
	| null;

export type PickerState = "unborn" | "loading" | "error" | "empty" | "ready";

export function branchPickerState(catalog: BranchCatalog, head: GitHead): PickerState {
	if (head.kind === "unborn") return "unborn";
	if (catalog === null) return "loading";
	if ("error" in catalog) return "error";
	return catalog.branches.length === 0 ? "empty" : "ready";
}

export function commitPickerState(history: CommitHistory): Exclude<PickerState, "unborn"> {
	if (history === null) return "loading";
	if ("error" in history) return "error";
	return history.commits.length === 0 ? "empty" : "ready";
}

export function selectedBranch(
	catalog: BranchCatalog,
	head: GitHead,
	selection: string,
): GitBranchRef | undefined {
	if (!catalog || "error" in catalog) return undefined;
	const currentRef = head.kind === "branch" ? `refs/heads/${head.name}` : null;
	return catalog.branches.find((branch) => branch.ref === selection && branch.ref !== currentRef);
}

export function selectedCommit(history: CommitHistory, selection: string): GitCommit | undefined {
	return history && "commits" in history
		? history.commits.find((commit) => commit.sha === selection)
		: undefined;
}
