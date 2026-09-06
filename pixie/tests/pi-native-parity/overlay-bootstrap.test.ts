import { afterEach, expect, test } from "bun:test";
import { copyFile, mkdir, mkdtemp, readFile, rm, stat, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import profiles from "../../../pi/config/profiles.json";
import {
	agentPath,
	checkDependencies,
	configure,
	main,
	profileFor,
	root,
	verify,
} from "../../../pi/scripts/overlay.ts";

const temporary: string[] = [];
async function fixture() {
	const path = await mkdtemp(join(tmpdir(), "pixie-overlay-test-"));
	temporary.push(path);
	return path;
}
afterEach(async () => {
	await Promise.all(temporary.splice(0).map((path) => rm(path, { recursive: true, force: true })));
});

test("bootstrap preserves native configuration and agents and repeats without rewrites", async () => {
	const home = await fixture();
	const dir = join(home, "selected-agent");
	await mkdir(join(dir, "agents"), { recursive: true });
	await mkdir(join(dir, "extensions"));
	const native = {
		"settings.json":
			'{"defaultProvider":"fixture","defaultModel":"unchanged","extensions":["custom.ts"]}\n',
		"auth.json": '{"fixture":{"type":"api_key","key":"test-only"}}\n',
		"models.json": '{"providers":{"fixture":{}}}\n',
		"agents/custom.md": "---\nname: custom\ndescription: Keep me\n---\nExisting instructions\n",
		"extensions/signet.json": '{"enabled":false}\n',
		"trusted-projects.json": '{"projects":[]}\n',
	};
	for (const [path, content] of Object.entries(native)) await writeFile(join(dir, path), content);
	await configure(dir, "overlay");
	expect((await verify(dir)).profile).toBe("overlay");
	const paths = ["pixie-overlay.json", "extensions/signet-pi.js"];
	const before = await Promise.all(paths.map((path) => stat(join(dir, path))));
	await configure(dir, "overlay");
	for (const [index, path] of paths.entries()) {
		const after = await stat(join(dir, path));
		expect(after.ino).toBe(before[index]?.ino);
		expect(after.mtimeMs).toBe(before[index]?.mtimeMs);
		expect(after.mode & 0o777).toBe(0o600);
	}
	for (const [path, content] of Object.entries(native))
		expect(await readFile(join(dir, path), "utf8")).toBe(content);
	await configure(dir, "baseline");
	expect((await verify(dir)).profile).toBe("baseline");
	expect(await Bun.file(join(dir, "extensions/signet-pi.js")).exists()).toBe(true);
});

test("Signet bootstrap cannot clean other agent directories or persist installer config", async () => {
	const home = await fixture();
	const legacy = join(home, ".pi/agent/extensions");
	const config = join(home, ".config/signet");
	await mkdir(legacy, { recursive: true });
	await mkdir(config, { recursive: true });
	await writeFile(join(legacy, "signet-pi.js"), "// SIGNET_MANAGED_PI_EXTENSION\nlegacy sentinel");
	await writeFile(join(config, "pi.json"), JSON.stringify({ agentDir: join(home, ".pi/agent") }));
	const script = join(import.meta.dir, "../../../pi/scripts/overlay.ts");
	const agentDir = join(home, "chosen");
	const child = Bun.spawn(
		[
			process.execPath,
			"--eval",
			`const {configure}=await import(${JSON.stringify(script)}); await configure(${JSON.stringify(agentDir)}, "overlay");`,
		],
		{
			env: {
				...process.env,
				HOME: home,
				XDG_CONFIG_HOME: join(home, ".config"),
				SIGNET_API_KEY: "fixture-do-not-embed",
				PI_CODING_AGENT_DIR: join(home, ".pi/agent"),
			},
			stdout: "pipe",
			stderr: "pipe",
		},
	);
	const [code, errors] = await Promise.all([child.exited, new Response(child.stderr).text()]);
	expect(errors).toBe("");
	expect(code).toBe(0);
	expect(await readFile(join(legacy, "signet-pi.js"), "utf8")).toContain("legacy sentinel");
	expect(JSON.parse(await readFile(join(config, "pi.json"), "utf8"))).toEqual({
		agentDir: join(home, ".pi/agent"),
	});
	expect(await readFile(join(agentDir, "extensions/signet-pi.js"), "utf8")).not.toContain(
		"fixture-do-not-embed",
	);
});

test("dry run makes no selected-agent writes, drift fails verification, and conflicts survive", async () => {
	const home = await fixture();
	const dir = join(home, "new-agent");
	await configure(dir, "overlay", true);
	expect(await Bun.file(join(dir, "pixie-overlay.json")).exists()).toBe(false);
	expect(await Bun.file(join(dir, "extensions/signet-pi.js")).exists()).toBe(false);
	await configure(dir, "overlay");
	await writeFile(join(dir, "extensions/signet-pi.js"), "user extension");
	await expect(verify(dir)).rejects.toThrow("missing or changed");
	await expect(configure(dir, "overlay")).rejects.toThrow("Preserving existing");
	expect(await readFile(join(dir, "extensions/signet-pi.js"), "utf8")).toBe("user extension");
	await writeFile(join(dir, "pixie-overlay.json"), '{"user":"owned"}');
	await expect(configure(dir, "baseline")).rejects.toThrow("Refusing to overwrite");
});

test("validation rejects unknown profiles, unsafe paths, symlinks and inappropriate arguments", async () => {
	const home = await fixture();
	expect(() => profileFor("toString")).toThrow("Unknown profile");
	expect(() => agentPath("relative")).toThrow("absolute");
	await symlink(home, join(home, "linked"));
	await expect(configure(join(home, "linked/agent"), "baseline")).rejects.toThrow("symlink");
	await expect(verify(join(home, "absent"))).rejects.toThrow("not configured");
	for (const args of [
		["configure", "--wat"],
		["verify", "extra"],
		["install", "--profile", "baseline"],
		["configure"],
		["start", "--dry-run"],
		["verify", "--agent-dir", "relative"],
	]) {
		await expect(main(args)).rejects.toThrow();
	}
});

test("installed workspace dependencies match authoritative direct pins", async () => {
	// Toolchain enforcement remains a CLI concern so this focused test can diagnose an older Bun.
	await checkDependencies(false);
});

test("both MCP profile aliases configure and verify without changing native settings", async () => {
	const home = await fixture();
	const settings = '{"extensions":["user-extension.ts"],"defaultProvider":"fixture"}\n';
	await writeFile(join(home, "settings.json"), settings);
	for (const name of ["overlay", "adapter-evaluation"] as const) {
		await configure(home, name);
		expect((await verify(home)).profile).toBe(name);
		expect(await readFile(join(home, "settings.json"), "utf8")).toBe(settings);
		const selected = profiles[name].extensions.filter((item) =>
			["mcp", "pi-mcp-adapter"].includes(item),
		);
		expect(selected).toEqual([name === "overlay" ? "mcp" : "pi-mcp-adapter"]);
	}
});

test("dependency verification rejects either missing patched MCP API", async () => {
	for (const missing of ["readMcpResourceV1", "callMcpAppToolV1"]) {
		const child = Bun.spawn(
			[
				process.execPath,
				"--eval",
				`
			import {mock} from "bun:test";
			import {createRequire} from "node:module";
			const require = createRequire(${JSON.stringify(join(root, "pi/host/package.json"))});
			mock.module(require.resolve("pi-mcp-adapter"), () => ({
				${missing === "readMcpResourceV1" ? "callMcpAppToolV1" : "readMcpResourceV1"}: () => {}
			}));
			const {checkDependencies} = await import(${JSON.stringify(join(root, "pi/scripts/overlay.ts"))});
			await checkDependencies();
		`,
			],
			{ stdout: "pipe", stderr: "pipe", timeout: 30_000 },
		);
		const [code, errors] = await Promise.all([child.exited, new Response(child.stderr).text()]);
		expect(code).not.toBe(0);
		expect(errors).toContain(`missing ${missing}`);
	}
});

test("bootstrap installs the pinned MCP patch from a fresh frozen production workspace", async () => {
	const home = await fixture();
	const checkout = join(home, "checkout");
	const manifest = await Bun.file(join(root, "package.json")).json();
	expect(`bun@${Bun.version}`).toBe(manifest.packageManager);
	const files = [
		"package.json",
		"bun.lock",
		"pi/scripts/overlay.ts",
		"pi/scripts/signet-installer.ts",
		"pi/config/profiles.json",
		...manifest.workspaces.packages.map((path: string) => `${path}/package.json`),
		...(Object.values(manifest.patchedDependencies) as string[]),
	];
	for (const path of files) {
		await mkdir(dirname(join(checkout, path)), { recursive: true });
		await copyFile(join(root, path), join(checkout, path));
	}
	const lock = await readFile(join(checkout, "bun.lock"), "utf8");
	const script = join(checkout, "pi/scripts/overlay.ts");
	const dir = join(home, "agent");
	for (const args of [
		["install"],
		["configure", "--agent-dir", dir, "--profile", "baseline"],
		["verify", "--agent-dir", dir],
	]) {
		const child = Bun.spawn([process.execPath, script, ...args], {
			cwd: home,
			env: {
				...process.env,
				HOME: home,
				XDG_CONFIG_HOME: join(home, "config"),
				PI_CODING_AGENT_DIR: dir,
			},
			stdout: "pipe",
			stderr: "pipe",
			timeout: 120_000,
		});
		const [code, stdout, stderr] = await Promise.all([
			child.exited,
			new Response(child.stdout).text(),
			new Response(child.stderr).text(),
		]);
		if (code !== 0) throw new Error(`${args[0]} failed (${code}): ${stdout}\n${stderr}`);
	}
	expect(await readFile(join(checkout, "bun.lock"), "utf8")).toBe(lock);
	expect((await Bun.file(join(dir, "pixie-overlay.json")).json()).profile).toBe("baseline");
}, 180_000);

test("vanilla Pi discovers the generated extension from the selected directory without extra packages", async () => {
	const home = await fixture();
	const dir = join(home, "agent");
	const cwd = join(home, "project");
	await mkdir(cwd);
	await configure(dir, "overlay");
	const sdk = import.meta.resolve("@earendil-works/pi-coding-agent");
	const child = Bun.spawn(
		[
			process.execPath,
			"--eval",
			`
		const {DefaultResourceLoader} = await import(${JSON.stringify(sdk)});
		const loader = new DefaultResourceLoader({agentDir: ${JSON.stringify(dir)}, cwd: ${JSON.stringify(cwd)}});
		await loader.reload();
		const result = loader.getExtensions();
		console.log(JSON.stringify({errors: result.errors, tools: result.extensions.flatMap(extension => [...extension.tools.keys()])}));
	`,
		],
		{
			env: {
				HOME: home,
				XDG_CONFIG_HOME: join(home, "config"),
				PI_CODING_AGENT_DIR: dir,
				PI_OFFLINE: "1",
				SIGNET_BYPASS: "1",
				SIGNET_ENABLED: "true",
			},
			stdout: "pipe",
			stderr: "pipe",
			timeout: 30_000,
		},
	);
	const [code, output, errors] = await Promise.all([
		child.exited,
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
	]);
	expect(errors).toBe("");
	expect(code).toBe(0);
	const result = JSON.parse(output);
	expect(result.errors).toEqual([]);
	expect(result.tools.sort()).toEqual([
		"signet_recall",
		"signet_remember",
		"signet_session_search",
		"signet_source_search",
	]);
});
