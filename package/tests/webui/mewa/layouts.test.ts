import { expect, test } from "bun:test";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

const LAYOUTS = [
	"split",
	"secondary-focus",
	"primary-context",
	"primary-sidebar",
	"primary-focus",
] as const;

test("mewa-03 probes cover all five content-filled desktop modes", async () => {
	const layouts = await source("foundation/layouts.css");
	for (const layout of LAYOUTS) {
		expect(layouts).toContain(`[data-layout="${layout}"]`);
	}
	// Probes are content-filled fixtures: scroll regions, anchored composer
	// and header, and truncating long names exist alongside the grid.
	for (const contract of [
		".mewa-layout-probe",
		".mewa-layout-probe__slot",
		".mewa-layout-probe__scroll",
		".mewa-layout-probe__header",
		".mewa-layout-probe__composer",
		".mewa-layout-probe__long-name",
		".mewa-layout-probe__history",
		"grid-template-columns:",
		"grid-template-areas:",
	]) {
		expect(layouts).toContain(contract);
	}
	// Six stable slots stay named in the probe areas.
	expect(layouts).toContain("primary-rail");
	expect(layouts).toContain("primary-sidebar");
	expect(layouts).toContain("primary-view");
	expect(layouts).toContain("secondary-view");
	expect(layouts).toContain("secondary-sidebar");
	expect(layouts).toContain("secondary-rail");
});

test("mewa-03 probes share geometry in light and dark without a second visual system", async () => {
	const layouts = await source("foundation/layouts.css");
	expect(layouts).toContain('[data-theme="light"]');
	expect(layouts).toContain('[data-theme="dark"]');
	expect(layouts).toContain("color-scheme: light");
	expect(layouts).toContain("color-scheme: dark");
	// Roles are read, never redefined. Definitions carry a colon; reads use var().
	expect(layouts).toContain("var(--border-secondary)");
	expect(layouts).toContain("var(--border-focus)");
	expect(layouts).not.toMatch(/--color-[a-z0-9-]+\s*:/);
	expect(layouts).not.toMatch(/--space-[a-z0-9-]+\s*:/);
	expect(layouts).not.toMatch(/--size-[a-z0-9-]+\s*:/);
	expect(layouts).not.toMatch(/--radius-[a-z0-9-]+\s*:/);
	expect(layouts).not.toMatch(/--tr-[a-z0-9-]+\s*:/);
	expect(layouts).not.toMatch(/--background\s*:/);
	expect(layouts).not.toMatch(/--text-[a-z0-9-]+\s*:/);
	expect(layouts).not.toMatch(/--border-[a-z0-9-]+\s*:/);
	expect(layouts).not.toContain("tailwind");
	expect(layouts).not.toContain("@theme");
	expect(layouts).not.toContain("@layer");
	expect(layouts).not.toContain("!important");
	expect(layouts).not.toContain("styles/generated");
	expect(layouts).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
	expect(layouts).not.toContain("rgb(");
	expect(layouts).not.toContain("color-mix(");
});

test("mewa-03 probes collapse narrow viewports and 200 percent zoom to one surface", async () => {
	const layouts = await source("foundation/layouts.css");
	expect(layouts).toContain("@media (width < 64rem)");
	expect(layouts).toContain('grid-template-areas: "mobile"');
	expect(layouts).toContain("grid-template-columns: minmax(0, 1fr)");
	// Every level keeps min bounds so content never forces page scroll.
	expect(layouts).toContain("min-width: 0");
	expect(layouts).toContain("min-height: 0");
	expect(layouts).toContain("overflow: hidden;");
	expect(layouts).toContain("overflow: auto;");
	expect(layouts).toContain("overscroll-behavior: contain;");
	// Percentage heights collapse under app-shell minimum-height guarantees.
	expect(layouts).not.toMatch(/height:\s*100%/);
	// Long names truncate inside their region at any zoom.
	expect(layouts).toContain("text-overflow: ellipsis;");
	expect(layouts).toContain("white-space: nowrap;");
});

test("mewa-03 probes keep keyboard, focus, and overflow contracts", async () => {
	const layouts = await source("foundation/layouts.css");
	// Visible focus only; behavior lives in shell-resizer and split-view.
	expect(layouts).toContain(":focus-visible");
	expect(layouts).toContain("var(--border-focus)");
	expect(layouts).toContain("var(--focus-ring-width");
	expect(layouts).toContain(".mewa-layout-probe__resizer:focus-visible");
	// Collapsed tracks never trap focus: hidden slots are inert.
	expect(layouts).toContain('[aria-hidden="true"]');
	expect(layouts).toContain("[inert]");
	expect(layouts).toContain("visibility: hidden;");
	// Independent scroll regions keep composer and headers anchored.
	expect(layouts).toContain("position: sticky;");
	expect(layouts).toContain("overflow-y: auto;");
	// Reduced motion and forced-colors stay explicit.
	expect(layouts).toContain("@media (prefers-reduced-motion: reduce)");
	expect(layouts).toContain("@media (forced-colors: active)");
	expect(layouts).toContain("CanvasText");
	expect(layouts).toContain("Highlight");
});

test("mewa-03 probes are presentation-only with no view-driven runtime loss", async () => {
	const layouts = await source("foundation/layouts.css");
	// Probe header records the runtime-outside-lifetime contract.
	for (const contract of [
		"MEWA-03",
		"Presentation-only",
		"never stops accepted work",
		"draft",
		"restoreLayout",
	]) {
		expect(layouts).toContain(contract);
	}
	// Visibility changes use track collapse and inertness, never state reset.
	expect(layouts).toContain("minmax(0, 0px)");
	// The canonical restore path still promises the same invariant.
	const workArea = await source("workspace/views/project-work-area.svelte");
	expect(workArea).toContain("Restore is presentation-only");
	expect(workArea).toContain("selections, drafts, and accepted work stay intact");
	expect(workArea).toContain("function restoreLayout()");
});
