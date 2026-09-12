import type { BridgeBlocker, BridgeFeasibilityResult } from "../bridge/feasibility.ts";
import type { PiToolsCallBlocker } from "../extensions/adapter-mcp.ts";

export type AdminProfileId = "V" | "A" | "M";
export type OptionalCapability = "subagent" | "llama" | "signet" | "native-extension";
export type AdminFeatureId =
	| "FC17"
	| "FC18"
	| "FC19"
	| "FC20"
	| "FC21"
	| "FC22"
	| "FC23"
	| "FC24"
	| "FC25"
	| "FC26"
	| "FC27"
	| "FC28";

export type AdminRequirement =
	| "vanilla"
	| "authoring"
	| "controller"
	| "bridge"
	| "mcp"
	| "subagent"
	| "llama"
	| "native-extension";

export type AdminSupportStatus = "supported" | "unsupported" | "blocked" | "optional-unavailable";

export interface AdminOperationDefinition {
	readonly id: string;
	readonly featureId: AdminFeatureId;
	readonly label: string;
	readonly profile: AdminProfileId;
	readonly route: string;
	readonly requirement: AdminRequirement;
	readonly optional: boolean;
	/** A native TUI is never presented as a replacement for this operation. */
	readonly tuiFallback: false;
}

export interface AdminFeatureDefinition {
	readonly id: AdminFeatureId;
	readonly label: string;
	readonly operations: readonly AdminOperationDefinition[];
}

export interface VanillaEvidence {
	readonly available: boolean;
	readonly operations?: readonly string[];
}

export interface AuthoringEvidence {
	readonly available: boolean;
	readonly operations?: readonly string[];
}

export interface ControllerEvidence {
	readonly available: boolean;
	readonly operations?: readonly string[];
}

export interface McpEvidence {
	readonly available: boolean;
	readonly publicAdministration: boolean;
	readonly operations?: readonly string[];
	readonly version?: string | null;
}

export interface OptionalEvidence {
	readonly subagent?: boolean;
	readonly llama?: boolean;
	readonly signet?: boolean;
	readonly nativeExtension?: boolean;
}

export interface AdminProfileEvidence {
	readonly vanilla?: VanillaEvidence;
	readonly authoring?: AuthoringEvidence;
	readonly controller?: ControllerEvidence;
	readonly bridge?: BridgeFeasibilityResult;
	readonly mcp?: McpEvidence;
	readonly optional?: OptionalEvidence;
}

export type AdminBlocker = BridgeBlocker | PiToolsCallBlocker;

export interface ProfileStatus {
	readonly id: AdminProfileId;
	readonly status: "available" | "blocked";
	readonly supported: boolean;
	readonly reason: string;
	readonly blockers: readonly AdminBlocker[];
}

export interface AdminOperationStatus {
	readonly id: string;
	readonly featureId: AdminFeatureId;
	readonly profile: AdminProfileId;
	readonly route: string;
	readonly status: AdminSupportStatus;
	readonly supported: boolean;
	readonly reason: string;
	readonly userVisibleLimitation: string | null;
	/** Unsupported controls are unavailable; there is no TUI substitute. */
	readonly fallback: "unavailable";
	readonly tuiFallback: false;
	readonly blockers: readonly AdminBlocker[];
}

export interface AdminProfileReport {
	readonly version: 1;
	readonly profiles: Readonly<Record<AdminProfileId, ProfileStatus>>;
	readonly operations: readonly AdminOperationStatus[];
}
