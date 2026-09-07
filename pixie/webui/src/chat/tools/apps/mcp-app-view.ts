import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import type { McpAppAttachment, McpAppPermissions, McpAppToolResult } from "@pixie/contracts";
import type { ToolRenderProps } from "../../render/tool-registry";
import { toText } from "../tool-helpers";

export const MCP_APP_IFRAME_SANDBOX = "allow-scripts allow-same-origin allow-forms";

const APP_PERMISSIONS = [
	["camera", "Camera"],
	["microphone", "Microphone"],
	["geolocation", "Location"],
	["clipboardWrite", "Clipboard write"],
] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
	return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function canOpenMcpApp(
	app: McpAppAttachment | undefined,
	status: ToolRenderProps["status"],
): app is McpAppAttachment {
	return Boolean(
		(status === "done" || status === "error") &&
			app &&
			app.toolName.trim() &&
			app.extensionName.trim() &&
			app.resourceUri.startsWith("ui://"),
	);
}

export function mcpAppPermissionLabels(permissions: McpAppPermissions | undefined): string[] {
	return APP_PERMISSIONS.filter(([key]) => permissions?.[key] !== undefined).map(
		([, label]) => label,
	);
}

/** Preserve valid MCP envelopes; normalize legacy scalar results for the app lifecycle. */
export function toMcpToolResult(value: unknown, failed = false): CallToolResult {
	const source = isRecord(value) ? value : undefined;
	const rawContent = source?.content;
	const content = Array.isArray(rawContent)
		? rawContent
		: Array.isArray(value)
			? value
			: value == null
				? []
				: [{ type: "text", text: typeof value === "string" ? value : toText(value) }];
	return {
		content: content as CallToolResult["content"],
		...(source && isRecord(source.structuredContent)
			? { structuredContent: source.structuredContent }
			: {}),
		...(failed || source?.isError === true ? { isError: true } : {}),
		...(source && isRecord(source._meta) ? { _meta: source._meta } : {}),
	};
}

export function toCallToolResult(result: McpAppToolResult): CallToolResult {
	return {
		content: result.content as CallToolResult["content"],
		...(result.structuredContent ? { structuredContent: result.structuredContent } : {}),
		...(result.isError === undefined ? {} : { isError: result.isError }),
		...(result._meta ? { _meta: result._meta } : {}),
	};
}
