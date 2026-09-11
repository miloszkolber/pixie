#!/usr/bin/env bun

import { createHash } from "node:crypto";
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { writeDeterministicTarGz } from "./deterministic-tar.ts";

const SOURCE_COMMIT_PATTERN = /^[0-9a-f]{40}$/;
const ARCHITECTURES = ["amd64", "arm64"] as const;
const VARIANTS = ["assistant", "host"] as const;
type Architecture = (typeof ARCHITECTURES)[number];
type Variant = (typeof VARIANTS)[number];

interface ReleaseOptions {
	output: string;
	dryRun: boolean;
	allowDirty: boolean;
}

interface ArtifactRecord {
	variant: Variant;
	architecture: Architecture;
	binary: string;
	binaryPath: string;
	archive: string;
	archivePath: string;
	binarySha256: string;
	archiveSha256: string;
}

function usage(): never {
	console.error("usage: bun scripts/build-release.ts [--output DIR] [--dry-run] [--allow-dirty]");
	process.exit(2);
}

function parseArgs(args: readonly string[]): ReleaseOptions {
	let output = resolve(import.meta.dir, "../dist/release");
	let dryRun = false;
	let allowDirty = false;
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index];
		switch (argument) {
			case "--dry-run":
				dryRun = true;
				break;
			case "--allow-dirty":
				allowDirty = true;
				break;
			case "--output":
				if (args[index + 1] === undefined || args[index + 1]?.trim() === "") usage();
				output = resolve(args[++index] as string);
				break;
			default:
				if (argument?.startsWith("--output=")) {
					const value = argument.slice("--output=".length).trim();
					if (!value) usage();
					output = resolve(value);
					break;
				}
				usage();
		}
	}
	return { output, dryRun, allowDirty };
}

async function run(
	command: readonly string[],
	cwd: string,
	env: Record<string, string> = {},
): Promise<string> {
	const child = Bun.spawn(command, {
		cwd,
		env: { ...process.env, ...env },
		stdout: "pipe",
		stderr: "pipe",
	});
	const [stdout, stderr, exitCode] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
		child.exited,
	]);
	if (exitCode !== 0) {
		throw new Error(
			`${command[0]} ${command.slice(1).join(" ")} failed (${exitCode}): ${stderr.trim() || stdout.trim()}`,
		);
	}
	return stdout.trim();
}

async function digest(path: string): Promise<string> {
	const hash = createHash("sha256");
	hash.update(await readFile(path));
	return hash.digest("hex");
}

async function commitIdentity(
	repositoryRoot: string,
): Promise<{ sourceCommit: string; releaseId: string; commitTime: number; clean: boolean }> {
	const sourceCommit = await run(["git", "rev-parse", "--verify", "HEAD^{commit}"], repositoryRoot);
	if (!SOURCE_COMMIT_PATTERN.test(sourceCommit)) {
		throw new Error("selected source revision must be exactly 40 lowercase hexadecimal characters");
	}
	const status = await run(["git", "status", "--porcelain"], repositoryRoot);
	const commitTime = Number(
		await run(["git", "show", "-s", "--format=%ct", sourceCommit], repositoryRoot),
	);
	if (!Number.isSafeInteger(commitTime) || commitTime < 0)
		throw new Error("selected source commit has no valid timestamp");
	return {
		sourceCommit,
		releaseId: `sha-${sourceCommit.slice(0, 12)}`,
		commitTime,
		clean: status === "",
	};
}

function installInstructions(variant: Variant): string {
	const binary = variant === "assistant" ? "pixie-assistant" : "pixie";
	const unit = `${binary}.service`;
	return `# Pixie ${binary}\n\nCopy ${binary} to ~/.local/bin/${binary}, install ${unit} under ~/.config/systemd/user/, then run:\n\n    systemctl --user daemon-reload\n    systemctl --user enable --now ${unit}\n\nStop and disable the unit before replacing or removing the binary. This archive does not include native Pi state or credentials.\n`;
}

async function buildBinary(
	variant: Variant,
	architecture: Architecture,
	repositoryRoot: string,
	workRoot: string,
	releaseId: string,
	sourceCommit: string,
): Promise<string> {
	const binary = variant === "assistant" ? "pixie-assistant" : "pixie";
	const output = join(workRoot, binary);
	const moduleRoot =
		variant === "assistant" ? join(repositoryRoot, "assistant") : join(repositoryRoot, "package");
	const packagePath = variant === "assistant" ? "./cmd/pixie-assistant" : "./cmd";
	await run(
		[
			"go",
			"-C",
			moduleRoot,
			"build",
			"-trimpath",
			"-ldflags",
			`-s -w -X main.version=${releaseId} -X main.revision=${sourceCommit}`,
			"-o",
			output,
			packagePath,
		],
		repositoryRoot,
		{ GOOS: "linux", GOARCH: architecture, CGO_ENABLED: "0" },
	);
	await chmod(output, 0o755);
	return output;
}

async function archiveBinary(
	variant: Variant,
	architecture: Architecture,
	commitTime: number,
	repositoryRoot: string,
	workRoot: string,
	output: string,
	releaseId: string,
): Promise<ArtifactRecord> {
	const binary = variant === "assistant" ? "pixie-assistant" : "pixie";
	const archive = `${binary}-${releaseId}-linux-${architecture}.tar.gz`;
	const archivePath = join(output, archive);
	const unit = `${binary}.service`;
	const config = variant === "assistant" ? "assistant.json" : "pixie.json";
	const stage = join(workRoot, `${variant}-${architecture}`);
	await mkdir(stage, { recursive: true });
	await copyFile(join(workRoot, binary), join(stage, binary));
	await copyFile(join(repositoryRoot, "package/systemd", unit), join(stage, unit));
	await copyFile(join(repositoryRoot, "package/systemd", config), join(stage, config));
	await writeFile(join(stage, "INSTALL.md"), installInstructions(variant));
	await copyFile(join(repositoryRoot, "LICENSE"), join(stage, "LICENSE"));
	await copyFile(join(repositoryRoot, "NOTICE.md"), join(stage, "NOTICE.md"));
	await writeDeterministicTarGz(
		archivePath,
		[
			{ name: binary, path: join(stage, binary), mode: 0o755 },
			{ name: unit, path: join(stage, unit), mode: 0o644 },
			{ name: config, path: join(stage, config), mode: 0o644 },
			{ name: "INSTALL.md", path: join(stage, "INSTALL.md"), mode: 0o644 },
			{ name: "LICENSE", path: join(stage, "LICENSE"), mode: 0o644 },
			{ name: "NOTICE.md", path: join(stage, "NOTICE.md"), mode: 0o644 },
		],
		commitTime,
	);
	return {
		variant,
		architecture,
		binary,
		binaryPath: join(workRoot, binary),
		archive,
		archivePath,
		binarySha256: await digest(join(workRoot, binary)),
		archiveSha256: await digest(archivePath),
	};
}

async function main(): Promise<void> {
	const options = parseArgs(Bun.argv.slice(2));
	const repositoryRoot = resolve(import.meta.dir, "../..");
	const identity = await commitIdentity(repositoryRoot);
	if (!identity.clean && !options.allowDirty && !options.dryRun) {
		throw new Error(
			"release builds require a clean source tree (use --allow-dirty only for local inspection)",
		);
	}
	const plan = {
		releaseId: identity.releaseId,
		sourceCommit: identity.sourceCommit,
		cleanSourceTree: identity.clean,
		architectures: [...ARCHITECTURES],
		variants: [...VARIANTS],
		output: options.output,
	};
	if (options.dryRun) {
		console.log(
			JSON.stringify({ ...plan, mode: "validate-only", publication: "disabled" }, null, 2),
		);
		return;
	}
	await mkdir(options.output, { recursive: true });
	// The host binary embeds the real UI; never package a placeholder bundle.
	await run(["bun", "run", "build:web"], repositoryRoot);
	const workRoot = await mkdtemp(join(tmpdir(), "pixie-release-"));
	try {
		const artifacts: ArtifactRecord[] = [];
		for (const variant of VARIANTS) {
			for (const architecture of ARCHITECTURES) {
				const binaryPath = await buildBinary(
					variant,
					architecture,
					repositoryRoot,
					workRoot,
					identity.releaseId,
					identity.sourceCommit,
				);
				// Keep one staged binary name per variant while archives are assembled.
				if (binaryPath !== join(workRoot, variant === "assistant" ? "pixie-assistant" : "pixie"))
					throw new Error("release binary staging path changed unexpectedly");
				artifacts.push(
					await archiveBinary(
						variant,
						architecture,
						identity.commitTime,
						repositoryRoot,
						workRoot,
						options.output,
						identity.releaseId,
					),
				);
			}
		}
		const checksums = `${artifacts
			.flatMap((artifact) => [`${artifact.archiveSha256}  ${artifact.archive}`])
			.join("\n")}\n`;
		await writeFile(join(options.output, "checksums.txt"), checksums);
		await writeFile(
			join(options.output, "release-manifest.json"),
			`${JSON.stringify(
				{
					schemaVersion: 1,
					releaseId: identity.releaseId,
					sourceCommit: identity.sourceCommit,
					cleanSourceTree: identity.clean,
					completeSet: false,
					artifacts: artifacts.map(
						({ variant, architecture, binary, archive, binarySha256, archiveSha256 }) => ({
							variant,
							architecture,
							binary,
							archive,
							binarySha256,
							archiveSha256,
						}),
					),
					archiveHashes: Object.fromEntries(
						artifacts.map(({ archive, archiveSha256 }) => [archive, archiveSha256]),
					),
					checksumsPresent: true,
					// Registry attestations are not available during local archive staging.
					// The release workflow may set these only from verified image evidence.
					sbomPresent: false,
					provenancePresent: false,
					docker: { status: "not-built", tag: `ghcr.io/miloszkolber/pixie:${identity.releaseId}` },
					note: "Local staging metadata; registry publication, OCI digests, SBOM and provenance require the release workflow.",
				},
				null,
				2,
			)}\n`,
		);
		console.log(
			JSON.stringify(
				{ ...plan, mode: "staged", archives: artifacts.map(({ archive }) => archive) },
				null,
				2,
			),
		);
	} finally {
		await rm(workRoot, { recursive: true, force: true });
	}
}

try {
	await main();
} catch (error) {
	console.error(`build-release: ${error instanceof Error ? error.message : String(error)}`);
	process.exitCode = 1;
}
