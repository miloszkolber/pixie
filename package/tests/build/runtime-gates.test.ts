import { expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const packageRoot = resolve(import.meta.dir, "../..");
const repositoryRoot = resolve(packageRoot, "..");

function text(value: Uint8Array): string {
	return new TextDecoder().decode(value);
}

function run(command: readonly string[], env: Record<string, string> = {}) {
	const result = Bun.spawnSync([...command], {
		cwd: packageRoot,
		env: { ...process.env, ...env },
		stdout: "pipe",
		stderr: "pipe",
	});
	return {
		exitCode: result.exitCode,
		stdout: text(result.stdout),
		stderr: text(result.stderr),
	};
}

function output(result: ReturnType<typeof run>): string {
	return `${result.stdout}\n${result.stderr}`;
}

test("checked-in runtime composition has explicit controller-only and drain gates", async () => {
	const dockerfile = await readFile(join(packageRoot, "Dockerfile"), "utf8");
	const fullHostUnit = await readFile(join(packageRoot, "systemd/pixie.service"), "utf8");
	const assistantUnit = await readFile(
		join(packageRoot, "systemd/pixie-assistant.service"),
		"utf8",
	);
	const runtime = await readFile(join(packageRoot, "cmd/runtime.go"), "utf8");
	const controller = await readFile(join(packageRoot, "cmd/controller.go"), "utf8");

	const finalStage = dockerfile.slice(dockerfile.lastIndexOf("FROM "));
	expect(dockerfile).toContain("go build -trimpath -tags=controller");
	expect(dockerfile).toContain("-droprequire=github.com/miloszkolber/pixie/assistant");
	expect(dockerfile).toContain("-dropreplace=github.com/miloszkolber/pixie/assistant");
	expect(finalStage).toContain(
		'ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/pixie", "serve", "--mode", "controller"]',
	);
	expect(finalStage).toContain("COPY --from=web-build /work/package/webui/dist /app/web");
	expect(finalStage).toContain("USER 1000:1000");
	expect(finalStage).not.toMatch(/(?:pixie-assistant|\bpi\s+(?:serve|--))/i);

	for (const unit of [fullHostUnit, assistantUnit]) {
		expect(unit).toContain("Type=exec");
		expect(unit).toContain("EnvironmentFile=%h/.config/pixie/pixie.env");
		expect(unit).toContain("Restart=on-failure");
		expect(unit).toContain("RestartForceExitStatus=75");
		expect(unit).toContain("TimeoutStopSec=30");
		expect(unit).toContain("KillMode=mixed");
	}
	expect(fullHostUnit).toContain("ExecStart=%h/.local/bin/pixie serve --config");
	expect(fullHostUnit).not.toContain("Requires=pixie-assistant.service");
	expect(assistantUnit).toContain("ExecStart=%h/.local/bin/pixie-assistant serve --config");

	expect(runtime).toContain("const applicationDrainTimeout = 25 * time.Second");
	expect(runtime).toContain("rejectControllerAssistantSettings");
	expect(controller).toContain("mode != modeController");
});

test("controller and full-host executable fixtures exercise mode boundaries without live Pi claims", async () => {
	const buildTempRoot = process.env.TMPDIR?.trim() || resolve(repositoryRoot, ".pixie-tmp");
	await mkdir(buildTempRoot, { recursive: true });
	const temporary = await mkdtemp(join(buildTempRoot, "pixie-runtime-gates-"));
	try {
		const fullHostBinary = join(temporary, "pixie");
		const controllerBinary = join(temporary, "pixie-controller");
		const buildEnvironment = {
			CGO_ENABLED: "0",
			GOCACHE: join(temporary, "go-build"),
			GOTMPDIR: temporary,
			TMPDIR: temporary,
		};
		const fullHostBuild = run(
			["go", "build", "-trimpath", "-o", fullHostBinary, "./cmd"],
			buildEnvironment,
		);
		expect(fullHostBuild.exitCode).toBe(0);
		const controllerBuild = run(
			["go", "build", "-trimpath", "-tags=controller", "-o", controllerBinary, "./cmd"],
			buildEnvironment,
		);
		expect(controllerBuild.exitCode).toBe(0);

		const version = run([fullHostBinary, "--version"]);
		expect(version.exitCode).toBe(0);
		expect(version.stdout).toMatch(/^pixie \S+ \(revision \S+\)\s*$/);
		const controllerVersion = run([controllerBinary, "--version"]);
		expect(controllerVersion.exitCode).toBe(0);
		expect(controllerVersion.stdout).toMatch(/^pixie \S+ \(revision \S+\)\s*$/);

		const controllerRejectsFullHost = run([
			controllerBinary,
			"serve",
			"--mode=full-host",
			"--config",
			"/tmp/pixie.json",
		]);
		expect(controllerRejectsFullHost.exitCode).not.toBe(0);
		expect(output(controllerRejectsFullHost)).toContain("controller-only build does not support");
		const duplicateMode = run([
			controllerBinary,
			"serve",
			"--mode=controller",
			"--mode=controller",
		]);
		expect(duplicateMode.exitCode).not.toBe(0);
		expect(output(duplicateMode)).toContain("--mode may only be specified once");
		const controllerRejectsLocalPiSetting = run([controllerBinary, "serve", "--mode=controller"], {
			PI_CODING_AGENT_DIR: "/tmp/pi",
		});
		expect(controllerRejectsLocalPiSetting.exitCode).not.toBe(0);
		expect(output(controllerRejectsLocalPiSetting)).toContain(
			"controller-only mode rejects local assistant setting PI_CODING_AGENT_DIR",
		);

		const embeddedUi = text(await readFile(fullHostBinary));
		expect(embeddedUi).toContain("<title>pixie</title>");

		// The shared assistant facade is intentionally unavailable in this checkout.
		// Exercise that real failure rather than claiming a live full-host service.
		const facade = await readFile(join(repositoryRoot, "assistant/host/host.go"), "utf8");
		if (facade.includes("return nil, ErrUnavailable")) {
			const unavailable = run([fullHostBinary, "serve"]);
			expect(unavailable.exitCode).not.toBe(0);
			expect(output(unavailable)).toContain("assistant engine is unavailable in this build");
		}
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
}, 120_000);
