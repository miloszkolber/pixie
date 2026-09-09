import { expect, test } from "bun:test";
import { chmod, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import {
	assistantFacade,
	describeAssistantBuild,
	describePiInstallation,
	redactAssistantConfig,
	resolveAssistantConfig,
	resolvePiExecutable,
	validateNativeArgv,
} from "../../../../assistant/src/facade.ts";
import {
	parseAssistantPort,
	validateAssistantHost,
	validateAssistantSecret,
} from "../../../../assistant/src/startup.ts";

const SECRET = "facade-regression-secret-01";

test("facade resolves CLI over env over file over defaults without creating state", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-facade-config-"));
	try {
		const agentDir = join(root, "agent");
		const resolved = resolveAssistantConfig({
			cli: { host: "localhost", port: "3284", agentDir, llama: false },
			env: {
				pixiePiSecretKey: SECRET,
				piCodingAgentDir: join(root, "env-agent"),
				assistantHost: "127.0.0.1",
				assistantPort: "9999",
			},
			file: {
				host: "127.0.0.1",
				port: 1234,
				agentDir: join(root, "file-agent"),
				llama: true,
			},
		});
		expect(resolved.hostname).toBe("localhost");
		expect(resolved.port).toBe(3284);
		expect(resolved.agentDir).toBe(resolve(agentDir));
		expect(resolved.secret).toBe(SECRET);
		expect(resolved.llama).toBe(false);
		const { readdir } = await import("node:fs/promises");
		const entries = await readdir(root).catch(() => []);
		expect(entries).toEqual([]);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("facade validation delegates to the single startup surface", () => {
	expect(assistantFacade.validateAssistantHost).toBe(validateAssistantHost);
	expect(assistantFacade.parseAssistantPort).toBe(parseAssistantPort);
	expect(assistantFacade.validateAssistantSecret).toBe(validateAssistantSecret);
	for (const value of ["0.0.0.0", "example.test", "127.0.0.1:3284"])
		expect(() => resolveAssistantConfig({ cli: { host: value }, env: { pixiePiSecretKey: SECRET } })).toThrow(
			"literal loopback host",
		);
	for (const value of ["0", "65536", "NaN"])
		expect(() =>
			resolveAssistantConfig({ cli: { port: value }, env: { pixiePiSecretKey: SECRET } }),
		).toThrow("integer from 1 to 65535");
	expect(() => resolveAssistantConfig({})).toThrow("at least 16 characters");
	expect(() => resolveAssistantConfig({ env: { pixiePiSecretKey: "short" } })).toThrow(
		"at least 16 characters",
	);
});

test("facade rejects reserved native argv including equals forms", () => {
	expect(validateNativeArgv(undefined)).toEqual([]);
	expect(validateNativeArgv([])).toEqual([]);
	expect(validateNativeArgv(["--model", "fixture:echo", "--flag", "value"])).toEqual([
		"--model",
		"fixture:echo",
		"--flag",
		"value",
	]);
	for (const reserved of [
		"--mode",
		"--mode=json",
		"--session",
		"--session=abc",
		"--resume",
		"--continue",
		"--print",
		"--no-session",
		"--cwd",
		"--cwd=/tmp",
		"-m",
		"--session-id=abc",
	]) {
		expect(() => validateNativeArgv([reserved])).toThrow("reserves");
		expect(() =>
			resolveAssistantConfig({
				cli: { piArgv: [reserved] },
				env: { pixiePiSecretKey: SECRET },
			}),
		).toThrow("reserves");
	}
	expect(() => validateNativeArgv("--mode" as unknown as string[])).toThrow("operator array");
	expect(() => validateNativeArgv(["ok", ""])).toThrow("operator array");
});

test("facade resolves independent Pi installations with explicit errors", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-facade-pi-"));
	try {
		await expect(resolvePiExecutable(join(root, "missing-pi"))).rejects.toThrow(
			"Pi executable not found",
		);
		const dirAsExecutable = join(root, "dir-pi");
		await mkdir(dirAsExecutable);
		await expect(resolvePiExecutable(dirAsExecutable)).rejects.toThrow("not a file");
		const notExecutable = join(root, "plain.txt");
		await writeFile(notExecutable, "hello");
		await chmod(notExecutable, 0o644);
		await expect(resolvePiExecutable(notExecutable)).rejects.toThrow("not executable");
		const binDir = join(root, "custom prefix with spaces", "bin");
		await mkdir(binDir, { recursive: true });
		const piPath = join(binDir, "pi");
		await writeFile(piPath, "#!/bin/sh\nexit 0\n");
		await chmod(piPath, 0o755);
		const resolved = await resolvePiExecutable(undefined, `${binDir}:/nonexistent`);
		expect(resolved.resolved).toBe(piPath);
		expect(resolved.source).toBe("path");
		const linkDir = join(root, "link-bin");
		await mkdir(linkDir);
		await symlink(piPath, join(linkDir, "pi"));
		const linked = await resolvePiExecutable(undefined, linkDir);
		expect(linked.realPath).toBe(piPath);
		const badShebang = join(binDir, "bad-pi");
		await writeFile(badShebang, "#!/nonexistent/interpreter-xyz\nexit 0\n");
		await chmod(badShebang, 0o755);
		await expect(resolvePiExecutable(badShebang)).rejects.toThrow("Pi interpreter missing");
		await expect(resolvePiExecutable(undefined, join(root, "empty-path"))).rejects.toThrow(
			"not found on PATH",
		);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("facade reports build metadata independently of native Pi", async () => {
	const build = await describeAssistantBuild();
	expect(build.protocolVersion).toBe(1);
	expect(typeof build.assistantVersion).toBe("string");
	expect(build.assistantVersion.length).toBeGreaterThan(0);
	expect(typeof build.piSdkVersion).toBe("string");
	expect(build).not.toHaveProperty("secret");
	const installation = await describePiInstallation({ pathEnv: "/nonexistent" });
	expect(["npm", "standalone", "unknown"]).toContain(installation.kind);
	if (installation.executable === null) expect(installation.executableError).toMatch(/Pi executable/);
});

test("facade redacts secrets and exposes one host surface", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-facade-redact-"));
	try {
		const resolved = resolveAssistantConfig({
			cli: { agentDir: join(root, "agent") },
			env: { pixiePiSecretKey: SECRET },
		});
		const redacted = redactAssistantConfig(resolved);
		expect(JSON.stringify(redacted)).not.toContain(SECRET);
		expect(redacted.secret).toEqual({ present: true, length: SECRET.length, redacted: "***" });
		const source = await readFile(
			resolve(import.meta.dir, "../../../../assistant/src/facade.ts"),
			"utf8",
		);
		expect(source).toContain('from "./server.ts"');
		expect(source).toContain('from "./startup.ts"');
		expect(source).not.toContain("Bun.serve");
		expect(typeof assistantFacade.startAssistantHost).toBe("function");
		expect(typeof assistantFacade.startHost).toBe("function");
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("facade host surface starts and closes without copied lifecycle", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-facade-host-"));
	try {
		const resolved = resolveAssistantConfig({
			cli: { agentDir: join(root, "agent"), port: undefined },
			env: { pixiePiSecretKey: "facade-host-lifecycle-secret" },
			file: { port: 0 },
		});
		expect(resolved.port).toBe(0);
		const host = await assistantFacade.startAssistantHost(resolved);
		try {
			expect(host.server.port).toBeGreaterThan(0);
			expect(host.capabilities.sessions).toBe(1);
		} finally {
			await host.close();
		}
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});
