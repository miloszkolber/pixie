import { expect, test } from "bun:test";
import { chmod, mkdir, mkdtemp, readdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { runAssistantDoctor } from "../../../../assistant/src/doctor.ts";
import { resolveAssistantConfig } from "../../../../assistant/src/facade.ts";

const SECRET = "doctor-regression-secret-02";

async function snapshot(root: string): Promise<string[]> {
	const entries: string[] = [];
	async function walk(dir: string): Promise<void> {
		let names: string[];
		try {
			names = await readdir(dir);
		} catch {
			return;
		}
		for (const name of names.sort()) {
			const path = join(dir, name);
			entries.push(path.slice(root.length));
			await walk(path);
		}
	}
	await walk(root);
	return entries;
}

test("doctor is read-only and distinguishes fresh native state", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-doctor-readonly-"));
	try {
		const agentDir = join(root, "agent");
		const before = await snapshot(root);
		const report = await runAssistantDoctor({
			request: { cli: { agentDir }, env: { pixiePiSecretKey: SECRET } },
			pathEnv: "/nonexistent",
		});
		const after = await snapshot(root);
		expect(after).toEqual(before);
		const agentCheck = report.checks.find((check) => check.id === "agent-dir");
		expect(agentCheck?.ok).toBe(true);
		expect(agentCheck?.detail).toMatch(/Fresh native state/);
		expect(report.redactedConfig).not.toBeNull();
		expect(JSON.stringify(report)).not.toContain(SECRET);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("doctor redacts secrets and never writes config", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-doctor-redact-"));
	try {
		const agentDir = join(root, "agent");
		await mkdir(agentDir, { recursive: true });
		const configPath = join(root, "assistant.json");
		await writeFile(configPath, JSON.stringify({ host: "127.0.0.1", port: 3284, secret: SECRET }));
		const rawBefore = await readFile(configPath, "utf8");
		const report = await runAssistantDoctor({
			request: { cli: { agentDir }, env: { pixiePiSecretKey: SECRET } },
			configPath,
			pathEnv: "/nonexistent",
		});
		expect(JSON.stringify(report)).not.toContain(SECRET);
		expect(report.redactedConfig?.secret).toEqual({
			present: true,
			length: SECRET.length,
			redacted: "***",
		});
		expect(await readFile(configPath, "utf8")).toBe(rawBefore);
		const badPath = join(root, "bad.json");
		await writeFile(badPath, "{not-json");
		const bad = await runAssistantDoctor({
			request: { cli: { agentDir }, env: { pixiePiSecretKey: SECRET } },
			configPath: badPath,
			pathEnv: "/nonexistent",
		});
		expect(bad.checks.find((check) => check.id === "config-file")?.ok).toBe(false);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("doctor never installs extensions or loads project code", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-doctor-noinstall-"));
	try {
		const agentDir = join(root, "agent");
		const project = join(root, "project");
		await mkdir(join(agentDir, "extensions"), { recursive: true });
		await mkdir(join(project, ".pi", "extensions"), { recursive: true });
		const marker = join(root, "extension-ran");
		await writeFile(
			join(project, ".pi", "extensions", "probe.js"),
			`import { appendFileSync } from "node:fs";\nappendFileSync(${JSON.stringify(marker)}, "ran\\n");\nexport default () => {};`,
		);
		const settingsPath = join(agentDir, "settings.json");
		const settings = JSON.stringify({
			packages: ["npm:@pixie-fixture/never-install-this-package@0.0.0"],
		});
		await writeFile(settingsPath, settings);
		const before = await snapshot(root);
		const report = await runAssistantDoctor({
			request: { cli: { agentDir }, env: { pixiePiSecretKey: SECRET } },
			pathEnv: "/nonexistent",
		});
		const after = await snapshot(root);
		expect(after).toEqual(before);
		expect(await readFile(settingsPath, "utf8")).toBe(settings);
		let markerExists = true;
		try {
			await readFile(marker, "utf8");
		} catch {
			markerExists = false;
		}
		expect(markerExists).toBe(false);
		expect(report.checks.find((check) => check.id === "read-only-guarantee")?.ok).toBe(true);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("doctor reports independent Pi installations with explicit errors", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-doctor-pi-"));
	try {
		const agentDir = join(root, "agent");
		await mkdir(agentDir, { recursive: true });
		const missing = await runAssistantDoctor({
			request: { cli: { agentDir }, env: { pixiePiSecretKey: SECRET } },
			piExecutable: join(root, "missing-pi"),
		});
		const piCheck = missing.checks.find((check) => check.id === "pi-installation");
		expect(piCheck?.ok).toBe(false);
		expect(piCheck?.detail).toMatch(/Pi executable not found/);
		expect(piCheck?.remediation).toMatch(/explicit piExecutable/);
		const binDir = join(root, "custom prefix", "bin");
		await mkdir(binDir, { recursive: true });
		const piPath = join(binDir, "pi");
		await writeFile(piPath, "#!/bin/sh\nexit 0\n");
		await chmod(piPath, 0o755);
		const linkDir = join(root, "links");
		await mkdir(linkDir);
		await symlink(piPath, join(linkDir, "pi"));
		const found = await runAssistantDoctor({
			request: { cli: { agentDir }, env: { pixiePiSecretKey: SECRET } },
			pathEnv: linkDir,
		});
		expect(found.checks.find((check) => check.id === "pi-installation")?.ok).toBe(true);
		expect(found.piInstallation?.executable?.realPath).toBe(piPath);
		const badShebang = join(binDir, "bad-pi");
		await writeFile(badShebang, "#!/nonexistent/interpreter-xyz\nexit 0\n");
		await chmod(badShebang, 0o755);
		const bad = await runAssistantDoctor({
			request: { cli: { agentDir }, env: { pixiePiSecretKey: SECRET } },
			piExecutable: badShebang,
		});
		expect(bad.checks.find((check) => check.id === "pi-installation")?.detail).toMatch(
			/Pi interpreter missing/,
		);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("doctor skips active probes unless explicitly enabled", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-doctor-probes-"));
	try {
		const agentDir = join(root, "agent");
		await mkdir(agentDir, { recursive: true });
		const resolved = resolveAssistantConfig({
			cli: { agentDir },
			env: { pixiePiSecretKey: SECRET },
			file: { port: 0 },
		});
		const quiet = await runAssistantDoctor({ config: resolved, pathEnv: "/nonexistent" });
		expect(quiet.checks.find((check) => check.id === "active-probes")?.detail).toMatch(/Skipped/);
		const probed = await runAssistantDoctor({
			config: resolved,
			pathEnv: "/nonexistent",
			allowActiveProbes: true,
		});
		expect(probed.checks.find((check) => check.id === "active-probes")?.ok).toBe(true);
		expect(probed.checks.find((check) => check.id === "active-probes")?.detail).not.toMatch(
			/^Skipped/,
		);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("doctor delegates to the facade and stays read-only", async () => {
	const source = await readFile(
		resolve(import.meta.dir, "../../../../assistant/src/doctor.ts"),
		"utf8",
	);
	expect(source).toContain('from "./facade.ts"');
	expect(source).not.toContain('from "./server.ts"');
	expect(source).not.toContain('from "./sessions.ts"');
	expect(source).not.toContain("startHost");
	expect(source).not.toContain("new Sessions");
	expect(source).not.toMatch(/mkdir\s*\(/);
	expect(source).not.toContain("writeFile");
	expect(source).not.toContain("proper-lockfile");
	expect(JSON.stringify(source)).not.toContain(SECRET);
});
