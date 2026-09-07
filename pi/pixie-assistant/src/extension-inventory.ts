import { open } from "node:fs/promises";
import { statSync } from "node:fs";
import { join } from "node:path";
import {
	type AgentSession,
	DefaultPackageManager,
	hasTrustRequiringProjectResources,
	ProjectTrustStore,
	SettingsManager,
} from "@earendil-works/pi-coding-agent";
import { extensionResourceKey, extensionRevision } from "./extension-configuration.ts";

// Source references are display metadata, not URLs to fetch. Never expose URL
// credentials, query strings, fragments, arbitrary manifest fields or errors.
export function inventoryReference(value: string): string {
	return value.replace(/\p{Cc}/gu, "")
		.replace(/([a-z][a-z0-9+.-]*:\/\/)[^/\s]*@/gi, "$1")
		.split(/[?#]/, 1)[0].slice(0, 1024);
}

async function packageIdentity(path?: string): Promise<{ name: string | null; version: string | null }> {
	const unknown = { name: null, version: null };
	if (!path) return unknown;
	try {
		const file = await open(join(path, "package.json"), "r");
		try {
			const buffer = Buffer.alloc(65537);
			let size = 0;
			while (size < buffer.length) {
				const { bytesRead } = await file.read(buffer, size, buffer.length - size, null);
				if (!bytesRead) break;
				size += bytesRead;
			}
			if (size > 65536) return unknown;
			const data = JSON.parse(buffer.subarray(0, size).toString("utf8"));
			return {
				name: typeof data.name === "string" && /^@?[a-z0-9._/-]{1,200}$/i.test(data.name) ? data.name : null,
				version: typeof data.version === "string" && /^\d+\.\d+\.\d+(?:[-+][a-z0-9.+-]+)?$/i.test(data.version) ? data.version : null,
			};
		} finally { await file.close(); }
	} catch { return unknown; }
}

export async function extensionInventory(agentDir: string, cwd: string, session?: AgentSession,
	reader: "session" | "service" | "not-resident" | "configured-only" = "configured-only",
	sessionId: string | null = null) {
	const warnings: string[] = [];
	// A fresh settings reader observes saved configuration without changing the
	// resident session's settings or draining its diagnostics.
	const settings = SettingsManager.create(cwd, agentDir);
	if (settings.drainErrors().length) warnings.push("settings-read-failed");
	const manager = new DefaultPackageManager({ cwd, agentDir, settingsManager: settings });
	const loaded = session?.resourceLoader.getExtensions();
	let packages: ReturnType<DefaultPackageManager["listConfiguredPackages"]> = [];
	let resources: Awaited<ReturnType<DefaultPackageManager["resolve"]>>["extensions"] = [];
	try { packages = manager.listConfiguredPackages(); }
	catch { warnings.push("package-discovery-failed"); }
	try {
		// SDK 0.85.1 resolves manifests and paths only. The explicit skip callback
		// prevents installing missing or mismatched packages. No loader.reload(),
		// extension import, session binding or temporary Git refresh is involved.
		resources = (await manager.resolve(async () => "skip")).extensions;
	} catch { warnings.push("resource-discovery-failed"); }
	const limit = 500;
	const globalSettings = settings.getGlobalSettings();
	const projectSettings = settings.getProjectSettings();
	if (packages.length > limit || resources.length > limit || (loaded?.extensions.length ?? 0) > limit ||
		(loaded?.errors.length ?? 0) > limit || (globalSettings.extensions?.length ?? 0) > limit ||
		(projectSettings.extensions?.length ?? 0) > limit)
		warnings.push("inventory-truncated");
	let contributionBudget = 5000;
	const contributionNames = (keys: Iterable<string>) => {
		const names: string[] = [];
		for (const name of keys) {
			if (names.length >= limit || contributionBudget === 0) {
				if (!warnings.includes("inventory-truncated")) warnings.push("inventory-truncated");
				break;
			}
			names.push(inventoryReference(name));
			contributionBudget--;
		}
		return names;
	};
	const safeSource = (source: { source: string; scope: string; origin: string }) => ({
		source: inventoryReference(source.source), scope: source.scope, origin: source.origin,
	});
	const configuredPackages = await Promise.all(packages.slice(0, limit).map(async (pkg) => ({
		source: inventoryReference(pkg.source), scope: pkg.scope, filtered: pkg.filtered,
		installed: Boolean(pkg.installedPath), ...await packageIdentity(pkg.installedPath),
		state: !pkg.installedPath ? "missing" : !loaded ? "not-observed" :
			resources.some((resource) => resource.metadata.source === pkg.source && resource.metadata.scope === pkg.scope &&
				loaded.errors.some((error) => error.path === resource.path)) ? "failed" :
			loaded.extensions.some((extension) => extension.sourceInfo.source === pkg.source && extension.sourceInfo.scope === pkg.scope)
				? "loaded" : "not-loaded",
	})));
	const configurationSupported = (resource: typeof resources[number]) => {
		if (resource.metadata.scope !== "user" && resource.metadata.scope !== "project") return false;
		if (resource.metadata.origin === "top-level") return true;
		const installedPath = packages.find((pkg) => pkg.source === resource.metadata.source && pkg.scope === resource.metadata.scope)?.installedPath;
		try { return Boolean(installedPath && installedPath !== resource.path && statSync(installedPath).isDirectory()); }
		catch { return false; }
	};
	// Project trust decides whether project-local resources load at all, so
	// report it alongside inventory: "trusted" mirrors the native gate
	// (no trust-requiring resources, or an explicit stored trust decision).
	const trustStore = new ProjectTrustStore(agentDir);
	let trustDecision: boolean | null = null;
	try { trustDecision = trustStore.get(cwd); } catch { warnings.push("trust-read-failed"); }
	const requiresDecision = hasTrustRequiringProjectResources(cwd);
	return {
		version: 1,
		trust: {
			projectTrusted: !requiresDecision || trustDecision === true,
			decision: trustDecision,
			requiresDecision: requiresDecision && trustDecision === null,
		},
		configurationRevisions: {
			user: extensionRevision("user", globalSettings),
			project: extensionRevision("project", projectSettings),
		},
		context: { cwd: inventoryReference(cwd), sessionId, reader },
		packages: configuredPackages,
		paths: ([ ["user", globalSettings], ["project", projectSettings] ] as const)
			.flatMap(([scope, values]) => (Array.isArray(values.extensions) ? values.extensions : [])
				.filter((path): path is string => typeof path === "string").slice(0, limit)
				.map((path) => ({ path: inventoryReference(path), scope }))),
		resources: resources.slice(0, limit).map((resource) => ({
			resourceKey: extensionResourceKey(resource),
			configurationSupported: configurationSupported(resource),
			path: inventoryReference(resource.path), ...safeSource(resource.metadata), enabled: resource.enabled,
			state: !loaded ? "not-observed" : loaded.errors.some((error) => error.path === resource.path)
				? "failed" : loaded.extensions.some((extension) => (extension.resolvedPath === resource.path || extension.path === resource.path) && extension.sourceInfo.scope === resource.metadata.scope)
					? "loaded" : "not-loaded",
		})),
		extensions: (loaded?.extensions ?? []).slice(0, limit).map((extension) => ({
			path: inventoryReference(extension.path), resolvedPath: inventoryReference(extension.resolvedPath),
			source: safeSource(extension.sourceInfo),
			// SourceInfo has no loaded package version. Reading the current
			// manifest here would mislabel code loaded before a package update.
			name: null, version: null,
			tools: contributionNames(extension.tools.keys()),
			commands: contributionNames(extension.commands.keys()),
			interfaceSupport: "unknown",
		})),
		errors: (loaded?.errors ?? []).slice(0, limit).map(({ path }) => ({
			path: inventoryReference(path), code: "load-failed",
		})),
		warnings,
	};
}
