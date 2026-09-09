import { expect, test } from "bun:test";
import {
	type AssistantBuildInput,
	collectAssistantBuildInput,
	formatAssistantBuildReport,
	inspectAssistantBuild,
} from "../../scripts/check-assistant-build.ts";

function passingBuild(): AssistantBuildInput {
	return {
		manifest: {
			bin: { "pixie-assistant": "dist/main.js" },
			files: ["dist", "patches"],
			exports: {
				"./capabilities": "./dist/capabilities.js",
				"./doctor": "./dist/doctor.js",
			},
			scripts: {
				start: "bun dist/main.js",
				build: "bun build src/main.ts src/doctor.ts --target=bun --outdir=dist",
				typecheck: "tsc --noEmit",
			},
		},
		sourceFiles: {
			"src/main.ts": 'import "./runtime.ts"; import "../package.json";\n',
			"src/runtime.ts": "export const runtime = true;\n",
			"src/doctor.ts": "export const doctor = true;\n",
		},
	};
}

test("assistant build uses an independent bundled runtime and keeps doctor/typecheck", () => {
	const report = inspectAssistantBuild(passingBuild());

	expect(report.ok).toBe(true);
	expect(report.facts.buildEntries).toEqual(["src/main.ts", "src/doctor.ts"]);
	expect(report.facts.artifactRoots).toEqual(["dist"]);
	expect(report.facts.closure).toEqual(["src/doctor.ts", "src/main.ts", "src/runtime.ts"]);
});

test("assistant build rejects controller/UI/worker closure and non-runtime artifacts", () => {
	const input = passingBuild();
	const report = inspectAssistantBuild({
		...input,
		sourceFiles: {
			...input.sourceFiles,
			"src/main.ts": 'import "./runtime.ts"; import "./controller.ts";\n',
			"src/controller.ts": 'import "../../package/webui/src/app.ts";\n',
		},
		artifactFiles: ["dist/main.js", "src/main.ts", "tests/build.test.ts", ".env.production"],
	});

	expect(report.ok).toBe(false);
	const violations = report.violations.join("\n");
	expect(violations).toMatch(/forbidden path|escapes assistant runtime/);
	expect(violations).toMatch(/non-runtime file/);
});

test("assistant artifact checks reject embedded secret literals and source package allowlists", () => {
	const input = passingBuild();
	const report = inspectAssistantBuild({
		...input,
		manifest: { ...input.manifest, files: ["dist", "src", "tests", "secrets"] },
		artifactSources: { "dist/main.js": 'const PIXIE_PI_SECRET_KEY = "not-a-test-secret";\n' },
	});

	expect(report.ok).toBe(false);
	const violations = report.violations.join("\n");
	expect(violations).toMatch(/runtime-only/);
	expect(violations).toMatch(/secret-shaped literal/);
});

test("assistant build rejects a boundary dependency even when it is not a relative import", () => {
	const input = passingBuild();
	const report = inspectAssistantBuild({
		...input,
		manifest: {
			...input.manifest,
			dependencies: { "@pixie/controller-runtime": "1.0.0" },
		},
		sourceFiles: {
			...input.sourceFiles,
			"src/main.ts": 'import "@pixie/worker-runtime";\n',
		},
	});

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toMatch(/controller\/UI\/worker/);
});

test("assistant build does not silently drop doctor or the independent typecheck", () => {
	const input = passingBuild();
	const scripts = { ...input.manifest.scripts };
	delete scripts.typecheck;
	const sourceFiles = { ...input.sourceFiles };
	delete sourceFiles["src/doctor.ts"];
	const report = inspectAssistantBuild({
		...input,
		manifest: { ...input.manifest, scripts },
		sourceFiles,
	});

	expect(report.ok).toBe(false);
	const violations = report.violations.join("\n");
	expect(violations).toMatch(/doctor/);
	expect(violations).toMatch(/typecheck/);
});

test("checked-in assistant package reports the remaining runtime-packaging work", async () => {
	const report = inspectAssistantBuild(await collectAssistantBuildInput());

	const output = formatAssistantBuildReport(report);
	expect(output).toContain("FAILED");
	expect(report.ok).toBe(false);
	expect(output).toMatch(/runtime bin/);
	expect(output).toMatch(/bundled dist artifact/);
	expect(output).toMatch(/runtime-only/);
	expect(output).toMatch(/export .*bundled runtime output/);
	expect(output).toMatch(/doctor/);
});
