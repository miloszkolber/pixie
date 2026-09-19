import { expect, test } from "bun:test";
import {
	collectDocumentationInput,
	type DocumentationInput,
	formatDocumentationReport,
	inspectDocumentation,
} from "../../scripts/check-docs.ts";

function passingDocumentation(): DocumentationInput {
	return {
		files: {
			"README.md": "[Architecture](docs/architecture.md)\n",
			"docs/architecture.md": "# Architecture\n[Security](security.md#security)\n",
			"docs/security.md":
				"# Security\nVerified-from-code; same-UID is not a sandbox.\n## Security\n",
			"docs/pi.md": "# Pi integration\n",
			"docs/deployment.md": "# Deployment\n",
			"docs/development.md":
				"# Development\n" +
				["check:deps", "check:docs", "check:coverage", "lint", "typecheck", "test", "build"]
					.map((name) => `bun run ${name}`)
					.join("\n"),
			"roadmap/roadmap.md": "# Roadmap\n",
		},
		packageScripts: Object.fromEntries(
			["check:deps", "check:docs", "check:coverage", "lint", "typecheck", "test", "build"].map(
				(name) => [name, "fixture"],
			),
		),
	};
}

// Minimal generated-module fixtures. The real catalog is produced by the
// contract generator; these snippets exercise the same text shape the drift
// guard parses without importing the generated module twice.
function generatedCatalog(statuses: Readonly<Record<string, string>>): string {
	const names = Object.keys(statuses);
	return (
		"export const CONTROLLER_METHODS = [\n" +
		names.map((name) => `\t"${name}",`).join("\n") +
		"\n] as const;\n\n" +
		"export const CONTROLLER_METHOD_STATUS: Record<ControllerMethod, HostOperationStatus> = {\n" +
		names.map((name) => `\t"${name}": "${statuses[name]}",`).join("\n") +
		"\n};\n"
	);
}

function coverageSection(total: number, sections: readonly string[]): string {
	return [
		"## Method coverage",
		"",
		`The table enumerates all ${total} methods.`,
		"",
		...sections,
		"",
		"## Decisions",
		"",
	].join("\n");
}

function methodRows(status: string, methods: readonly string[]): string {
	return [
		`### ${status} (${methods.length})`,
		"",
		"| Method | Route | Owner |",
		"| --- | --- | --- |",
		...methods.map((method) => `| \`${method}\` | \`route\` | controller |`),
		"",
	].join("\n");
}

test("documentation checker validates local links, anchors, source paths and commands", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"docs/architecture.md": `${input.files["docs/architecture.md"]}\n\`cmd/main.go\`\n`,
			"cmd/main.go": "package main\n",
		},
	});

	expect(report.ok).toBe(true);
	expect(report.violations).toEqual([]);
	expect(report.facts.localLinks).toBe(2);
	expect(report.facts.checkedCommands).toHaveLength(7);
});

test("documentation checker rejects stale links, anchors, source references and commands", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"docs/architecture.md": `${input.files["docs/architecture.md"]}\n[Missing](gone.md#nowhere)\n[Stale](security.md#nowhere)\n\`internal/removed.go\`\nroadmap/MCP.md\n`,
			"docs/development.md": "# Development\nbun run test\n",
		},
		packageScripts: { test: "fixture" },
	});

	expect(report.ok).toBe(false);
	const output = formatDocumentationReport(report);
	expect(output).toContain("local link target does not exist");
	expect(output).toContain("has no heading anchor");
	expect(output).toContain("referenced path does not exist");
	expect(output).toContain("missing documented command bun run check:deps");
	expect(output).toContain("removed planning copy");
});

test("documentation checker rejects an undocumented environment variable", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"src/assistant/seeded.ts": "const value = process.env.PIXIE_SEEDED_UNDOCUMENTED;\n",
		},
	});

	expect(report.ok).toBe(false);
	expect(formatDocumentationReport(report)).toContain(
		"undocumented environment variable PIXIE_SEEDED_UNDOCUMENTED",
	);
});

test("documentation checker accepts documented and fixture environment variables", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"docs/deployment.md": "# Deployment\nPIXIE_SEEDED_DOCUMENTED\n",
			"src/assistant/config.ts": "process.env.PIXIE_SEEDED_DOCUMENTED;\n",
			"src/assistant/probe.ts": "process.env.PIXIE_PI_SDK_PROBE_CHECKER;\n",
			// The scanner ignores test files, so their fixtures need no entry.
			"internal/controller/seeded_test.go": "PIXIE_SEEDED_TEST_FIXTURE\n",
		},
	});

	expect(report.ok).toBe(true);
	expect(report.violations).toEqual([]);
	expect(report.facts.environmentVariables).toBe(2);
});

test("documentation checker accepts a method coverage table matching the generated catalog", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"src/shared/generated/protocol-catalog.ts": generatedCatalog({
				"session.list": "available",
				"session.prompt": "available",
				"session.steer": "unavailable",
			}),
			"docs/sdk-coverage.md": coverageSection(3, [
				methodRows("available", ["session.list", "session.prompt"]),
				methodRows("unavailable", ["session.steer"]),
			]),
		},
	});

	expect(report.ok).toBe(true);
	expect(report.violations).toEqual([]);
	expect(report.facts.documentedMethods).toBe(3);
});

test("documentation checker rejects a method coverage table that drifts from the generated catalog", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"src/shared/generated/protocol-catalog.ts": generatedCatalog({
				"alpha.one": "available",
				"alpha.two": "available",
				"beta.one": "unavailable",
			}),
			// Count, split and per-row status are all wrong: the catalog has two
			// available and one unavailable method, and beta.one is misfiled.
			"docs/sdk-coverage.md": coverageSection(2, [
				methodRows("available", ["alpha.one", "alpha.two", "beta.one"]),
				methodRows("unavailable", []),
			]),
		},
	});

	expect(report.ok).toBe(false);
	const output = formatDocumentationReport(report);
	expect(output).toContain("declared method count 2 does not match the generated catalog count 3");
	expect(output).toContain("available heading reports 3 but the generated catalog has 2");
	expect(output).toContain("available table lists 3 methods but the generated catalog has 2");
	expect(output).toContain("unavailable table lists 0 methods but the generated catalog has 1");
	expect(output).toContain(
		"documented method beta.one is listed under available but the generated catalog marks it unavailable",
	);
});

test("documentation checker rejects unknown and missing generated controller methods", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"src/shared/generated/protocol-catalog.ts": generatedCatalog({
				"alpha.one": "available",
				"alpha.two": "available",
			}),
			"docs/sdk-coverage.md": coverageSection(2, [
				methodRows("available", ["alpha.one", "gamma.one"]),
			]),
		},
	});

	expect(report.ok).toBe(false);
	const output = formatDocumentationReport(report);
	expect(output).toContain("documented method gamma.one is not a generated controller method");
	expect(output).toContain("generated controller method alpha.two is not documented");
});

test("checked-in operating docs pass static checks", async () => {
	const report = inspectDocumentation(await collectDocumentationInput());
	expect(formatDocumentationReport(report)).toContain("check-docs: OK");
	expect(report.ok).toBe(true);
}, 15_000);
