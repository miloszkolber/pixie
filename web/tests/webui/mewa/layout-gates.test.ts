import { expect, test } from "bun:test";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

const MODES = [
	"split",
	"secondary-focus",
	"primary-context",
	"primary-sidebar",
	"primary-focus",
] as const;
const SLOTS = [
	"primary-rail",
	"primary-sidebar",
	"primary-view",
	"secondary-view",
	"secondary-sidebar",
	"secondary-rail",
] as const;

test("MEWA-03 mounts every mode and theme as a content-filled six-slot fixture", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	for (const mode of MODES) {
		expect(workArea).toContain(`"${mode}"`);
	}
	for (const theme of ["light", "dark"]) {
		expect(workArea).toContain(`"${theme}"`);
	}
	expect(workArea).toContain("?mewa=probes");
	expect(workArea).toContain('data-testid="mewa-layout-probes"');
	expect(workArea).toContain("mutationId=schedule-original");
	expect(workArea).toContain("Draft retained across focus and restore.");
	expect(workArea).toContain("a-very-long-project-name-that-must-not-widen-the-sidebar");
	for (const slot of SLOTS) {
		expect(
			workArea.match(new RegExp(`data-slot=\\"${slot}\\"`, "g"))?.length ?? 0,
		).toBeGreaterThanOrEqual(2);
	}
	expect(workArea).toContain("data-theme={shellTheme}");
	expect(workArea).toContain("data-theme={probeTheme}");
	expect(workArea).toContain("probeThemeClass(probeTheme)");
});

test("MEWA-03 keeps collapsed fixture tracks inert and scrollable", async () => {
	const [layouts, workArea] = await Promise.all([
		source("foundation/layouts.css"),
		source("workspace/views/project-work-area.svelte"),
	]);
	for (const slot of SLOTS) {
		expect(layouts).toContain(`data-slot="${slot}"`);
	}
	expect(workArea).toContain("inert={!probeSlotVisible(probeMode");
	expect(layouts).toContain('[aria-hidden="true"]');
	expect(layouts).toContain("[inert]");
	expect(layouts).toContain("visibility: hidden;");
	expect(layouts).toContain("overflow: auto;");
	expect(layouts).toContain("min-width: 0;");
	expect(layouts).toContain("min-height: 0;");
	expect(layouts).toContain("@media (width < 64rem)");
	expect(layouts).toContain('grid-template-areas: "mobile"');
	expect(layouts).toContain("@media (64rem <= width < 80rem)");
});

test("MEWA-03 uses the pinned Mewa cascade and visible keyboard focus", async () => {
	const [mewa, layouts, workArea] = await Promise.all([
		source("mewa.css"),
		source("foundation/layouts.css"),
		source("workspace/views/project-work-area.svelte"),
	]);
	expect(mewa.match(/@import "\.\/foundation\/layouts\.css";/g)).toHaveLength(1);
	expect(layouts).toContain("var(--border-focus)");
	expect(layouts).toContain(":focus-visible");
	expect(layouts).toContain("position: sticky;");
	expect(layouts).toContain("mewa-layout-probe__composer");
	expect(layouts).toContain("mewa-layout-probe__history");
	expect(workArea).toContain("<ShellResizer");
	expect(workArea).toContain("function restoreLayout()");
	expect(workArea).toContain("selections, drafts, and accepted work stay intact");
});
