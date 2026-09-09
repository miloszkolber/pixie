import { expect, test } from "bun:test";
import {
	collectPackageArtifactInput,
	formatPackageArtifactReport,
	inspectPackageArtifacts,
	type PackageArchiveEvidence,
	type PackageArtifactInput,
} from "../../scripts/check-package-artifacts.ts";

const releaseId = "sha-0123456789ab";

const assistantUnit = `[Unit]
Description=Pixie assistant
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=exec
ExecStart=%h/.local/bin/pixie-assistant serve --config %h/.config/pixie/assistant.json
Restart=on-failure
RestartSec=2
RestartForceExitStatus=75
TimeoutStopSec=30
KillMode=mixed
UMask=0077
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`;

const hostUnit = `[Unit]
Description=Pixie full host
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=exec
ExecStart=%h/.local/bin/pixie serve --config %h/.config/pixie/pixie.json
Restart=on-failure
RestartSec=2
RestartForceExitStatus=75
TimeoutStopSec=30
KillMode=mixed
UMask=0077
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`;

function archive(variant: "assistant" | "host", architecture: "amd64" | "arm64"): PackageArchiveEvidence {
	const binary = variant === "assistant" ? "pixie-assistant" : "pixie";
	const unit = `${binary}.service`;
	const config = variant === "assistant" ? "assistant.json" : "pixie.json";
	return {
		name: `${binary}-${releaseId}-linux-${architecture}.tar.gz`,
		entries: [binary, unit, config, "INSTALL.md", "LICENSE", "NOTICE.md"],
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
		archives: [
			archive("assistant", "amd64"),
			archive("assistant", "arm64"),
			archive("host", "amd64"),
			archive("host", "arm64"),
		],
		units: {
			"package/systemd/pixie-assistant.service": assistantUnit,
			"package/systemd/pixie.service": hostUnit,
		},
		configs: {
			"package/systemd/assistant.json": '{"host":"127.0.0.1","port":3284}',
			"package/systemd/pixie.json": '{"host":"127.0.0.1","port":7312,"mode":"full-host"}',
		},
		commandSources: { "package/cmd/main.go": command },
		webuiSources: { "package/webui/webui.go": "//go:embed all:dist" },
		embeddedUiFiles: ["package/webui/dist/index.html"],
		facadeSources: { "assistant/host/host.go": "func Start() *Handle { return handle }" },
		binaries: [
			{
				variant: "assistant",
				architecture: "amd64",
				path: "/release/pixie-assistant",
				version: releaseId,
				doctor: true,
				readiness: true,
				lifecycle: true,
				uninstall: true,
			},
			{
				variant: "assistant",
				architecture: "arm64",
				path: "/release/pixie-assistant",
				version: releaseId,
				doctor: true,
				readiness: true,
				lifecycle: true,
				uninstall: true,
			},
			{
				variant: "host",
				architecture: "amd64",
				path: "/release/pixie",
				version: releaseId,
				doctor: true,
				readiness: true,
				lifecycle: true,
				uninstall: true,
				uiEmbedded: true,
			},
			{
				variant: "host",
				architecture: "arm64",
				path: "/release/pixie",
				version: releaseId,
				doctor: true,
				readiness: true,
				lifecycle: true,
				uninstall: true,
				uiEmbedded: true,
			},
		],
	};
}

test("package fixtures cover both archives, unit choices, config and lifecycle checks", () => {
		const report = inspectPackageArtifacts(passingPackage());

		expect(report.ok).toBe(true);
		expect(report.staticOk).toBe(true);
		expect(report.complete).toBe(true);
		expect(report.violations).toEqual([]);
		expect(report.facts.expectedArchives).toHaveLength(4);
		expect(report.facts.unitFiles).toEqual(["pixie-assistant.service", "pixie.service"]);
});

test("package checks reject split services, secret config and incomplete archive contents", () => {
	const input = passingPackage();
	const report = inspectPackageArtifacts({
		...input,
		archives: [
			{
				name: `pixie-${releaseId}-linux-amd64.tar.gz`,
				entries: ["pixie", "pixie-assistant.service", "web/index.html"],
			},
		],
		units: {
			"package/systemd/pixie-assistant.service": assistantUnit,
			"package/systemd/pixie.service": `${hostUnit}\nRequires=pixie-assistant.service\n`,
		},
		configs: {
			"package/systemd/assistant.json": '{"PIXIE_MCP_TOKEN":"not-for-an-example"}',
			"package/systemd/pixie.json": "not-json",
		},
		binaries: input.binaries === undefined ? [] : input.binaries.slice(0, 1),
	});

	expect(report.ok).toBe(false);
	const output = formatPackageArtifactReport(report);
	expect(output).toMatch(/full-host unit must not depend/);
	expect(output).toMatch(/secret-shaped key/);
	expect(output).toMatch(/missing pixie\.json configuration example|not valid JSON/);
	expect(output).toMatch(/missing live artifact evidence/);
	expect(output).toMatch(/web asset directory/);
});

test("checked-in package reports static gaps and missing live artifacts without claiming PKG-01", async () => {
	const report = inspectPackageArtifacts(await collectPackageArtifactInput());
	const output = formatPackageArtifactReport(report);

	expect(report.ok).toBe(false);
	expect(report.staticOk).toBe(true);
	expect(report.complete).toBe(false);
	expect(output).toContain("check-package-artifacts: FAILED");
	expect(output).toMatch(/missing live artifact evidence/);
	expect(output).toMatch(/four commit-named host archives/);
	expect(report.facts.unitFiles).toEqual(["pixie-assistant.service", "pixie.service"]);
});
