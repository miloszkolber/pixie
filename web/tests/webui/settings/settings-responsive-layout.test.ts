import { expect, test } from "bun:test";

const sectionsRoot = new URL("../../../webui/src/settings/sections/", import.meta.url);

function readSection(name: string): Promise<string> {
	return Bun.file(new URL(`${name}.svelte`, sectionsRoot)).text();
}

// The settings detail pane can be ~360-420 CSS px wide while the window is large, so layout must
// follow the pane (container query) instead of the viewport (`sm:` / `md:`).

test("provider cards size to the pane and keep the description readable", async () => {
	const source = await readSection("provider-card");
	expect(source).toContain('class="provider-card-container"');
	expect(source).toContain("container-type: inline-size;");
	// A real minimum keeps the description out of a mid-word crush.
	expect(source).toContain(".provider-card__summary");
	expect(source).toContain("min-inline-size: 12rem;");
	// Only widen to a single action row once the pane itself has room.
	expect(source).toContain("@container (min-width: 42rem)");
	expect(source).toMatch(
		/@container \(min-width: 42rem\)\s*{[^}]*\.provider-card\s*{[^}]*flex-wrap:\s*nowrap;/s,
	);
	expect(source).not.toContain("@media (min-width: 42rem)");
	expect(source).toContain('class="u-flex u-shrink-0 u-flex-wrap u-items-center u-gap-xs"');
});

test("system cards stack below the pane threshold and keep label/value separate", async () => {
	const source = await readSection("system-settings");
	expect(source).toContain(".system-settings");
	expect(source).toContain("max-inline-size: 56rem;");
	expect(source).toContain("container-type: inline-size;");
	expect(source).toContain(".system-service-grid");
	expect(source).toContain("grid-template-columns: minmax(0, 1fr);");
	expect(source).toMatch(
		/@container \(min-width: 42rem\)\s*{[^}]*\.system-service-grid\s*{[^}]*grid-template-columns:\s*repeat\(3, minmax\(0, 1fr\)\);/s,
	);
	expect(source).not.toContain("@media (min-width: 42rem)");
	// Label keeps its intrinsic width and the value gets the remaining track, so the gap is visible.
	expect(source).toContain("grid-template-columns: max-content minmax(0, 1fr);");
	expect(source).toContain("column-gap: var(--space-sm);");
	// The badge wraps under the title instead of being clipped, and never breaks inside its label.
	expect(source).toContain(".state-badge");
	expect(source).toContain("white-space: nowrap;");
	expect(source).toContain('class="u-flex u-flex-wrap u-items-center u-justify-between u-gap-sm"');
});

test("model rows stack inside the pane and keep the action and meta badge intact", async () => {
	const source = await readSection("models-settings");
	expect(source).toContain('class="models-settings u-flex u-flex-col u-gap-lg"');
	expect(source).toContain("container-type: inline-size;");
	expect(source).toContain(".model-row");
	expect(source).toContain("grid-template-columns: minmax(0, 1fr);");
	expect(source).toMatch(
		/@container \(min-width: 42rem\)\s*{[^}]*\.model-row\s*{[^}]*grid-template-columns:\s*minmax\(12rem, 1fr\) auto auto;/s,
	);
	expect(source).not.toContain("@media (min-width: 42rem)");
	// The ctx/out badge stays on one line and the eye control remains present.
	expect(source).toContain(".model-row__context");
	expect(source).toContain("white-space: nowrap;");
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
