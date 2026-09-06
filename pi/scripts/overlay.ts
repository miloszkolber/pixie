import { createHash } from "node:crypto";
import { link, lstat, mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { createRequire } from "node:module";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import profiles from "../config/profiles.json";

export const root = fileURLToPath(new URL("../../", import.meta.url));
const stateName = "pixie-overlay.json";
const marker = "// SIGNET_MANAGED_PI_EXTENSION\n";
type Profile = keyof typeof profiles;
type State = { owner: "pixie-overlay"; version: 1; profile: Profile; signetSha256: string | null };
const hash = (value: string) => createHash("sha256").update(value).digest("hex");

export function profileFor(name: string): Profile {
	if (!Object.hasOwn(profiles, name))
		throw new Error(`Unknown profile ${name}. Choose ${Object.keys(profiles).join(", ")}`);
	return name as Profile;
}

export function agentPath(value: string): string {
	if (!isAbsolute(value) || value.trim() !== value || resolve(value) === "/") {
		throw new Error("--agent-dir must be an explicit absolute, non-root directory");
	}
	return resolve(value);
}

// Reject symlinked destinations and ancestors rather than writing through them.
async function safePath(path: string): Promise<void> {
	const parent = dirname(path);
	if (parent !== path) await safePath(parent);
	try {
		const stat = await lstat(path);
		if (stat.isSymbolicLink()) throw new Error(`Refusing symlink: ${path}`);
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
	}
}

async function optionalRead(path: string): Promise<string | null> {
	await safePath(path);
	try {
		if (!(await lstat(path)).isFile()) throw new Error(`Expected regular file: ${path}`);
		return await readFile(path, "utf8");
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
		throw error;
	}
}

async function publish(path: string, content: string, replace = false): Promise<void> {
	await safePath(path);
	await mkdir(dirname(path), { recursive: true, mode: 0o700 });
	const temporary = `${path}.${crypto.randomUUID()}.tmp`;
	try {
		await writeFile(temporary, content, { flag: "wx", mode: 0o600 });
		if (replace) await rename(temporary, path);
		else await link(temporary, path); // Atomic create, never clobber a concurrent writer.
	} finally {
		await rm(temporary, { force: true });
	}
}

async function readState(agentDir: string): Promise<State | null> {
	const text = await optionalRead(join(agentDir, stateName));
	if (text === null) return null;
	const value = JSON.parse(text);
	if (
		value?.owner !== "pixie-overlay" ||
		value.version !== 1 ||
		Object.keys(value).sort().join(",") !== "owner,profile,signetSha256,version" ||
		!(
			value.signetSha256 === null ||
			(typeof value.signetSha256 === "string" && /^[a-f0-9]{64}$/.test(value.signetSha256))
		)
	)
		throw new Error(`Unrecognized ${stateName}. Refusing to overwrite it`);
	profileFor(value.profile);
	return value;
}

export async function checkDependencies(checkRuntime = true): Promise<void> {
	const manifest = await Bun.file(join(root, "package.json")).json();
	if (checkRuntime && manifest.packageManager !== `bun@${Bun.version}`) {
		throw new Error(`Use ${manifest.packageManager}, running bun@${Bun.version}`);
	}
	for (const workspace of ["host", "mcp"]) {
		const path = join(root, "pi", workspace, "package.json");
		const { dependencies } = await Bun.file(path).json();
		for (const [name, version] of Object.entries(dependencies) as [string, string][]) {
			if (version.startsWith("workspace:")) continue;
			let entry: string;
			try {
				entry = import.meta.resolve(`${name}/package.json`, path);
			} catch {
				entry = import.meta.resolve(name, path);
			}
			let directory = dirname(fileURLToPath(entry));
			while (true) {
				const candidate = Bun.file(join(directory, "package.json"));
				if (await candidate.exists()) {
					const installed = await candidate.json();
					if (installed.name === name) {
						if (installed.version !== version)
							throw new Error(
								`${name}: expected ${version}, found ${installed.version}. Run install`,
							);
						break;
					}
				}
				const parent = dirname(directory);
				if (parent === directory) throw new Error(`Cannot validate ${name}. Run install`);
				directory = parent;
			}
		}
	}
	// Resolve exactly as the host does. A version match alone cannot detect an unpatched install.
	const adapter = createRequire(join(root, "pi/host/package.json"))("pi-mcp-adapter");
	for (const api of ["readMcpResourceV1", "callMcpAppToolV1"]) {
		if (typeof adapter[api] !== "function") {
			throw new Error(
				`pi-mcp-adapter: missing ${api}. Run install with the root patchedDependencies patch`,
			);
		}
	}
}

export async function generateSignet(): Promise<string> {
	const stage = await mkdtemp(join(tmpdir(), "pixie-signet-"));
	try {
		// No inherited Signet keys: the connector embeds credentials and endpoint defaults.
		// HOME isolates its legacy-copy cleanup. XDG_CONFIG_HOME isolates pi.json writes.
		const child = Bun.spawn(
			[process.execPath, join(root, "pi/scripts/signet-installer.ts"), stage],
			{
				env: {
					HOME: stage,
					XDG_CONFIG_HOME: `${stage}/config`,
					PI_CODING_AGENT_DIR: `${stage}/agent`,
					PIXIE_SIGNET_STAGING: "1",
				},
				stdout: "pipe",
				stderr: "pipe",
				timeout: 30_000,
			},
		);
		const [code, , errors] = await Promise.all([
			child.exited,
			new Response(child.stdout).text(),
			new Response(child.stderr).text(),
		]);
		if (code !== 0) throw new Error(`Signet installer failed: ${errors}`);
		const content = await readFile(join(stage, "agent/extensions/signet-pi.js"), "utf8");
		if (!content.startsWith(marker) || content.includes(stage))
			throw new Error("Unexpected Signet generated output");
		return content;
	} finally {
		await rm(stage, { recursive: true, force: true });
	}
}

export async function configure(directory: string, name: string, dryRun = false): Promise<void> {
	const agentDir = agentPath(directory);
	const profile = profileFor(name);
	const previous = await readState(agentDir);
	const signet = join(agentDir, "extensions/signet-pi.js");
	let digest = previous?.signetSha256 ?? null;
	if ((profiles[profile].extensions as string[]).includes("signet")) {
		const content = await generateSignet();
		const existing = await optionalRead(signet);
		if (existing !== null && existing !== content) {
			throw new Error(
				`Preserving existing ${signet}. It differs from the pinned generated extension. Review and move it manually before configuring`,
			);
		}
		digest = hash(content);
		if (existing === null && !dryRun) await publish(signet, content);
	}
	const state: State = { owner: "pixie-overlay", version: 1, profile, signetSha256: digest };
	if (!dryRun && JSON.stringify(state) !== JSON.stringify(previous)) {
		await publish(
			join(agentDir, stateName),
			`${JSON.stringify(state, null, 2)}\n`,
			previous !== null,
		);
	}
}

export async function verify(directory: string): Promise<State> {
	const agentDir = agentPath(directory);
	const state = await readState(agentDir);
	if (!state) throw new Error("Overlay is not configured. Run configure with --profile");
	if ((profiles[state.profile].extensions as string[]).includes("signet") || state.signetSha256) {
		const content = await optionalRead(join(agentDir, "extensions/signet-pi.js"));
		if (!content?.startsWith(marker) || hash(content) !== state.signetSha256) {
			throw new Error("Managed Signet extension missing or changed. Review it and rerun configure");
		}
		if (hash(await generateSignet()) !== state.signetSha256) {
			throw new Error(
				"Managed Signet extension differs from the installed connector. Review it and rerun configure",
			);
		}
	}
	return state;
}

export async function main(args: string[]): Promise<void> {
	const { values, positionals } = parseArgs({
		args,
		strict: true,
		allowPositionals: true,
		options: {
			"agent-dir": { type: "string" },
			profile: { type: "string" },
			"dry-run": { type: "boolean" },
		},
	});
	const command = positionals[0] ?? "";
	if (
		positionals.length !== 1 ||
		!["install", "configure", "verify", "start"].includes(command ?? "")
	) {
		throw new Error(
			"Usage: bun pi/scripts/overlay.ts install|configure|verify|start [--agent-dir /absolute/path] [--profile name] [--dry-run]",
		);
	}
	if (command !== "configure" && values.profile !== undefined)
		throw new Error("--profile is only valid for configure");
	if (command === "configure" && !values.profile) throw new Error("configure requires --profile");
	if (values["dry-run"] && !["install", "configure"].includes(command))
		throw new Error("--dry-run is only valid for install or configure");
	if (command === "install" && values["agent-dir"] !== undefined)
		throw new Error("install changes workspace dependencies only, not --agent-dir");
	const agentDir = command === "install" ? "" : agentPath(values["agent-dir"] ?? "");
	if (values.profile) profileFor(values.profile);
	const manifest = await Bun.file(join(root, "package.json")).json();
	if (manifest.packageManager !== `bun@${Bun.version}`)
		throw new Error(`Use ${manifest.packageManager}, running bun@${Bun.version}`);
	if (command === "install") {
		const args = [
			process.execPath,
			"install",
			"--frozen-lockfile",
			"--production",
			"--filter",
			"@pixie/pi-host",
			"--filter",
			"@pixie/pi-mcp",
		];
		if (values["dry-run"]) console.log(JSON.stringify({ cwd: root, args }));
		else {
			const code = await Bun.spawn(args, {
				cwd: root,
				stdin: "inherit",
				stdout: "inherit",
				stderr: "inherit",
			}).exited;
			if (code !== 0) throw new Error(`Locked install failed (${code})`);
			await checkDependencies();
		}
		return;
	}
	await checkDependencies();
	if (command === "configure") {
		await configure(agentDir, values.profile ?? "", values["dry-run"]);
		console.log(
			`${values["dry-run"] ? "Would configure" : "Configured"} ${agentDir}: ${values.profile}`,
		);
		return;
	}
	const state = await verify(agentDir);
	console.log(`${state.profile}: ${profiles[state.profile].description}`);
	if (command === "verify") return;
	if (!process.env.PIXIE_PI_SECRET_KEY?.trim())
		throw new Error("start requires PIXIE_PI_SECRET_KEY in the environment");
	// Set before importing the SDK so extensions and child sessions use the same directory.
	process.env.PI_CODING_AGENT_DIR = agentDir;
	process.argv = [process.execPath, join(root, "pi/host/src/main.ts"), "--agent-dir", agentDir];
	const extensions = profiles[state.profile].extensions.join(",");
	if (extensions) process.argv.push("--extensions", extensions);
	await import("../host/src/main.ts");
}

if (import.meta.main) {
	main(process.argv.slice(2)).catch((error) => {
		console.error(error instanceof Error ? error.message : String(error));
		process.exitCode = 1;
	});
}
