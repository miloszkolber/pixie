import { expect, test } from "bun:test";
import type { GitBranchRef, GitCommit, GitHead } from "@pixie/contracts";
import { branchName, diffTabId, scopeKey, scopeLabel } from "@/files/changes/changes-model";
import {
	branchPickerState,
	commitPickerState,
	selectedBranch,
	selectedCommit,
} from "@/files/changes/git-scope-state";

const commit: GitCommit = {
	sha: "a".repeat(40),
	shortSha: "aaaaaaa",
	subject: "An actual commit",
	author: "A contributor",
	committedAt: "2026-08-30T00:00:00Z",
};
const main: GitBranchRef = { ref: "refs/heads/main", name: "main" };
const remoteMain: GitBranchRef = { ref: "refs/remotes/origin/main", name: "origin/main" };
const detached: GitHead = { kind: "detached", oid: "b".repeat(40) };

test("commit selection distinguishes loading, failure, empty history and stale selections", () => {
	expect(commitPickerState(null)).toBe("loading");
	expect(commitPickerState({ error: "Git failed" })).toBe("error");
	expect(commitPickerState({ commits: [] })).toBe("empty");
	expect(commitPickerState({ commits: [commit] })).toBe("ready");
	expect(selectedCommit({ commits: [commit] }, "")).toBeUndefined();
	expect(selectedCommit({ commits: [commit] }, "removed-commit")).toBeUndefined();
	expect(selectedCommit({ commits: [commit] }, commit.sha)).toEqual(commit);
});

test("branch selection distinguishes catalogs, unborn heads, current and stale selections", () => {
	expect(branchPickerState(null, detached)).toBe("loading");
	expect(branchPickerState({ error: "Git failed" }, detached)).toBe("error");
	expect(branchPickerState({ branches: [], truncated: false }, detached)).toBe("empty");
	expect(branchPickerState(null, { kind: "unborn" })).toBe("unborn");
	const catalog = { branches: [main, remoteMain], truncated: true };
	expect(branchPickerState(catalog, detached)).toBe("ready");
	expect(selectedBranch(catalog, { kind: "branch", name: "main" }, main.ref)).toBeUndefined();
	expect(selectedBranch(catalog, detached, "refs/heads/deleted")).toBeUndefined();
	expect(selectedBranch(catalog, detached, remoteMain.ref)).toEqual(remoteMain);
});

test("review targets distinguish scopes, commits, repositories and projects", () => {
	const scope = { kind: "commit", sha: commit.sha } as const;
	const targets = [
		diffTabId("one", "/first", scope, "file.txt"),
		diffTabId("two", "/first", scope, "file.txt"),
		diffTabId("one", "/second", scope, "file.txt"),
		diffTabId("one", "/first", { kind: "commit", sha: "b".repeat(40) }, "file.txt"),
		diffTabId("one", "/first", { kind: "pinned", baseRef: commit.sha }, "file.txt"),
		diffTabId("one", "/first", { kind: "branch", baseRef: main.ref }, "file.txt"),
		diffTabId("one", "/first", { kind: "branch", baseRef: remoteMain.ref }, "file.txt"),
	];
	expect(new Set(targets).size).toBe(targets.length);
	expect(scopeKey(scope)).not.toBe(scopeKey({ kind: "pinned", baseRef: commit.sha }));
	expect(scopeLabel(scope)).toBe("Commit aaaaaaa");
	expect(scopeLabel({ kind: "pinned", baseRef: commit.sha })).toBe("Since aaaaaaa");
	expect(scopeLabel({ kind: "branch", baseRef: remoteMain.ref })).toBe("Changes from origin/main");
	expect(branchName("refs/heads/release\u202eexe.txt")).toBe("releaseexe.txt");
});

test("an open scope menu clears stale repository choices before reloading", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/files/changes/git-scope-menu.svelte", import.meta.url),
	).text();
	expect(source).toMatch(
		/if \(identity !== branchIdentity\) \{[\s\S]*branches = null;[\s\S]*git\.listBranches/,
	);
	expect(source).toMatch(
		/if \(identity !== commitIdentity\) \{[\s\S]*history = null;[\s\S]*git\.listCommits/,
	);
});
