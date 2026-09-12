import type { BridgeBlocker } from "../bridge/feasibility.ts";
import { PI_TOOLS_CALL_BLOCKER } from "../extensions/adapter-mcp.ts";
import { ADMIN_OPERATIONS } from "./catalog.ts";
import type {
	AdminBlocker,
	AdminOperationDefinition,
	AdminOperationStatus,
	AdminProfileEvidence,
	AdminProfileReport,
	AdminRequirement,
	AdminSupportStatus,
	ProfileStatus,
} from "./types.ts";

const unsupported = (requirement: AdminRequirement): string => {
	switch (requirement) {
		case "vanilla":
			return "The selected Pi native RPC profile was not verified.";
		case "authoring":
			return "The bounded host-side authoring service was not verified.";
		case "controller":
			return "The controller-owned operation is not available in this assistant profile.";
		case "bridge":
			return "The selected installation has no verified public administration bridge.";
		case "mcp":
			return "The selected installation has no verified public MCP adapter administration surface.";
		case "subagent":
			return "The operator-installed native subagent extension is unavailable.";
		case "llama":
			return "The optional native llama.cpp provider is not enabled.";
		case "native-extension":
			return "The optional native extension is not installed or was not observed.";
	}
};

function bridgeBlockers(evidence: AdminProfileEvidence): readonly BridgeBlocker[] {
	return evidence.bridge?.blockers ?? [];
}

function profileStatus(id: "V" | "A" | "M", evidence: AdminProfileEvidence): ProfileStatus {
	if (id === "V") {
		return evidence.vanilla?.available === true
			? {
					id,
					status: "available",
					supported: true,
					reason: "Vanilla native RPC is verified.",
					blockers: [],
				}
			: {
					id,
					status: "blocked",
					supported: false,
					reason: unsupported("vanilla"),
					blockers: [],
				};
	}
	if (id === "A") {
		const blockers = bridgeBlockers(evidence);
		return evidence.bridge?.status === "available"
			? {
					id,
					status: "available",
					supported: true,
					reason: "The selected installation exposes the verified public bridge.",
					blockers: [],
				}
			: {
					id,
					status: "blocked",
					supported: false,
					reason: unsupported("bridge"),
					blockers,
				};
	}
	const blockers =
		evidence.mcp?.available && evidence.mcp.publicAdministration ? [] : [PI_TOOLS_CALL_BLOCKER];
	return evidence.mcp?.available === true && evidence.mcp.publicAdministration === true
		? {
				id,
				status: "available",
				supported: true,
				reason: "The public native MCP adapter surface is verified.",
				blockers: [],
			}
		: {
				id,
				status: "blocked",
				supported: false,
				reason: unsupported("mcp"),
				blockers,
			};
}

function hasRequirement(requirement: AdminRequirement, evidence: AdminProfileEvidence): boolean {
	switch (requirement) {
		case "vanilla":
			return evidence.vanilla?.available === true;
		case "authoring":
			return evidence.authoring?.available === true;
		case "controller":
			return evidence.controller?.available === true;
		case "bridge":
			return evidence.bridge?.status === "available";
		case "mcp":
			return evidence.mcp?.available === true && evidence.mcp.publicAdministration === true;
		case "subagent":
			return evidence.optional?.subagent === true;
		case "llama":
			return evidence.optional?.llama === true;
		case "native-extension":
			return evidence.optional?.nativeExtension === true;
	}
}

function blockersFor(
	operation: AdminOperationDefinition,
	evidence: AdminProfileEvidence,
): readonly AdminBlocker[] {
	if (operation.requirement === "bridge") return bridgeBlockers(evidence);
	if (
		operation.requirement === "mcp" &&
		!(evidence.mcp?.available && evidence.mcp.publicAdministration)
	)
		return [PI_TOOLS_CALL_BLOCKER];
	return [];
}

function operationStatus(
	operation: AdminOperationDefinition,
	evidence: AdminProfileEvidence,
): AdminOperationStatus {
	if (operation.id === PI_TOOLS_CALL_BLOCKER.operation)
		return {
			id: operation.id,
			featureId: operation.featureId,
			profile: operation.profile,
			route: PI_TOOLS_CALL_BLOCKER.route,
			status: "blocked",
			supported: false,
			reason: PI_TOOLS_CALL_BLOCKER.missingPublicSymbol,
			userVisibleLimitation: PI_TOOLS_CALL_BLOCKER.userVisibleLimitation,
			fallback: "unavailable",
			tuiFallback: false,
			blockers: [PI_TOOLS_CALL_BLOCKER],
		};
	const available = hasRequirement(operation.requirement, evidence);
	const blockers = blockersFor(operation, evidence);
	let status: AdminSupportStatus = available ? "supported" : "unsupported";
	if (!available && operation.optional) status = "optional-unavailable";
	if (!available && (blockers.length > 0 || operation.requirement === "bridge")) status = "blocked";
	return {
		id: operation.id,
		featureId: operation.featureId,
		profile: operation.profile,
		route: operation.route,
		status,
		supported: available,
		reason: available
			? `Supported through the ${operation.route} route.`
			: unsupported(operation.requirement),
		userVisibleLimitation: available
			? null
			: operation.optional
				? "This optional capability is unavailable; authoring and core native behavior remain separate."
				: unsupported(operation.requirement),
		fallback: "unavailable",
		tuiFallback: false,
		blockers,
	};
}

export function evaluateAdminProfiles(evidence: AdminProfileEvidence = {}): AdminProfileReport {
	const profiles = {
		V: profileStatus("V", evidence),
		A: profileStatus("A", evidence),
		M: profileStatus("M", evidence),
	} as const;
	return {
		version: 1,
		profiles,
		operations: ADMIN_OPERATIONS.map((operation) => operationStatus(operation, evidence)),
	};
}

export function operationSupport(
	operationId: string,
	evidence: AdminProfileEvidence = {},
): AdminOperationStatus {
	const operation = ADMIN_OPERATIONS.find((entry) => entry.id === operationId);
	if (!operation) throw new Error(`Unknown administration operation: ${operationId}`);
	return operationStatus(operation, evidence);
}
