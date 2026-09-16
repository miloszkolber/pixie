import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { runBoundaryCheck } from "../../scripts/check-boundaries.ts";
import { runConventionsCheck } from "../../scripts/check-conventions.ts";
import { classify, classifyExitCode } from "../../scripts/check-runtime.ts";

// AUX-22/AUX-09/AUX-10/AUX-35 seeded regressions. Each M0 gate owns a
// fail/pass distinction that the plain tree run cannot prove: the gate must
// reject one temporary violation and still accept the real tree. Temporary
// state lives under the host temp directory, never inside the repository, and
// every probe is bounded and deterministic.

const scriptsDir = resolve(import.meta.dir, "../../scripts");
const repositoryRoot = resolve(import.meta.dir, "../..");

interface ScriptResult {
	readonly exitCode: number;
	readonly stdout: string;
	readonly stderr: string;
}

function runScript(name: string, args: readonly string[]): ScriptResult {
	const result = Bun.spawnSync([process.execPath, join(scriptsDir, name), ...args], {
		cwd: repositoryRoot,
		stdout: "pipe",
		stderr: "pipe",
	});
	return {
		exitCode: result.exitCode,
		stdout: new TextDecoder().decode(result.stdout),
		stderr: new TextDecoder().decode(result.stderr),
	};
}

async function withTempDir<Result>(
	prefix: string,
	body: (dir: string) => Promise<Result>,
): Promise<Result> {
	const dir = await mkdtemp(join(tmpdir(), prefix));
	try {
		return await body(dir);
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
}

test("boundary gate fails on a seeded forbidden edge and passes on the tree", async () => {
	await withTempDir("pixie-checks-boundary-", async (dir) => {
		for (const root of ["assistant", "shared", "web"]) {
			await mkdir(join(dir, root), { recursive: true });
		}
		await writeFile(join(dir, "package.json"), "{}\n");
		// web -> assistant is a forbidden production edge (only web -> shared,
		// web is allowed). The assistant package name resolves to the assistant
		// root, so the seeded import must fail the gate.
		await writeFile(
			join(dir, "web", "bad.ts"),
			'import value from "@pixie_ai/pixie-assistant";\nvoid value;\n',
		);

		const seeded = await runBoundaryCheck({ root: dir });
		expect(seeded).toBe(1);

		const tree = await runBoundaryCheck();
		expect(tree).toBe(0);
	});
});

test("conventions gate fails on a seeded rule violation and passes on the tree", async () => {
	await withTempDir("pixie-checks-conventions-", async (dir) => {
		const source = join(dir, "web", "webui", "src");
		await mkdir(source, { recursive: true });
		// array-filter-map is a portable anti-slop rule from the AUX-09 subset.
		await writeFile(
			join(source, "bad.ts"),
			"const values = [1, 2, 3].filter((n) => n > 1).map((n) => n * 2);\nexport default values;\n",
		);
		const boundaries = join(dir, "boundaries.json");
		await writeFile(
			boundaries,
			`${JSON.stringify({
				schemaVersion: 1,
				boundaryRules: ["unknown-parameters", "unknown-returns"],
				boundaryRoots: ["assistant/src", "shared/src"],
				baseline: {},
			})}\n`,
		);

		const seeded = await runConventionsCheck({ root: dir, boundariesPath: boundaries });
		expect(seeded).toBe(1);

		const tree = await runConventionsCheck();
		expect(tree).toBe(0);
	});
});

test("config-schema --check accepts only a matching temporary artifact", async () => {
	await withTempDir("pixie-checks-schema-", async (dir) => {
		const stale = join(dir, "stale.json");
		await writeFile(stale, "{}\n");
		const staleRun = runScript("generate-config-schema.ts", ["--check", "--artifact", stale]);
		expect(staleRun.exitCode).toBe(1);
		expect(staleRun.stderr).toContain("is stale");

		// Generate the authoritative document and check it through a temporary
		// path so the committed docs/config-schema.json is never touched.
		const printed = runScript("generate-config-schema.ts", ["--print"]);
		expect(printed.exitCode).toBe(0);
		const matching = join(dir, "matching.json");
		await writeFile(matching, printed.stdout);
		const matchingRun = runScript("generate-config-schema.ts", ["--check", "--artifact", matching]);
		expect(matchingRun.exitCode).toBe(0);

		const tree = runScript("generate-config-schema.ts", ["--check"]);
		expect(tree.exitCode).toBe(0);
	});
});

test("runtime classifier blocks an unsupported Bun and a blocked temporary workspace", async () => {
	await withTempDir("pixie-checks-runtime-", async (dir) => {
		const path = process.env.PATH ?? "";

		const unsupported = classify([], {
			actualBun: "0.0.1",
			expectedBun: "1.4.0",
			workspaceRoot: dir,
			path,
			cgoEnabled: "0",
		});
		expect(unsupported.status).toBe("environment-blocked");
		expect(unsupported.checks.find((check) => check.check === "bun-runtime")?.status).toBe(
			"blocked",
		);
		expect(classifyExitCode(unsupported)).toBe(3);

		// A workspace whose parent is a regular file cannot be created, so the
		// temp-workspace check must classify the run as environment-blocked
		// rather than silently falling back.
		const notADirectory = join(dir, "not-a-directory");
		await writeFile(notADirectory, "file\n");
		const blocked = classify([], {
			actualBun: "1.4.0",
			expectedBun: "1.4.0",
			workspaceRoot: join(notADirectory, "tmp"),
			path,
			cgoEnabled: "0",
		});
		expect(blocked.status).toBe("environment-blocked");
		expect(blocked.checks.find((check) => check.check === "temp-workspace")?.status).toBe(
			"blocked",
		);
		expect(classifyExitCode(blocked)).toBe(3);

		// A pinned runtime with a writable workspace and no required CGO is the
		// supported classification; the gate maps it to a zero exit.
		const supported = classify([], {
			actualBun: "1.4.0",
			expectedBun: "1.4.0",
			workspaceRoot: dir,
			path,
			cgoEnabled: "0",
		});
		expect(supported.status).toBe("supported");
		expect(classifyExitCode(supported)).toBe(0);
	});
});
