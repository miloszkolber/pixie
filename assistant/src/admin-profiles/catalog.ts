import {
	PI_TOOLS_CALL_BLOCKER,
	RETAINED_MCP_OPERATIONS,
	type RetainedMcpOperation,
} from "../extensions/adapter-mcp.ts";
import type { AdminFeatureDefinition, AdminOperationDefinition } from "./types.ts";

const operation = (
	value: Omit<AdminOperationDefinition, "tuiFallback">,
): AdminOperationDefinition => ({ ...value, tuiFallback: false });

const mcpOperations = RETAINED_MCP_OPERATIONS.map((entry: RetainedMcpOperation) =>
	operation({
		id: entry.id,
		featureId: "FC26",
		label: entry.id,
		profile: "M",
		route: entry.route,
		requirement: "mcp",
		optional: false,
	}),
);

/**
 * The retained administration inventory is deliberately operation-level.  A
 * profile can expose one supported native route while an unrelated optional
 * route remains unavailable; a broad "Administration" flag would hide that
 * distinction.
 */
export const ADMIN_FEATURES: readonly AdminFeatureDefinition[] = [
	{
		id: "FC17",
		label: "Provider catalog, readiness and canonical model data",
		operations: [
			operation({
				id: "pi.providers.list",
				featureId: "FC17",
				label: "Session-visible provider catalog",
				profile: "V",
				route: "native-rpc",
				requirement: "vanilla",
				optional: false,
			}),
			operation({
				id: "pi.providers.canonical-model-info",
				featureId: "FC17",
				label: "Canonical model metadata",
				profile: "V",
				route: "native-rpc",
				requirement: "vanilla",
				optional: false,
			}),
			operation({
				id: "pi.providers.inventory.refresh",
				featureId: "FC17",
				label: "Refresh configured provider inventory",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.providers.readiness.check",
				featureId: "FC17",
				label: "Configured provider readiness",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
		],
	},
	{
		id: "FC18",
		label: "Provider API-key and OAuth authentication",
		operations: [
			operation({
				id: "provider.loginStart",
				featureId: "FC18",
				label: "Start native login",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "provider.loginBegin",
				featureId: "FC18",
				label: "Begin native login",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "provider.loginReply",
				featureId: "FC18",
				label: "Reply to native login",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "provider.loginCancel",
				featureId: "FC18",
				label: "Cancel native login",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.providers.config.read",
				featureId: "FC18",
				label: "Read native authentication status",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.providers.config.delete",
				featureId: "FC18",
				label: "Logout from native provider",
				profile: "A",
				route: "public-model-runtime",
				requirement: "bridge",
				optional: false,
			}),
		],
	},
	{
		id: "FC19",
		label: "Global defaults and preferences",
		operations: [
			operation({
				id: "pi.defaults.read",
				featureId: "FC19",
				label: "Read native defaults",
				profile: "A",
				route: "public-settings-manager",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.defaults.save",
				featureId: "FC19",
				label: "Save native defaults",
				profile: "A",
				route: "public-settings-manager",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.defaults.clear",
				featureId: "FC19",
				label: "Clear native defaults",
				profile: "A",
				route: "public-settings-manager",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.preferences.read",
				featureId: "FC19",
				label: "Read native preferences",
				profile: "A",
				route: "public-settings-manager",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.preferences.save",
				featureId: "FC19",
				label: "Save native preferences",
				profile: "A",
				route: "public-settings-manager",
				requirement: "bridge",
				optional: false,
			}),
			operation({
				id: "pi.preferences.reset",
				featureId: "FC19",
				label: "Reset native preferences",
				profile: "A",
				route: "public-settings-manager",
				requirement: "bridge",
				optional: false,
			}),
		],
	},
	{
		id: "FC20",
		label: "Native extension, package and skill inventory",
		operations: [
			operation({
				id: "pi.extensions.list",
				featureId: "FC20",
				label: "Configured and resident resource inventory",
				profile: "A",
				route: "public-package-resolver",
				requirement: "bridge",
				optional: false,
			}),
		],
	},
	{
		id: "FC21",
		label: "Native extension enable, disable and deferred application",
		operations: [
			operation({
				id: "pi.extensions.configure",
				featureId: "FC21",
				label: "Revision-checked native resource configuration",
				profile: "A",
				route: "public-settings-manager",
				requirement: "bridge",
				optional: false,
			}),
		],
	},
	{
		id: "FC22",
		label: "Whole-host reload",
		operations: [
			operation({
				id: "pi.reload",
				featureId: "FC22",
				label: "Bounded host restart",
				profile: "V",
				route: "assistant-lifecycle",
				requirement: "vanilla",
				optional: false,
			}),
		],
	},
	{
		id: "FC23",
		label: "Defined-agent Markdown authoring and mentions",
		operations: [
			operation({
				id: "pi.sources.list",
				featureId: "FC23",
				label: "List native agent definitions",
				profile: "V",
				route: "host-authoring",
				requirement: "authoring",
				optional: false,
			}),
			operation({
				id: "pi.sources.create",
				featureId: "FC23",
				label: "Create native Markdown",
				profile: "V",
				route: "host-authoring",
				requirement: "authoring",
				optional: false,
			}),
			operation({
				id: "pi.sources.update",
				featureId: "FC23",
				label: "Update native Markdown",
				profile: "V",
				route: "host-authoring",
				requirement: "authoring",
				optional: false,
			}),
			operation({
				id: "pi.sources.delete",
				featureId: "FC23",
				label: "Delete a revision-checked definition",
				profile: "V",
				route: "host-authoring",
				requirement: "authoring",
				optional: false,
			}),
			operation({
				id: "pi.agent-mentions.list",
				featureId: "FC23",
				label: "List agent mentions",
				profile: "V",
				route: "host-authoring",
				requirement: "authoring",
				optional: false,
			}),
		],
	},
	{
		id: "FC24",
		label: "Native delegation and subagent execution",
		operations: [
			operation({
				id: "pi.subagent.execute",
				featureId: "FC24",
				label: "Operator-installed native subagent execution",
				profile: "V",
				route: "native-extension",
				requirement: "subagent",
				optional: true,
			}),
		],
	},
	{
		id: "FC25",
		label: "Plans, todo, goals, tasks and questions",
		operations: [
			operation({
				id: "pi.todo.plan",
				featureId: "FC25",
				label: "Native todo plan projection",
				profile: "V",
				route: "native-extension",
				requirement: "native-extension",
				optional: true,
			}),
			operation({
				id: "pixie.goals.questions",
				featureId: "FC25",
				label: "Controller-owned goals and questions",
				profile: "V",
				route: "controller-api",
				requirement: "controller",
				optional: false,
			}),
		],
	},
	{
		id: "FC26",
		label: "Native MCP inventory, configuration and membership",
		operations: mcpOperations,
	},
	{
		id: "FC27",
		label: "Local llama.cpp provider",
		operations: [
			operation({
				id: "pi.llama",
				featureId: "FC27",
				label: "Explicit native llama.cpp provider",
				profile: "V",
				route: "native-cli-provider",
				requirement: "llama",
				optional: true,
			}),
		],
	},
	{
		id: "FC28",
		label: "Signet and unrelated native extensions",
		operations: [
			operation({
				id: "pi.native-extensions",
				featureId: "FC28",
				label: "Native extension discovery and lifecycle",
				profile: "V",
				route: "native-extension",
				requirement: "native-extension",
				optional: true,
			}),
		],
	},
] as const;

export const ADMIN_OPERATIONS: readonly AdminOperationDefinition[] = ADMIN_FEATURES.flatMap(
	(feature) => feature.operations,
);

export { PI_TOOLS_CALL_BLOCKER };
