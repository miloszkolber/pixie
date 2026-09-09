import { expect, test } from "bun:test";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

test("the production workspace uses the mewa layout cascade owner", async () => {
	const [index, mewa, workArea] = await Promise.all([
		source("index.css"),
		source("mewa.css"),
		source("workspace/views/project-work-area.svelte"),
	]);

	expect(mewa.match(/@import "\.\/foundation\/layouts\.css";/g)).toHaveLength(1);
	expect(index).not.toContain("foundation/layouts.css");
	expect(workArea).toContain('class="pixie-shell-grid mewa-layout-probe"');
	expect(workArea).toContain("data-layout={layoutProbe}");
});

test("all five content-filled probe modes map to the existing six-slot shell", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	for (const layout of [
		"split",
		"secondary-focus",
		"primary-context",
		"primary-sidebar",
		"primary-focus",
	]) {
		expect(workArea).toContain(`"${layout}"`);
	}
	for (const slot of [
		"primary-rail",
		"primary-sidebar",
		"primary-view",
		"secondary-view",
		"secondary-sidebar",
		"secondary-rail",
	]) {
		expect(workArea).toContain(`data-slot="${slot}"`);
	}
	expect(workArea.match(/mewa-layout-probe__slot/g)?.length ?? 0).toBeGreaterThanOrEqual(6);
	expect(workArea).toContain("mewa-layout-probe__scroll");
	expect(workArea).toContain('aria-hidden={!primaryViewVisible}');
	expect(workArea).toContain("inert={!primaryViewVisible}");
});

test("layout mode changes stay presentation-only and expose restore", async () => {
	const [layouts, workArea] = await Promise.all([
		source("foundation/layouts.css"),
		source("workspace/views/project-work-area.svelte"),
	]);
	expect(layouts).toContain("Presentation-only");
	expect(layouts).toContain("never stops accepted work");
	expect(workArea).toContain("function restoreLayout()");
	expect(workArea).toContain("selections, drafts, and accepted work stay intact");
	expect(workArea).toContain("dispatchLayout({ leftCollapsed: false, rightCollapsed: false, focus: \"none\" })");
});
