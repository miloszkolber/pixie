import { expect, test } from "bun:test";
import {
	changeNamesResource,
	createRefreshAttemptGate,
	decideLiveTabChange,
	runLiveTabRefresh,
} from "@/files/tabs/use-live-tab-content";

test("matches a changed file only in its originating root", () => {
	const tab = { projectAreaId: "project", root: "/work/one", path: "src/index.ts" };
	expect(changeNamesResource({ root: "/work/one", path: "src/index.ts" }, tab)).toBe(true);
	expect(changeNamesResource({ root: "/work/two", path: "src/index.ts" }, tab)).toBe(false);
});

test("acknowledges unrelated contiguous changes and reloads relevant, truncated or gapped changes", () => {
	const tab = {
		projectAreaId: "project",
		root: "/work/one",
		path: "src/index.ts",
		loadedTick: 2,
	};
	expect(
		decideLiveTabChange(
			{ tick: 3, truncated: false, changes: [{ root: "/work/one", path: "README.md" }] },
			tab,
		),
	).toEqual({ kind: "acknowledge", tick: 3 });
	for (const change of [
		{ tick: 3, truncated: false, changes: [{ root: "/work/one", path: "src/index.ts" }] },
		{ tick: 3, truncated: true, changes: [{ root: "/work/one", path: "README.md" }] },
		{ tick: 4, truncated: false, changes: [{ root: "/work/one", path: "README.md" }] },
	]) {
		expect(decideLiveTabChange(change, tab)).toEqual({ kind: "reload", tick: change.tick });
	}
	expect(decideLiveTabChange(undefined, tab)).toEqual({ kind: "none" });
});

test("matches repository-relative diffs against root-relative watcher paths", () => {
	const tab = {
		projectAreaId: "project",
		repository: "/work/root/packages/app",
		path: "src/index.ts",
	};
	expect(changeNamesResource({ root: "/work/root", path: "packages/app/src/index.ts" }, tab)).toBe(
		true,
	);
});

test("a failed live read remains unconsumed and can be retried", async () => {
	let consumedTick = 2;
	let failure = "";
	let attempts = 0;
	const refresh = (succeed: boolean) =>
		runLiveTabRefresh(
			async () => {
				attempts += 1;
				if (!succeed) throw new Error("read unavailable");
				return { content: "updated", tick: 3 };
			},
			() => true,
			(value) => {
				consumedTick = value.tick;
			},
			(cause) => {
				failure = cause instanceof Error ? cause.message : String(cause);
			},
		);

	expect(await refresh(false)).toBe("failed");
	expect(consumedTick).toBe(2);
	expect(failure).toBe("read unavailable");
	expect(await refresh(true)).toBe("applied");
	expect(consumedTick).toBe(3);
	expect(attempts).toBe(2);
});

test("a failed diff target remains unloaded until an explicit same-target retry succeeds", async () => {
	const attempts = createRefreshAttemptGate();
	let loadedTarget = "comparison-a";
	let reads = 0;
	const refresh = async (target: string, retryRevision: number, succeed: boolean) => {
		if (!attempts.claim(target, retryRevision)) return "suppressed";
		return runLiveTabRefresh(
			async () => {
				reads += 1;
				if (!succeed) throw new Error("diff unavailable");
				return target;
			},
			() => true,
			(target) => {
				loadedTarget = target;
			},
			() => {},
		);
	};

	attempts.reset(loadedTarget, 0);
	expect(await refresh("comparison-b", 0, false)).toBe("failed");
	expect(loadedTarget).toBe("comparison-a");
	expect(await refresh("comparison-b", 0, true)).toBe("suppressed");
	expect(await refresh("comparison-b", 1, true)).toBe("applied");
	expect(loadedTarget).toBe("comparison-b");
	expect(reads).toBe(2);
});
