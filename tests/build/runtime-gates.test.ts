import { expect, test } from "bun:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
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

test("checked-in runtime composition keeps the controller image and Pi-bearing archives distinct", async () => {
	const dockerfile = await readFile(join(packageRoot, "Dockerfile"), "utf8");
	const runtime = await readFile(join(packageRoot, "cmd/runtime.go"), "utf8");
	const main = await readFile(join(packageRoot, "cmd/main.go"), "utf8");
	const host = await readFile(join(packageRoot, "cmd/pixie/main.go"), "utf8");
	const releaseRuntime = await readFile(join(packageRoot, "scripts/release-runtime.ts"), "utf8");

	expect(dockerfile).toContain("go build -trimpath -tags=controller");
	expect(dockerfile).not.toContain("pixie/assistant");
	expect(dockerfile).toContain(
		'ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/pixie_web", "serve", "--mode", "controller"]',
	);
	expect(dockerfile).toContain("COPY --from=web-build /work/webui/dist /app/web");
	expect(dockerfile).not.toContain("FROM controller-runtime AS pixie\n");
	expect(dockerfile).not.toContain("/app/libexec/pixie_full");

	expect(runtime).toContain("const applicationDrainTimeout = 25 * time.Second");
	expect(runtime).toContain("rejectControllerAssistantSettings");
	expect(main).toContain("mode != modeController");
	expect(host).toContain("runtime/bin/bun");
	expect(host).toContain("dist/bun/cli.js");
	expect(host).toContain("Pi self-update is disabled");
	expect(host).toContain("pixie serve --config ABS");
	expect(host).toContain("PIXIE_PI_SECRET_KEY must be inherited");
	expect(releaseRuntime).toContain('BUNDLED_BUN_VERSION = "1.4.0"');
	expect(releaseRuntime).not.toContain("BUNDLED_NODE_VERSION");
	expect(releaseRuntime).toContain("stageBundledPiRuntime");
	expect(releaseRuntime).toContain("runtime staging must not include a Pi RPC surface");
	expect(releaseRuntime).toContain("runtime staging must not expose a package-manager executable");
});

test("controller executable fixtures exercise mode boundaries without live Pi claims", async () => {
	const temporary = await mkdtemp(join(repositoryRoot, ".pixie-runtime-gates-"));
	try {
		const controllerBinary = join(temporary, "pixie");
		const buildEnvironment = {
			CGO_ENABLED: "0",
			GOCACHE: join(temporary, "go-build"),
			GOTMPDIR: temporary,
			TMPDIR: temporary,
		};
		const controllerBuild = run(
			["go", "build", "-trimpath", "-tags=controller", "-o", controllerBinary, "./cmd"],
			buildEnvironment,
		);
		expect(controllerBuild.exitCode).toBe(0);

		const version = run([controllerBinary, "--version"]);
		expect(version.exitCode).toBe(0);
		expect(version.stdout).toMatch(/^pixie_web \S+ \(revision \S+\)\s*$/);
		const doctor = run([controllerBinary, "doctor"]);
		expect(doctor.exitCode).toBe(0);
		// The bounded recovery report is multi-line and environment-dependent; assert
		// the stable shape and the always-present readable-configuration fact rather
		// than a frozen string.
		expect(doctor.stdout).toMatch(/^pixie_web doctor: summary=\S+/);
		expect(doctor.stdout).toContain("pixie_web doctor: config.readable=ok");
		for (const line of doctor.stdout.trimEnd().split("\n")) {
			expect(line).toMatch(/^pixie_web doctor: /);
		}
		const uninstall = run([controllerBinary, "uninstall"]);
		expect(uninstall.exitCode).toBe(0);
		expect(uninstall.stdout).toBe(
			"pixie_web uninstall: stop and remove the selected user unit and binary\n",
		);

		const controllerRejectsFullHost = run([
			controllerBinary,
			"serve",
			"--mode=full-host",
			"--config",
			"/tmp/pixie.json",
		]);
		expect(controllerRejectsFullHost.exitCode).not.toBe(0);
		expect(output(controllerRejectsFullHost)).toContain("unsupported serve mode");
		expect(output(controllerRejectsFullHost)).toContain('"component":"pixie_web"');
		expect(output(controllerRejectsFullHost)).not.toContain('"component":"pixie"');
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

		const embeddedUi = text(await readFile(controllerBinary));
		expect(embeddedUi).toContain("<title>pixie</title>");
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
}, 120_000);
