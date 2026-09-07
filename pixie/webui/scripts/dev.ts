import { type FSWatcher, watch } from "node:fs";
import { cp, mkdir, mkdtemp, readdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { buildWeb } from "./build";
import { checkArtifacts } from "./check-artifacts";

const webRoot = resolve(import.meta.dir, "..");
const projectRoot = resolve(webRoot, "..");
const contractsRoot = join(projectRoot, "contracts", "src");
const defaultPort = 24269;

export function parseDevPort(value = process.env.PIXIE_UI_DEV_PORT): number {
	if (value === undefined || value === "") return defaultPort;
	if (!/^[0-9]+$/.test(value)) throw new Error("PIXIE_UI_DEV_PORT must be an integer");
	const port = Number(value);
	if (!Number.isSafeInteger(port) || port < 1024 || port > 65_535) {
		throw new Error("PIXIE_UI_DEV_PORT must be between 1024 and 65535");
	}
	return port;
}

const reloadScript = `<script>
(() => {
  let current = "";
  const poll = async () => {
    try {
      const next = await fetch("/.pixie-dev-version", { cache: "no-store" }).then((response) => response.ok ? response.text() : "");
      if (current && next && next !== current) location.reload();
      current = next || current;
    } catch {}
    setTimeout(poll, 500);
  };
  void poll();
})();
</script>`;

async function publishBuild(stagedOutput: string, servedOutput: string): Promise<void> {
	await mkdir(servedOutput, { recursive: true });
	for (const entry of await readdir(stagedOutput, { withFileTypes: true })) {
		if (entry.name === "index.html") continue;
		await cp(join(stagedOutput, entry.name), join(servedOutput, entry.name), {
			force: true,
			recursive: entry.isDirectory(),
		});
	}

	const sourceIndex = await readFile(join(stagedOutput, "index.html"), "utf8");
	if (!sourceIndex.includes("</body>")) throw new Error("Development build is missing </body>");
	const version = `${Date.now()}-${crypto.randomUUID()}`;
	const indexTemporary = join(servedOutput, `.index-${version}.html`);
	await writeFile(indexTemporary, sourceIndex.replace("</body>", `${reloadScript}</body>`));
	await rename(indexTemporary, join(servedOutput, "index.html"));
	const versionTemporary = join(servedOutput, `.version-${version}`);
	await writeFile(versionTemporary, `${version}\n`);
	await rename(versionTemporary, join(servedOutput, ".pixie-dev-version"));
}

async function buildDevelopment(devRoot: string, servedOutput: string): Promise<void> {
	const stageRoot = await mkdtemp(join(devRoot, "stage-"));
	const stagedOutput = join(stageRoot, "dist");
	const intermediateRoot = join(stageRoot, "intermediate");
	try {
		await buildWeb({ outputRoot: stagedOutput, intermediateRoot, development: true });
		checkArtifacts(stagedOutput, join(intermediateRoot, "bundle-manifest.json"));
		await publishBuild(stagedOutput, servedOutput);
	} finally {
		await rm(stageRoot, { force: true, recursive: true });
	}
}

async function compileFixture(binary: string, cache: string): Promise<void> {
	const process = Bun.spawn(["go", "build", "-trimpath", "-o", binary, "./tests/ui/fixture"], {
		cwd: projectRoot,
		env: { ...globalThis.process.env, GOCACHE: cache },
		stdout: "inherit",
		stderr: "inherit",
	});
	if ((await process.exited) !== 0) throw new Error("Could not build the UI development fixture");
}

async function waitUntilReady(child: Bun.Subprocess, origin: string): Promise<void> {
	let exitCode: number | undefined;
	void child.exited.then((code) => {
		exitCode = code;
	});
	for (let attempt = 0; attempt < 100; attempt += 1) {
		if (exitCode !== undefined) throw new Error(`UI development fixture exited with ${exitCode}`);
		try {
			const response = await fetch(`${origin}/livez`, { cache: "no-store" });
			if (response.ok) return;
		} catch {}
		await Bun.sleep(100);
	}
	throw new Error(`UI development fixture did not become ready at ${origin}`);
}

async function stopChild(child: Bun.Subprocess): Promise<void> {
	child.kill("SIGTERM");
	const stopped = await Promise.race([
		child.exited.then(() => true),
		Bun.sleep(5_000).then(() => false),
	]);
	if (!stopped) {
		child.kill("SIGKILL");
		await child.exited;
	}
}

async function main(): Promise<void> {
	if (process.platform !== "linux") {
		throw new Error("The same-origin UI development fixture requires the supported Linux runtime");
	}
	const port = parseDevPort();
	const origin = `http://127.0.0.1:${port}`;
	const devRoot = await mkdtemp(join(tmpdir(), "pixie-web-dev-"));
	const servedOutput = join(devRoot, "web");
	const fixtureBinary = join(devRoot, "pixie-ui-fixture");
	const fixtureBase = join(devRoot, "fixture");
	const fixtureReady = join(devRoot, "ready");
	const watchers: FSWatcher[] = [];
	let child: Bun.Subprocess | undefined;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let building: Promise<void> | undefined;
	let queued = false;
	let stopping = false;

	const rebuild = (initial = false): Promise<void> => {
		if (building) {
			queued = true;
			return building;
		}
		building = (async () => {
			do {
				queued = false;
				try {
					await buildDevelopment(devRoot, servedOutput);
					console.log(`dev: published ${new Date().toLocaleTimeString()}`);
				} catch (error) {
					if (initial) throw error;
					console.error("dev: rebuild failed; continuing to serve the last successful build");
					console.error(error);
				}
			} while (queued && !stopping);
		})().finally(() => {
			building = undefined;
		});
		return building;
	};

	const scheduleRebuild = (): void => {
		if (stopping) return;
		if (timer) clearTimeout(timer);
		timer = setTimeout(() => {
			timer = undefined;
			void rebuild();
		}, 75);
	};

	try {
		await Promise.all([rebuild(true), compileFixture(fixtureBinary, join(devRoot, "go-cache"))]);
		child = Bun.spawn([fixtureBinary], {
			cwd: projectRoot,
			env: {
				...process.env,
				PIXIE_ALLOWED_ORIGINS: "",
				PIXIE_ALLOW_UNAUTHENTICATED_REMOTE: "false",
				PIXIE_AUTH_ENABLED: "false",
				PIXIE_BROWSER_AUTH: "false",
				PIXIE_BROWSER_PUBLIC_ORIGIN: "",
				PIXIE_BROWSER_TOKEN: "",
				PIXIE_CONTROLLER_HOST: "127.0.0.1",
				PIXIE_PUBLIC_ORIGIN: origin,
				PIXIE_TOKEN: "",
				PIXIE_UI_FIXTURE_BASE: fixtureBase,
				PIXIE_UI_FIXTURE_PORT: String(port),
				PIXIE_UI_FIXTURE_READY_FILE: fixtureReady,
				PIXIE_UI_STATIC_DIR: servedOutput,
			},
			stdout: "inherit",
			stderr: "inherit",
		});
		await waitUntilReady(child, origin);

		const watchWeb = watch(webRoot, { recursive: true }, (_event, filename) => {
			const path = filename?.toString().replaceAll("\\", "/");
			if (!path || path === "index.html" || path.startsWith("src/") || path.startsWith("vendor/")) {
				scheduleRebuild();
			}
		});
		const watchContracts = watch(contractsRoot, { recursive: true }, scheduleRebuild);
		watchers.push(watchWeb, watchContracts);

		console.log(`dev: Pixie UI is available at ${origin}`);
		console.log("dev: the same-origin Go fixture serves static files, HTTP APIs, and WebSockets");

		let requestStop: ((signal: NodeJS.Signals) => void) | undefined;
		const stopped = new Promise<NodeJS.Signals>((resolveStop) => {
			requestStop = resolveStop;
		});
		const onInterrupt = () => requestStop?.("SIGINT");
		const onTerminate = () => requestStop?.("SIGTERM");
		process.once("SIGINT", onInterrupt);
		process.once("SIGTERM", onTerminate);
		const event = await Promise.race([
			stopped.then((signal) => ({ kind: "signal" as const, signal })),
			child.exited.then((code) => ({ kind: "exit" as const, code })),
		]);
		process.off("SIGINT", onInterrupt);
		process.off("SIGTERM", onTerminate);
		if (event.kind === "exit") throw new Error(`UI development fixture exited with ${event.code}`);
	} finally {
		stopping = true;
		if (timer) clearTimeout(timer);
		for (const watcher of watchers) watcher.close();
		if (building) await building;
		if (child) await stopChild(child);
		await rm(devRoot, { force: true, recursive: true });
	}
}

if (import.meta.main) await main();
