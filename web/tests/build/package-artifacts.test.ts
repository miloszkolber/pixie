import { expect, test } from "bun:test";
import { productArchiveLayout, RELEASE_PRODUCTS } from "../../scripts/build-release.ts";
import {
	collectPackageArtifactInput,
	formatPackageArtifactReport,
	inspectPackageArtifacts,
	type PackageArchiveEvidence,
	type PackageArtifactInput,
} from "../../scripts/check-package-artifacts.ts";

const releaseId = "sha-0123456789ab";

function archive(
	product: (typeof RELEASE_PRODUCTS)[number],
	architecture: "amd64" | "arm64",
): PackageArchiveEvidence {
	const runtime =
		product === "pixie_cli" || product === "pixie"
			? [
					"runtime/manifest.json",
					"runtime/bin/bun",
					"runtime/bun/LICENSE.md",
					"runtime/node_modules/@earendil-works/pi-coding-agent/package.json",
					"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
					"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/chunks/tui.js",
				]
			: [];
	return {
		name: `${product}-${releaseId}-linux-${architecture}.tar.gz`,
		entries: productArchiveLayout(product, runtime),
	};
}

function passingPackage(): PackageArtifactInput {
	const command = `
--version
doctor
/readyz
signal.NotifyContext
func run() {}
func (r *Runtime) Start() {}
func (r *Runtime) Shutdown() {}
uninstall
`;
	return {
		releaseId,
		archives: RELEASE_PRODUCTS.flatMap((product) => [
			archive(product, "amd64"),
			archive(product, "arm64"),
		]),
		commandSources: { "web/cmd/main.go": command },
		webuiSources: { "web/webui/webui.go": "//go:embed all:dist" },
		embeddedUiFiles: ["web/webui/dist/index.html"],
		binaries: RELEASE_PRODUCTS.flatMap((product) => [
			{ product, architecture: "amd64" as const, path: product },
			{ product, architecture: "arm64" as const, path: product },
		]),
	};
}

test("package fixtures cover the three public archive layouts and lifecycle checks", () => {
	const report = inspectPackageArtifacts(passingPackage());

	expect(report.ok).toBe(true);
	expect(report.staticOk).toBe(true);
	expect(report.complete).toBe(true);
	expect(report.violations).toEqual([]);
	expect(report.facts.expectedArchives).toHaveLength(6);
	expect(report.facts.unitFiles).toEqual([]);
});

test("package checks reject old public archive names and incomplete archive contents", () => {
	const input = passingPackage();
	const report = inspectPackageArtifacts({
		...input,
		archives: [
			{
				name: `pixie_assistant-${releaseId}-linux-amd64.tar.gz`,
				entries: ["pixie_assistant"],
			},
		],
		binaries: input.binaries === undefined ? [] : input.binaries.slice(0, 1),
	});

	expect(report.ok).toBe(false);
	const output = formatPackageArtifactReport(report);
	expect(output).toMatch(/current public product/);
	expect(output).toMatch(/missing live artifact evidence: package entrypoint evidence/);
});

test("package checks reject old archive names and missing products as structural failures", () => {
	const input = passingPackage();
	const report = inspectPackageArtifacts({
		...input,
		archives: [archive("pixie_web", "amd64")],
		binaries: [{ product: "pixie_web", architecture: "amd64", path: "pixie_web" }],
	});

	expect(report.staticOk).toBe(true);
	expect(report.missingLiveEvidence.join("\n")).toContain(
		"package entrypoint evidence pixie/arm64",
	);
	expect(report.missingLiveEvidence.join("\n")).toContain("release archive pixie_cli");
});

test("checked-in package reports static gaps and missing live artifacts without claiming PKG-01", async () => {
	const report = inspectPackageArtifacts(await collectPackageArtifactInput());
	const output = formatPackageArtifactReport(report);

	expect(report.ok).toBe(false);
	expect(report.staticOk).toBe(true);
	expect(report.complete).toBe(false);
	expect(output).toContain("check-package-artifacts: FAILED");
	expect(output).toMatch(/missing live artifact evidence/);
	expect(output).toMatch(/six commit-named public product archives/);
	expect(report.facts.unitFiles).toEqual([]);
});
