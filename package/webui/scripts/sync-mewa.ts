import { cp, mkdir, mkdtemp, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import {
	loadMewaLock,
	type MewaAsset,
	sha256,
	verifyReleasePackage,
	verifyVendoredMewa,
} from "./mewa-integrity";

const webRoot = resolve(import.meta.dir, "..");
const vendorRoot = join(webRoot, "vendor");
const lockPath = join(vendorRoot, "mewa.lock.json");
const lock = await loadMewaLock(lockPath);

const sourceArgument = process.argv.find((argument) => argument.startsWith("--from="));
const suppliedSource = sourceArgument ? resolve(sourceArgument.slice("--from=".length)) : null;
const temporaryRoot = await mkdtemp(join(tmpdir(), "pixie-mewa-"));
const transactionRoot = await mkdtemp(join(webRoot, ".mewa-sync-"));
const archiveRoot = suppliedSource ?? join(temporaryRoot, "archives");
const extractRoot = join(temporaryRoot, "extract");
const stagedVendor = join(transactionRoot, "vendor");
const previousVendor = join(transactionRoot, "previous-vendor");
let preserveTransaction = false;

function run(command: string[], label: string): string {
	const result = Bun.spawnSync(command, { stdout: "pipe", stderr: "pipe" });
	if (result.exitCode !== 0) {
		const detail = result.stderr.toString().trim();
		throw new Error(`${label} failed${detail ? `: ${detail}` : ""}`);
	}
	return result.stdout.toString();
}

function validateArchiveListing(asset: MewaAsset, archive: string): void {
	const expectedRoot = `${asset.package}/`;
	const names = run(["tar", "-tzf", archive], `List ${asset.file}`).split("\n").filter(Boolean);
	if (names.length === 0) throw new Error(`${asset.file} is empty`);
	for (const name of names) {
		if (
			name.startsWith("/") ||
			name.includes("\\") ||
			name.split("/").includes("..") ||
			!name.startsWith(expectedRoot)
		) {
			throw new Error(`${asset.file} contains an unsafe path: ${name}`);
		}
	}
	const verbose = run(["tar", "-tvzf", archive], `Inspect ${asset.file}`);
	for (const line of verbose.split("\n")) {
		if (line.startsWith("l") || line.startsWith("h")) {
			throw new Error(`${asset.file} contains a link entry`);
		}
	}
}

try {
	await mkdir(archiveRoot, { recursive: true });
	await mkdir(extractRoot, { recursive: true });
	await mkdir(stagedVendor, { recursive: true });
	if (!suppliedSource) {
		const patterns = lock.assets.flatMap((asset) => ["--pattern", asset.file]);
		run(
			[
				"gh",
				"release",
				"download",
				lock.release,
				"--repo",
				lock.repository,
				"--dir",
				archiveRoot,
				...patterns,
			],
			"Download Mewa release",
		);
	}

	for (const asset of lock.assets) {
		const archive = join(archiveRoot, basename(asset.file));
		if ((await Bun.file(archive).size) !== asset.size || (await sha256(archive)) !== asset.sha256) {
			throw new Error(`${asset.file} does not match the locked release asset`);
		}
		validateArchiveListing(asset, archive);
		run(["tar", "-xzf", archive, "-C", extractRoot], `Extract ${asset.file}`);
		await verifyReleasePackage(join(extractRoot, asset.package), asset, lock.version);
	}

	await cp(join(extractRoot, "mewa-ui"), join(stagedVendor, "mewa-ui"), { recursive: true });
	await cp(join(extractRoot, "mewa-svelte"), join(stagedVendor, "mewa-svelte"), {
		recursive: true,
	});

	const iconSource = join(extractRoot, "mewa-icons");
	const iconTarget = join(stagedVendor, "mewa-icons");
	await mkdir(join(iconTarget, "icons"), { recursive: true });
	await mkdir(join(iconTarget, "licenses"), { recursive: true });
	for (const file of ["LICENSE", "README.md", "package.json", "manifest.json"]) {
		await cp(join(iconSource, file), join(iconTarget, file));
	}
	await cp(join(iconSource, "checksums.json"), join(iconTarget, "upstream-checksums.json"));
	await cp(
		join(iconSource, "licenses", "LUCIDE-LICENSE.txt"),
		join(iconTarget, "licenses", "LUCIDE-LICENSE.txt"),
	);
	for (const icon of lock.icons) {
		await cp(join(iconSource, "icons", `${icon}.svg`), join(iconTarget, "icons", `${icon}.svg`));
	}
	await writeFile(
		join(iconTarget, "selected.json"),
		`${JSON.stringify({ version: lock.version, icons: lock.icons }, null, "\t")}\n`,
	);
	await cp(lockPath, join(stagedVendor, "mewa.lock.json"));
	await verifyVendoredMewa(stagedVendor, lock);

	await rename(vendorRoot, previousVendor);
	try {
		await rename(stagedVendor, vendorRoot);
	} catch (replacementError) {
		try {
			await rename(previousVendor, vendorRoot);
		} catch (rollbackError) {
			preserveTransaction = true;
			throw new AggregateError(
				[replacementError, rollbackError],
				`Mewa vendor replacement failed; recover the previous tree from ${previousVendor}`,
			);
		}
		throw replacementError;
	}
	await rm(previousVendor, { force: true, recursive: true });
	console.log(
		`mewa: vendored ${lock.version} (${lock.icons.length} selected icons) from ${lock.release}`,
	);
} finally {
	await rm(temporaryRoot, { force: true, recursive: true });
	if (!preserveTransaction) await rm(transactionRoot, { force: true, recursive: true });
}
