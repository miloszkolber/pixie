import { expect, test } from "bun:test";
import { readdir } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const repository = new URL("../../../", import.meta.url);
const webui = new URL("../../webui/", import.meta.url);
const webuiPath = fileURLToPath(webui);

async function svelteFiles(directory: string): Promise<string[]> {
	const entries = await readdir(directory, { withFileTypes: true });
	return (
		await Promise.all(
			entries.map((entry) => {
				const path = join(directory, entry.name);
				if (entry.isDirectory()) return svelteFiles(path);
				return entry.name.endsWith(".svelte") ? [path] : [];
			}),
		)
	).flat();
}

function utilitySelectors(css: string): Set<string> {
	return new Set(
		[...css.matchAll(/\.((?:\\.|[A-Za-z0-9_-])+)(?=\s*(?:\{|,|:))/g)].map(([, name]) =>
			(name ?? "").replace(/\\(.)/g, "$1"),
		),
	);
}

function utilityConsumers(source: string): string[] {
	return [...source.matchAll(/\bu-[A-Za-z0-9_.-]+\b/g)].map(([token]) => token);
}

test("Web UI build configuration has no Tailwind runtime dependency", async () => {
	const rootPackage = (await Bun.file(new URL("package.json", repository)).json()) as {
		workspaces: { catalog: Record<string, string> };
	};
	const webuiPackage = (await Bun.file(new URL("package.json", webui)).json()) as {
		devDependencies?: Record<string, string>;
	};
	const [lock, biome, indexCss, build] = await Promise.all([
		Bun.file(new URL("bun.lock", repository)).text(),
		Bun.file(new URL("biome.json", repository)).text(),
		Bun.file(new URL("src/index.css", webui)).text(),
		Bun.file(new URL("scripts/build.ts", webui)).text(),
	]);

	expect(rootPackage.workspaces.catalog).not.toHaveProperty("@tailwindcss/cli");
	expect(rootPackage.workspaces.catalog).not.toHaveProperty("tailwindcss");
	expect(webuiPackage.devDependencies).not.toHaveProperty("@tailwindcss/cli");
	expect(webuiPackage.devDependencies).not.toHaveProperty("tailwindcss");
	expect(lock).not.toMatch(/@tailwindcss\/|\btailwindcss\b/);
	expect(biome).not.toContain("tailwindDirectives");
	expect(indexCss).not.toMatch(/@import\s+["']tailwindcss|@theme\b/);
	expect(build).not.toMatch(/@tailwindcss|tailwind/i);
});

test("every Svelte u-* utility consumer has a token-backed selector", async () => {
	const [utilities, files] = await Promise.all([
		Bun.file(new URL("src/styles/utilities.css", webui)).text(),
		svelteFiles(join(webuiPath, "src")),
	]);
	const sources = await Promise.all(files.map((file) => Bun.file(file).text()));
	const selectors = utilitySelectors(utilities);
	const consumers = new Set(sources.flatMap(utilityConsumers));

	expect([...consumers].filter((token) => !selectors.has(token)).sort()).toEqual([]);
});

test("connection status dots use Mewa state attributes instead of Tailwind colour maps", async () => {
	const sourcePaths = [
		"src/workspace/shell.svelte",
		"src/workspace/views/project-work-area.svelte",
	];
	const [mewaStatusDotCss, ...sources] = await Promise.all([
		Bun.file(new URL("vendor/mewa-ui/css/components/app-shell.css", webui)).text(),
		...sourcePaths.map((path) => Bun.file(new URL(path, webui)).text()),
	]);
	const states = {
		connected: "positive",
		connecting: "caution",
		disconnected: "negative",
	};
	const labels = {
		connected: "Connected",
		connecting: "Connecting…",
		disconnected: "Disconnected",
	};

	for (const state of Object.values(states))
		expect(mewaStatusDotCss).toContain(`.status-dot[data-state="${state}"]`);
	for (const source of sources) {
		const map = source.match(/const STATUS_DOT = \{([\s\S]*?)\} as const;/)?.[1];
		expect(map).toBeDefined();
		for (const [status, state] of Object.entries(states))
			expect(map).toContain(`${status}: "${state}",`);
		expect(map).not.toContain("bg-feedback-");
		for (const [status, label] of Object.entries(labels))
			expect(source).toContain(`${status}: "${label}",`);
		expect(source).toContain('data-testid="connection-status"');
		expect(source).toContain("data-status={$appStore.status}");
		expect(source).toContain('role="status"');
		expect(source).toContain("aria-label={STATUS_LABEL[$appStore.status]}");
		expect(source).toContain('class="status-dot" data-state={STATUS_DOT[$appStore.status]}');
	}
});
