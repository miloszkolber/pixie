import { expect, test } from "bun:test";
import { GENERATED_CSS_PATH, loadColors, renderCss, validate } from "../../webui/scripts/colors";

test("color generation emits only semantic custom properties", async () => {
	const colors = loadColors();
	expect(validate(colors)).toEqual([]);

	const css = renderCss(colors);
	expect(css).toContain(":root {");
	expect(css).not.toContain("@theme");
	expect(css).not.toContain("--color-");
	expect(css).not.toContain("Tailwind");
	for (const name of Object.keys(colors.roles)) expect(css).toContain(`\t--${name}:`);
	for (const name of Object.keys(colors.effects)) expect(css).toContain(`\t--${name}:`);
	expect(await Bun.file(GENERATED_CSS_PATH).text()).toBe(css);
});

test("color source rejects the retired utility publication flag", () => {
	const colors = loadColors();
	const role = { ...colors.roles.primary, publish: true };
	const invalid = {
		...colors,
		roles: { ...colors.roles, primary: role },
	} as unknown as typeof colors;
	expect(validate(invalid)).toContain("roles.primary: unexpected property publish");
});
