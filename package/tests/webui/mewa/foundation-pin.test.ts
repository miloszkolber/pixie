import { expect, test } from "bun:test";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);
const webuiRoot = new URL("../../../webui/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

async function webuiFile(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiRoot)).text();
}

function importSpecifiers(css: string): string[] {
	return [...css.matchAll(/^\s*@import\s+["']([^"']+)["'];\s*$/gm)].map((match) => match[1] ?? "");
}

test("mewa vendor lock pins 0.1.2 with matching manifest foundations", async () => {
	const lock = (await Bun.file(new URL("vendor/mewa.lock.json", webuiRoot)).json()) as {
		version: string;
		release: string;
		revision: string;
		assets: unknown[];
		icons: string[];
	};
	expect(lock.version).toBe("0.1.2");
	expect(lock.release).toBe("v0.1.2");
	expect(lock.revision).toBe("c1bfb64a5874f2ca15d81c5ad78a2060360a37c0");
	expect(lock.assets).toHaveLength(3);
	expect(lock.icons.length).toBeGreaterThan(0);

	const manifest = (await Bun.file(new URL("vendor/mewa-ui/manifest.json", webuiRoot)).json()) as {
		name: string;
		version: string;
		foundations: { base: string; tokens: string };
	};
	expect(manifest.name).toBe("mewa-ui");
	expect(manifest.version).toBe(lock.version);
	expect(manifest.foundations.base).toBe("css/base.css");
	expect(manifest.foundations.tokens).toBe("css/tokens.css");
});

test("foundation pin records the lock without defining a second visual system", async () => {
	const pin = await source("foundation/tokens.css");
	for (const marker of [
		"0.1.2",
		"c1bfb64a5874f2ca15d81c5ad78a2060360a37c0",
		"vendor/mewa.lock.json",
	]) {
		expect(pin).toContain(marker);
	}
	expect(pin).toContain("--mewa-foundation-version");
	// Role names appear in the header inventory for review; only emitted
	// declarations can become a second visual system.
	const declarations = pin.slice(pin.indexOf(":root"));
	expect(declarations).toContain(":root");
	expect(declarations).not.toContain("--color-");
	expect(declarations).not.toContain("--background");
	expect(declarations).not.toContain("--text-");
	expect(declarations).not.toContain("rgb(");
	expect(declarations).not.toContain("color-mix(");
	expect(pin).not.toContain("@theme");
	expect(pin).not.toContain("@layer");
	expect(pin).not.toContain("!important");
	expect(declarations).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
});

test("mewa.css is the single foundation cascade owner with pinned order", async () => {
	const mewa = await source("mewa.css");
	const imports = importSpecifiers(mewa);
	expect(imports.length).toBeGreaterThan(10);
	expect(imports[0]).toBe("./foundation/tokens.css");
	expect(imports[1]).toBe("../vendor/mewa-ui/css/base.css");
	expect(imports[2]).toBe("../vendor/mewa-ui/css/tokens.css");
	expect(imports).toContain("../vendor/mewa-ui/css/label.css");
	const labelIndex = imports.indexOf("../vendor/mewa-ui/css/label.css");
	const fieldIndex = imports.indexOf("../vendor/mewa-ui/css/field.css");
	expect(labelIndex).toBeGreaterThan(-1);
	expect(fieldIndex).toBeGreaterThan(-1);
	expect(labelIndex).toBeLessThan(fieldIndex);
	for (const specifier of imports) {
		expect(specifier.startsWith(".")).toBeTrue();
		expect(specifier).not.toContain("styles/");
		expect(specifier).not.toContain("tailwind");
	}
	expect(mewa).not.toContain("@theme");
	expect(mewa).not.toContain("!important");
	expect(mewa).not.toMatch(/--color-[a-z]/);

	const indexCss = await source("index.css");
	expect(indexCss).not.toContain("vendor/mewa");

	const main = await source("main.ts");
	const indexAt = main.indexOf('import "./index.css"');
	const mewaAt = main.indexOf('import "./mewa.css"');
	expect(indexAt).toBeGreaterThanOrEqual(0);
	expect(mewaAt).toBeGreaterThan(indexAt);
});

test("pinned vendor foundations expose the semantic roles pixie reads", async () => {
	const base = await webuiFile("vendor/mewa-ui/css/base.css");
	expect(base).toContain("COLOR-PALETTE:START");
	expect(base).toContain("--space-100:");
	expect(base).toContain("--size-1000:");
	expect(base).toContain("--focus-ring-width:");

	const tokens = await webuiFile("vendor/mewa-ui/css/tokens.css");
	for (const role of [
		"--background:",
		"--surface-secondary:",
		"--surface-inverted:",
		"--text-primary:",
		"--text-muted:",
		"--border-primary:",
		"--border-secondary:",
		"--border-focus:",
	]) {
		expect(tokens).toContain(role);
	}
});
