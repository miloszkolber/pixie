import { createHmac, randomBytes, randomUUID } from "node:crypto";
import {
	chmodSync,
	lstatSync,
	mkdirSync,
	readFileSync,
	renameSync,
	rmSync,
	writeFileSync,
} from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { DefaultPackageManager, SettingsManager } from "@earendil-works/pi-coding-agent";
import { lockSync } from "proper-lockfile";

type Settings = ReturnType<SettingsManager["getGlobalSettings"]>;
type Resource = Awaited<ReturnType<DefaultPackageManager["resolve"]>>["extensions"][number];
export type ExtensionScope = "user" | "project";
const tokenKey = randomBytes(32);
const token = (value: unknown) =>
	createHmac("sha256", tokenKey).update(JSON.stringify(value)).digest("hex");

export function extensionRevision(scope: ExtensionScope, settings: Settings): string {
	return token([scope, settings.packages ?? [], settings.extensions ?? []]);
}
export function extensionResourceKey(resource: Resource): string {
	return token([resource.path, resource.metadata]);
}

function parseSettings(raw: string | undefined): Settings {
	const value = raw ? JSON.parse(raw.replace(/^\uFEFF/, "")) : {};
	if (!value || typeof value !== "object" || Array.isArray(value))
		throw new Error("Invalid settings");
	for (const field of ["packages", "extensions"]) {
		if (value[field] !== undefined && !Array.isArray(value[field]))
			throw new Error("Invalid settings");
	}
	return value;
}

function override(patterns: string[], path: string, base: string, enabled: boolean): string[] {
	if (patterns.some((pattern) => typeof pattern !== "string"))
		throw new Error("Invalid native resource filters");
	return [
		...patterns.filter(
			(pattern) =>
				!(
					["+", "-"].includes(pattern[0] ?? "") &&
					resolve(base, pattern.slice(1)) === resolve(base, path)
				),
		),
		`${enabled ? "+" : "-"}${path}`,
	];
}

export async function configureExtension(
	agentDir: string,
	cwd: string,
	request: {
		scope: ExtensionScope;
		resourceKey: string;
		expectedRevision: string;
		enabled: boolean;
		confirmed: boolean;
	},
) {
	if (
		!["user", "project"].includes(request.scope) ||
		typeof request.enabled !== "boolean" ||
		request.confirmed !== true
	)
		throw new Error("Confirm the scoped native configuration change");
	const source = SettingsManager.create(cwd, agentDir);
	if (source.drainErrors().length)
		throw new Error("Native settings are unreadable. No configuration saved.");
	const snapshots = { global: source.getGlobalSettings(), project: source.getProjectSettings() };
	const scope = request.scope === "user" ? "global" : "project";
	if (extensionRevision(request.scope, snapshots[scope]) !== request.expectedRevision)
		throw new Error("Native configuration changed. Refresh inventory before saving.");
	const packages = new DefaultPackageManager({ cwd, agentDir, settingsManager: source });
	const resources = (await packages.resolve(async () => "skip")).extensions;
	const beforeByKey = new Map(resources.map((item) => [extensionResourceKey(item), item]));
	const resource = beforeByKey.get(request.resourceKey);
	if (!resource || resource.metadata.scope !== request.scope)
		throw new Error("Native resource is no longer available. Refresh inventory.");
	const next = structuredClone(snapshots[scope]);
	let field: "packages" | "extensions" = "extensions";
	if (resource.metadata.origin === "package") {
		field = "packages";
		const index =
			next.packages?.findIndex(
				(entry) => (typeof entry === "string" ? entry : entry.source) === resource.metadata.source,
			) ?? -1;
		if (index < 0 || !next.packages || !resource.metadata.baseDir)
			throw new Error("Manage this resource through native Pi configuration");
		const previous = next.packages[index];
		const entry = typeof previous === "string" ? { source: previous } : previous;
		if (!entry) throw new Error("Native package changed");
		let patterns = entry.extensions ?? (entry.autoload === false ? [] : ["**"]);
		if (patterns.length === 0 && entry.autoload !== false) patterns = ["!**"];
		next.packages[index] = {
			...entry,
			extensions: override(
				patterns,
				relative(resource.metadata.baseDir, resource.path).split("\\").join("/"),
				resource.metadata.baseDir,
				request.enabled,
			),
		};
	} else {
		next.extensions = override(
			next.extensions ?? [],
			resource.path,
			request.scope === "user" ? agentDir : join(cwd, ".pi"),
			request.enabled,
		);
	}
	// Evaluate native filters without loading code or installing packages. Do
	// not guess how package-file entries or parent force-excludes behave.
	const previewSettings = SettingsManager.fromStorage({
		withLock: (target, fn) => {
			fn(JSON.stringify(target === scope ? next : snapshots[target]));
		},
	});
	const preview = (
		await new DefaultPackageManager({ cwd, agentDir, settingsManager: previewSettings }).resolve(
			async () => "skip",
		)
	).extensions;
	const afterByKey = new Map(preview.map((item) => [extensionResourceKey(item), item]));
	const changed = afterByKey.get(request.resourceKey);
	if (!changed || changed.enabled !== request.enabled)
		throw new Error("Native filters cannot apply this change. No configuration saved.");
	if (
		[...beforeByKey].some(
			([key, before]) =>
				key !== request.resourceKey && afterByKey.get(key)?.enabled !== before.enabled,
		) ||
		[...afterByKey.keys()].some((key) => key !== request.resourceKey && !beforeByKey.has(key))
	)
		throw new Error("This change would affect other resources. Use native Pi configuration.");

	let writing = false;
	let saved = false;
	const manager = SettingsManager.fromStorage({
		withLock: (target, fn) => {
			if (!writing) {
				fn(JSON.stringify(snapshots[target]));
				return;
			}
			if (target !== scope) throw new Error("Unexpected settings scope");
			const path =
				target === "global" ? join(agentDir, "settings.json") : join(cwd, ".pi", "settings.json");
			mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
			// Same proper-lockfile identity used by Pi's native FileSettingsStorage.
			const release = lockSync(path, { realpath: false });
			const temporary = `${path}.${randomUUID()}.tmp`;
			try {
				let raw: string | undefined;
				let mode = 0o600;
				try {
					const stat = lstatSync(path);
					if (!stat.isFile()) throw new Error("Settings target is not a regular file");
					mode = stat.mode & 0o777;
					raw = readFileSync(path, "utf8");
				} catch (error) {
					if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
				}
				if (extensionRevision(request.scope, parseSettings(raw)) !== request.expectedRevision)
					throw new Error("Concurrent native configuration change");
				const content = fn(raw);
				if (content === undefined) throw new Error("Native settings write was not produced");
				writeFileSync(temporary, content, { mode: 0o600, flag: "wx" });
				chmodSync(temporary, mode);
				renameSync(temporary, path);
				saved = true;
			} finally {
				rmSync(temporary, { force: true });
				release();
			}
		},
	});
	writing = true;
	if (field === "packages") {
		if (scope === "global") manager.setPackages(next.packages ?? []);
		else manager.setProjectPackages(next.packages ?? []);
	} else if (scope === "global") manager.setExtensionPaths(next.extensions ?? []);
	else manager.setProjectExtensionPaths(next.extensions ?? []);
	await manager.flush();
	const errors = manager.drainErrors();
	if (!saved)
		throw new Error(
			"Native settings save was not confirmed or conflicted. Refresh inventory before retrying.",
		);
	return {
		saved: true,
		loaded: false,
		reload: "deferred" as const,
		warning: errors.length ? "settings-cleanup-failed" : null,
	};
}
