import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const components = [
	new URL("../../../webui/src/workspace/views/project-work-area.svelte", import.meta.url),
	new URL("../../../webui/src/design/design-preview.svelte", import.meta.url),
] as const;

test("active Canvas and Design surfaces do not replay executable HTML", async () => {
	for (const url of components) {
		const source = await Bun.file(url).text();
		expect(compile(source, { filename: url.pathname, generate: false }).warnings).toEqual([]);
		expect(source).not.toContain("{@html}");
		expect(source).not.toContain("<iframe");
		expect(source).not.toContain("srcdoc");
		expect(source).not.toContain("innerHTML");
	}
});
