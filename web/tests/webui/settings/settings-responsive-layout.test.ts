import { expect, test } from "bun:test";

const sectionsRoot = new URL("../../../webui/src/settings/sections/", import.meta.url);

function readSection(name: string): Promise<string> {
	return Bun.file(new URL(`${name}.svelte`, sectionsRoot)).text();
}

// The settings detail pane can be ~360-420 CSS px wide while the window is large, so layout must
// follow the pane (container query) instead of the viewport (`sm:` / `md:`).

test("provider cards size to the pane and keep the description readable", async () => {
	const source = await readSection("provider-card");
	expect(source).toContain('class="@container"');
	// A real minimum keeps the description out of a mid-word crush.
	expect(source).toContain("min-w-48");
	// Only widen to a single action row once the pane itself has room.
	expect(source).toContain("@2xl:flex-nowrap");
	expect(source).not.toContain("sm:flex-nowrap");
	expect(source).toContain("flex shrink-0 flex-wrap items-center gap-xs");
});

test("system cards stack below the pane threshold and keep label/value separate", async () => {
	const source = await readSection("system-settings");
	expect(source).toContain("@container mx-auto flex w-full max-w-[56rem]");
	expect(source).toContain("@2xl:grid-cols-3");
	expect(source).not.toContain("md:grid-cols-3");
	// Label keeps its intrinsic width and the value gets the remaining track, so the gap is visible.
	expect(source).toContain("grid-cols-[max-content_minmax(0,1fr)]");
	expect(source).toContain("gap-x-sm");
	// The badge wraps under the title instead of being clipped, and never breaks inside its label.
	expect(source).toContain("whitespace-nowrap");
	expect(source).toContain("gap-x-sm gap-y-xs");
});

test("model rows stack inside the pane and keep the action and meta badge intact", async () => {
	const source = await readSection("models-settings");
	expect(source).toContain('class="@container flex flex-col gap-lg"');
	expect(source).toContain("@2xl:grid-cols-[minmax(12rem,1fr)_auto_auto]");
	expect(source).not.toContain("sm:grid-cols-");
	// The ctx/out badge stays on one line and the eye control remains present.
	expect(source).toContain('class="badge whitespace-nowrap"');
	expect(source).toContain('name={model.hidden ? "eye-off" : "eye"}');
});

test("Pi preference resets sit with their fields instead of the save action", async () => {
	const source = await readSection("pi-settings");
	const reserve = source.indexOf("Reset reserve");
	const thinkingField = source.indexOf("Thinking effort");
	const thinkingReset = source.indexOf("Reset thinking");
	const save = source.indexOf("Save preferences");
	expect(reserve).toBeGreaterThan(-1);
	expect(thinkingField).toBeGreaterThan(-1);
	expect(thinkingReset).toBeGreaterThan(-1);
	expect(save).toBeGreaterThan(-1);
	// Reset reserve precedes the thinking field; reset thinking follows it and precedes save.
	expect(reserve).toBeLessThan(thinkingField);
	expect(thinkingReset).toBeGreaterThan(thinkingField);
	expect(thinkingReset).toBeLessThan(save);
	const saveBlock = source.match(
		/<div class="u-flex u-flex-wrap u-gap-xs">\s*<Button size="sm" disabled=\{busy \|\| loading \|\| !preferencesReady\} onclick=\{\(\) => void savePreferences\(\)\}>[\s\S]*?<\/Button>\s*<\/div>/,
	);
	expect(saveBlock?.[0]).toBeDefined();
	expect(saveBlock?.[0]).not.toContain("Reset");
});
