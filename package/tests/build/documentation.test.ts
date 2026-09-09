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
			"roadmap/README.md": "# Roadmap\n",
		},
		packageScripts: Object.fromEntries(
			["check:deps", "check:docs", "check:coverage", "lint", "typecheck", "test", "build"].map(
				(name) => [name, "fixture"],
			),
		),
	};
}

test("documentation checker validates local links, anchors, source paths and commands", () => {
	const input = passingDocumentation();
	const report = inspectDocumentation({
		...input,
		files: {
			...input.files,
			"docs/architecture.md": `${input.files["docs/architecture.md"]}\n\`package/cmd/main.go\`\n`,
			"package/cmd/main.go": "package main\n",
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
			"docs/architecture.md": `${input.files["docs/architecture.md"]}\n[Missing](gone.md#nowhere)\n[Stale](security.md#nowhere)\n\`package/internal/removed.go\`\nroadmap/MCP.md\n`,
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

test("checked-in operating docs pass static checks", async () => {
	const report = inspectDocumentation(await collectDocumentationInput());
	expect(formatDocumentationReport(report)).toContain("check-docs: OK");
	expect(report.ok).toBe(true);
});
