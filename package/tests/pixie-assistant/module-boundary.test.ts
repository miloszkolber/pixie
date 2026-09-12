import { expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import {
	ASSISTANT_MODULE_NAME,
	assertAssistantImportSurface,
	checkExactLocalReplacement,
	extractStaticImportSpecifiers,
	findForbiddenAssistantImports,
} from "../../../assistant/src/module-boundary.ts";

async function collectAssistantSources(root: string): Promise<Record<string, string>> {
	const files: Record<string, string> = {};
	async function walk(dir: string): Promise<void> {
		const entries = await readdir(dir, { withFileTypes: true });
		for (const entry of entries) {
			const path = join(dir, entry.name);
			if (entry.isDirectory()) {
				await walk(path);
			} else if (entry.isFile() && entry.name.endsWith(".ts")) {
				files[path] = await readFile(path, "utf8");
			}
		}
	}
	await walk(root);
	return files;
}

test("assistant import surface extractor ignores comments and finds requires", () => {
	const source = `
		// import ignored from "package/internal/ignored";
		/* import alsoIgnored from 'package/webui/ignored'; */
		import { getAgentDir } from "@earendil-works/pi-coding-agent";
		import manifest from "../package.json" with { type: "json" };
		import { startHost } from "./server.ts";
		export { helper } from "./startup.ts";
		const lazy = require("proper-lockfile");
	`;
	const specifiers = extractStaticImportSpecifiers(source);
	expect(specifiers).toContain("@earendil-works/pi-coding-agent");
	expect(specifiers).toContain("../package.json");
	expect(specifiers).toContain("./server.ts");
	expect(specifiers).toContain("./startup.ts");
	expect(specifiers).toContain("proper-lockfile");
	expect(specifiers.join("\n")).not.toContain("package/internal/ignored");
	expect(specifiers.join("\n")).not.toContain("package/webui/ignored");
});

test("assistant boundary allows SDK and local modules, rejects controller internals", () => {
	const allowed = [
		"@earendil-works/pi-coding-agent",
		"@earendil-works/pi-ai",
		"node:fs/promises",
		"node:path",
		"proper-lockfile",
		"yaml",
		"./facade.ts",
		"./startup.ts",
		"./server.ts",
		"./discovery.ts",
		"./discovery-compat.ts",
		"./module-boundary.ts",
		"../package.json",
	];
	expect(findForbiddenAssistantImports(allowed)).toEqual([]);

	const forbiddenCases: Array<[string, string]> = [
		["../../package/internal/controller/store", "package/internal/"],
		["../webui/src/state", "/webui/"],
		["../../package/webui/src/routes", "package/webui/"],
		["../../package/cmd/pixie", "package/cmd/"],
		["../../package/contracts/schemas", "package/contracts/"],
	];
	for (const [specifier, matched] of forbiddenCases) {
		const found = findForbiddenAssistantImports([specifier]);
		expect(found.length).toBe(1);
		expect(found[0]?.matched).toBe(matched);
	}

	expect(() =>
		assertAssistantImportSurface({
			"assistant/src/probe.ts": `import { store } from "../../package/internal/store.ts";`,
		}),
	).toThrow("module boundary violated");
	expect(() =>
		assertAssistantImportSurface({
			"assistant/src/ok.ts": `import { getAgentDir } from "@earendil-works/pi-coding-agent";`,
		}),
	).not.toThrow();
});

test("current assistant checkout imports nothing controller-internal", async () => {
	const root = resolve(import.meta.dir, "../../../assistant/src");
	const files = await collectAssistantSources(root);
	expect(Object.keys(files).length).toBeGreaterThan(0);
	expect(Object.keys(files).some((path) => path.endsWith("facade.ts"))).toBe(true);
	expect(Object.keys(files).some((path) => path.endsWith("doctor.ts"))).toBe(true);
	assertAssistantImportSurface(files);
});

test("exact local replacement resolves assistant and package from this checkout", async () => {
	const dir = import.meta.dir;
	const rootText = await readFile(resolve(dir, "../../../package.json"), "utf8");
	const assistantText = await readFile(resolve(dir, "../../../assistant/package.json"), "utf8");
	const packageGoMod = await readFile(resolve(dir, "../../go.mod"), "utf8").catch(() => undefined);

	const check = checkExactLocalReplacement({
		rootPackageJsonText: rootText,
		assistantPackageJsonText: assistantText,
		packageGoModText: packageGoMod,
	});
	expect(check.ok).toBe(true);
	expect(check.details.join("\n")).toMatch(/exact local/);
	expect(JSON.parse(assistantText).name).toBe(ASSISTANT_MODULE_NAME);
});

test("exact local replacement rejects published-only requires", () => {
	const rootText = JSON.stringify({ workspaces: { packages: ["assistant", "package"] } });
	const assistantText = JSON.stringify({ name: ASSISTANT_MODULE_NAME, dependencies: {} });

	const publishedOnly = checkExactLocalReplacement({
		rootPackageJsonText: rootText,
		assistantPackageJsonText: assistantText,
		packageGoModText:
			"module github.com/miloszkolber/pixie\n\nrequire example.test/assistant v1.2.3\n",
	});
	expect(publishedOnly.ok).toBe(false);
	expect(publishedOnly.details.join("\n")).toMatch(/exact local replace/);

	const exactLocal = checkExactLocalReplacement({
		rootPackageJsonText: rootText,
		assistantPackageJsonText: assistantText,
		packageGoModText:
			"module github.com/miloszkolber/pixie\n\nrequire example.test/assistant v0.0.0\n\nreplace example.test/assistant => ../assistant\n",
	});
	expect(exactLocal.ok).toBe(true);

	const nonLocalReplace = checkExactLocalReplacement({
		rootPackageJsonText: rootText,
		assistantPackageJsonText: assistantText,
		packageGoModText:
			"module github.com/miloszkolber/pixie\n\nrequire example.test/assistant v0.0.0\n\nreplace example.test/assistant => /tmp/other\n",
	});
	expect(nonLocalReplace.ok).toBe(false);

	const internalAcrossModules = checkExactLocalReplacement({
		rootPackageJsonText: rootText,
		assistantPackageJsonText: assistantText,
		packageGoModText: "module x\n\n// import example.test/assistant/internal/foo\n",
	});
	expect(internalAcrossModules.ok).toBe(false);

	const controllerDep = checkExactLocalReplacement({
		rootPackageJsonText: rootText,
		assistantPackageJsonText: JSON.stringify({
			name: ASSISTANT_MODULE_NAME,
			dependencies: { pixie: "file:../package" },
		}),
	});
	expect(controllerDep.ok).toBe(false);
});
