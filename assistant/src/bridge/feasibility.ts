/**
 * Pure BRIDGE-01 feasibility checks.
 *
 * The selected Pi installation supplies all probe values.  This module never
 * imports a package, creates a native context, reads settings, or falls back
 * to a bundled SDK.  A package export list is not enough on its own: the
 * import origin, live extension context, native lock and lifecycle hooks must
 * all be verified by the caller before the administration profile is enabled.
 */

export type BridgeDistribution = "npm" | "standalone";
export type BridgeSurface = "import" | "context" | "storage" | "runtime-hooks" | "asset" | "channel";

export const BRIDGE_PROBE_VERSION = 1;
export const BRIDGE_SDK_BASELINE = "0.85.1";
export const PI_CODING_AGENT_PACKAGE = "@earendil-works/pi-coding-agent";

export interface BridgePublicExport {
	readonly symbol: string;
	readonly kind: "runtime" | "type";
	readonly purpose: string;
}

/** Public symbols needed before any administration operation can be claimed. */
export const BRIDGE_PUBLIC_EXPORTS: readonly BridgePublicExport[] = [
	{
		symbol: "ModelRuntime",
		kind: "runtime",
		purpose: "Delegate provider/auth/model administration to the selected Pi runtime.",
	},
	{
		symbol: "SettingsManager",
		kind: "runtime",
		purpose: "Use native settings ownership and locking instead of editing JSON directly.",
	},
	{
		symbol: "DefaultPackageManager",
		kind: "runtime",
		purpose: "Resolve configured native resources without installing them during inspection.",
	},
	{
		symbol: "ExtensionAPI",
		kind: "type",
		purpose: "Bind the bridge through the public extension entrypoint and event bus.",
	},
	{
		symbol: "ExtensionContext",
		kind: "type",
		purpose: "Describe the live context passed to public extension callbacks.",
	},
] as const;

/** Names observed on a live public ExtensionAPI/ExtensionContext. */
export const BRIDGE_CONTEXT_REQUIREMENTS: readonly string[] = [
	"extension.events.emit",
	"extension.events.on",
	"extension.on",
	"context.cwd",
	"context.sessionManager",
	"context.ui",
	"context.signal",
] as const;

/** Public lifecycle events needed to bind and dispose a bridge-owned context. */
export const BRIDGE_RUNTIME_HOOKS: readonly string[] = ["session_start", "session_shutdown"] as const;

/** Public settings methods and lock surface used by the bridge feasibility probe. */
export const BRIDGE_STORAGE_REQUIREMENTS: readonly string[] = [
	"SettingsManager.create",
	"SettingsManager.fromStorage",
	"SettingsManager.flush",
	"SettingsManager.reload",
	"SettingsStorage.withLock",
] as const;

export interface BridgeImportProbe {
	/** Identity of the installation that supplied the imported module. */
	readonly installationId: string;
	/** Resolved module origin, retained for diagnostics and origin verification. */
	readonly moduleOrigin: string;
	readonly packageName: string;
	readonly packageVersion: string | null;
	/** A live import from this installation, not a declaration-only inspection. */
	readonly live: boolean;
	/** The caller verified that moduleOrigin belongs to installationId. */
	readonly originVerified: boolean;
	readonly runtimeExports: readonly string[];
	readonly typeExports: readonly string[];
}

export interface BridgeContextProbe {
	/** True only after an actual extension callback received this context. */
	readonly live: boolean;
	readonly members: readonly string[];
	readonly hooks: readonly string[];
}

export interface BridgeStorageProbe {
	/** True only after the selected installation's public storage was exercised. */
	readonly live: boolean;
	readonly members: readonly string[];
	readonly lock: {
		readonly available: boolean;
		readonly identity: "native" | "unknown" | "different";
		readonly matchesNative: boolean;
	};
}

export interface BridgeFeasibilityInput {
	readonly distribution: BridgeDistribution;
	readonly installationId: string;
	readonly sdkVersion: string | null;
	readonly import: BridgeImportProbe;
	readonly context: BridgeContextProbe;
	readonly storage: BridgeStorageProbe;
	readonly expectedSdkVersion?: string;
}

export interface BridgeBlocker {
	readonly code: "bridge_unavailable";
	readonly fcId: "BRIDGE-01";
	readonly surface: BridgeSurface;
	readonly distribution: BridgeDistribution;
	readonly version: string | null;
	readonly missingPublicSymbol: string;
	readonly reproduction: string;
	readonly attemptedAlternatives: readonly string[];
	readonly userVisibleLimitation: string;
	readonly owner: "B/E/A";
	readonly releaseConsequence: string;
}

export interface BridgeSurfaceResult {
	readonly available: boolean;
	readonly missing: readonly string[];
}

export interface BridgeFeasibilityResult {
	readonly version: 1;
	readonly distribution: BridgeDistribution;
	readonly sdkVersion: string | null;
	/** Vanilla native RPC remains usable when the optional profile is blocked. */
	readonly vanillaFunctional: true;
	readonly status: "available" | "blocked";
	readonly importedPublicApi: BridgeSurfaceResult;
	readonly liveContext: BridgeSurfaceResult;
	readonly nativeStorage: BridgeSurfaceResult;
	readonly runtimeHooks: BridgeSurfaceResult;
	readonly blockers: readonly BridgeBlocker[];
}

const DEFAULT_ATTEMPTED_ALTERNATIVES = [
	"Inspect only the selected installation's public exports and declarations.",
	"Use the public extension context, native storage lock and lifecycle hooks.",
	"Keep the vanilla profile functional instead of loading a bundled SDK or private deep import.",
] as const;

export function createBridgeBlocker(input: {
	distribution: BridgeDistribution;
	version: string | null;
	surface: BridgeSurface;
	missingPublicSymbol: string;
	reproduction: string;
	attemptedAlternatives?: readonly string[];
	userVisibleLimitation?: string;
	releaseConsequence?: string;
}): BridgeBlocker {
	return {
		code: "bridge_unavailable",
		fcId: "BRIDGE-01",
		surface: input.surface,
		distribution: input.distribution,
		version: input.version,
		missingPublicSymbol: input.missingPublicSymbol,
		reproduction: input.reproduction,
		attemptedAlternatives: [...(input.attemptedAlternatives ?? DEFAULT_ATTEMPTED_ALTERNATIVES)],
		userVisibleLimitation:
			input.userVisibleLimitation ??
			"Optional native administration is unavailable; vanilla native RPC remains available.",
		owner: "B/E/A",
		releaseConsequence:
			input.releaseConsequence ?? "BRIDGE-01 stays open for this installed Pi profile.",
	};
}

export function missingBridgeMembers(
	available: readonly string[],
	required: readonly string[],
): readonly string[] {
	const present = new Set(available);
	return required.filter((member) => !present.has(member));
}

function nonempty(value: string): boolean {
	return value.length > 0 && !value.includes("\0");
}

/**
 * Evaluate public bridge surfaces from caller-supplied evidence.  This is a
 * report-only function: missing public APIs become blockers and are never
 * represented by no-op or mocked capabilities.
 */
export function discoverBridgeCapabilities(input: BridgeFeasibilityInput): BridgeFeasibilityResult {
	const expectedVersion = input.expectedSdkVersion ?? BRIDGE_SDK_BASELINE;
	const version = input.sdkVersion ?? input.import.packageVersion;
	const blockers: BridgeBlocker[] = [];
	const add = (
		surface: BridgeSurface,
		missingPublicSymbol: string,
		reproduction: string,
		userVisibleLimitation?: string,
	) => {
		blockers.push(
			createBridgeBlocker({
				distribution: input.distribution,
				version,
				surface,
				missingPublicSymbol,
				reproduction,
				userVisibleLimitation,
			}),
		);
	};

	if (!nonempty(input.installationId))
		add("import", "selected Pi installation identity", "The feasibility probe supplied no installation identity.");
	if (!nonempty(input.import.installationId) || input.import.installationId !== input.installationId)
		add(
			"import",
			"selected-installation import origin",
			"The imported module identity does not match the selected Pi installation.",
			"Administration is unavailable until imports are proven to come from the selected installation.",
		);
	if (!nonempty(input.import.moduleOrigin) || !input.import.originVerified)
		add(
			"import",
			"verified public import origin",
			"A live public import origin was not verified for the selected installation.",
			"Administration is unavailable; no random global or bundled SDK is searched.",
		);
	if (input.import.packageName !== PI_CODING_AGENT_PACKAGE)
		add(
			"import",
			PI_CODING_AGENT_PACKAGE,
			`The selected import reported package ${input.import.packageName || "<missing>"}.`,
		);
	if (input.import.packageVersion !== null && input.sdkVersion !== null && input.import.packageVersion !== input.sdkVersion)
		add(
			"import",
			"independent Pi SDK version identity",
			`The imported package version ${input.import.packageVersion} differs from reported SDK version ${input.sdkVersion}.`,
		);
	if (version !== expectedVersion)
		add(
			"import",
			`Pi SDK ${expectedVersion}`,
			`The selected installation reports SDK version ${version ?? "unknown"}; baseline ${expectedVersion} is required for this probe.`,
		);
	if (!input.import.live)
		add(
			"import",
			"live public module import",
			"The available export/declaration listing was not verified by a live import.",
		);

	const availableExports = [...input.import.runtimeExports, ...input.import.typeExports];
	const missingExports = missingBridgeMembers(
		availableExports,
		BRIDGE_PUBLIC_EXPORTS.map((entry) => entry.symbol),
	);
	for (const symbol of missingExports)
		add(
			"import",
			symbol,
			`The selected ${input.distribution} Pi import does not expose public symbol ${symbol}.`,
			`${symbol} is unavailable through the public installed API; no private deep import or mock is used.`,
		);

	const missingContext = missingBridgeMembers(input.context.members, BRIDGE_CONTEXT_REQUIREMENTS);
	if (!input.context.live)
		add("context", "live public ExtensionContext", "No live extension callback supplied the inspected context.");
	for (const member of missingContext)
		add(
			"context",
			member,
			`The live ${input.distribution} Pi extension context does not expose ${member}.`,
		);

	const missingStorage = missingBridgeMembers(input.storage.members, BRIDGE_STORAGE_REQUIREMENTS);
	if (!input.storage.live)
		add("storage", "live native SettingsManager storage", "Native public storage was not exercised by the selected installation.");
	for (const member of missingStorage)
		add(
			"storage",
			member,
			`The selected ${input.distribution} Pi storage surface does not expose ${member}.`,
			"Native settings administration is unavailable; direct JSON writes are not a fallback.",
		);
	if (!input.storage.lock.available || input.storage.lock.identity !== "native" || !input.storage.lock.matchesNative)
		add(
			"storage",
			"native SettingsStorage lock identity",
			"The public storage lock was not verified as the same native lock used by Pi.",
			"Native settings administration is unavailable until lock ownership is proven.",
		);

	const missingHooks = missingBridgeMembers(input.context.hooks, BRIDGE_RUNTIME_HOOKS);
	for (const hook of missingHooks)
		add(
			"runtime-hooks",
			hook,
			`The selected ${input.distribution} Pi installation did not expose public ${hook} lifecycle delivery.`,
			"The bridge cannot claim safe bind/dispose lifecycle behavior without this hook.",
		);

	const surface = (kind: BridgeSurface): BridgeSurfaceResult => {
		const related = blockers.filter((blocker) => blocker.surface === kind);
		return {
			available: related.length === 0,
			missing: [...new Set(related.map((blocker) => blocker.missingPublicSymbol))],
		};
	};

	return {
		version: BRIDGE_PROBE_VERSION,
		distribution: input.distribution,
		sdkVersion: version,
		vanillaFunctional: true,
		status: blockers.length === 0 ? "available" : "blocked",
		importedPublicApi: surface("import"),
		liveContext: surface("context"),
		nativeStorage: surface("storage"),
		runtimeHooks: surface("runtime-hooks"),
		blockers,
	};
}
