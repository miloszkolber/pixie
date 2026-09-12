import { expect, test } from "bun:test";
import { repositoryDisplayName, scopeLabel } from "@/files/changes/changes-model";
import { rawPreviewNotice } from "@/files/changes/diff-pane-model";

const webuiRoot = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiRoot)).text();
}

test("multi-repository identity prefers the project-relative path", () => {
	expect(
		repositoryDisplayName({
			relativePath: "services/api",
			name: "api",
			root: "/work/services/api",
		}),
	).toBe("services/api");
	expect(repositoryDisplayName({ relativePath: "", name: "api", root: "/work/api" })).toBe("api");
	expect(repositoryDisplayName({ relativePath: "", name: "", root: "/work/only" })).toBe(
		"/work/only",
	);
	expect(scopeLabel({ kind: "uncommitted" })).toBe("Uncommitted");
	expect(scopeLabel({ kind: "branch", baseRef: "refs/heads/main" })).toBe("Changes from main");
});

test("raw conversion notices never masquerade as unavailable previews", () => {
	expect(rawPreviewNotice({})).toBe("");
	expect(
		rawPreviewNotice({
			message: "Showing raw worktree bytes",
		}),
	).toBe("Showing raw worktree bytes");
});

test("Git status keeps raw-conversion warnings visible across repositories", async () => {
	const panel = await source("files/changes/changes-panel.svelte");
	expect(panel).toContain('data-testid="git-repository-select"');
	expect(panel).toContain('data-testid="git-warnings"');
	expect(panel).toContain("repositoryDisplayName(candidate)");
	expect(panel).toContain("visibleWarnings");
});

test("diff previews carry review scope alongside read-only content", async () => {
	const pane = await source("files/changes/diff-pane.svelte");
	expect(pane).toContain('data-testid="diff-scope"');
	expect(pane).toContain("scopeLabel(tab.scope)");
	expect(pane).toContain('data-testid="diff-raw-notice"');
});

test("file and browser previews stay read-only with stable hooks", async () => {
	const filePane = await source("files/tabs/file-pane.svelte");
	expect(filePane).toContain('data-testid="file-pane"');
	expect(filePane).toContain('data-testid="file-binary-notice"');
	expect(filePane).toContain('data-testid="file-image"');
	expect(filePane).toContain("Read-only");

	const browser = await source("workspace/browser/browser-panel.svelte");
	expect(browser).toContain('data-testid="browser-address"');
	expect(browser).toContain('data-testid="browser-snapshot"');
	expect(browser).toContain('data-testid="browser-screenshot"');
	expect(browser).toContain("safeBrowserURL");
	expect(browser).toContain("readonly");
});
