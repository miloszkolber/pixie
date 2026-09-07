import type { McpAppAttachment, SubagentActivity } from "@pixie/contracts";
import type { Component } from "svelte";
import type { ToolStatus } from "../runtime/types";
import DefaultToolRenderer from "./default-tool-renderer.svelte";

export interface ToolRenderProps {
	toolCallId: string;
	toolName: string;
	args: Record<string, unknown>;
	result: unknown;
	app?: McpAppAttachment | undefined;
	subagentActivity?: SubagentActivity | undefined;
	status: ToolStatus;
	projectAreaRoot?: string | undefined;
	streaming: boolean;
}

export type ToolChrome = "card" | "bare";
export type ToolProminence = "routine" | "primary";
export type ToolRenderer = Component<ToolRenderProps>;
export type ToolSummary = (props: ToolRenderProps) => string;

export interface ToolRegistrationOptions {
	summary?: ToolSummary;
	chrome?: ToolChrome;
	prominence?: ToolProminence;
	defaultExpanded?: boolean;
}

interface ToolRegistration extends ToolRegistrationOptions {
	renderer: ToolRenderer;
}

const registry = new Map<string, ToolRegistration>();

function registration(toolName: string): ToolRegistration | undefined {
	return (
		registry.get(toolName) ??
		(toolName.endsWith("__browser_command") ? registry.get("browser_command") : undefined)
	);
}

export function registerToolRenderer(
	toolName: string,
	renderer: ToolRenderer,
	options: ToolRegistrationOptions = {},
): void {
	registry.set(toolName, { renderer, ...options });
}

export function getToolRenderer(toolName: string): ToolRenderer {
	return registration(toolName)?.renderer ?? DefaultToolRenderer;
}

export function getToolSummary(toolName: string, props: ToolRenderProps): string {
	return registration(toolName)?.summary?.(props) ?? "";
}

export function getToolChrome(toolName: string): ToolChrome {
	return registration(toolName)?.chrome ?? "card";
}

export interface ResolvedProminence {
	prominence: ToolProminence;
	defaultExpanded: boolean;
}

export function resolveProminence(toolName: string): ResolvedProminence {
	const entry = registration(toolName);
	const prominence = entry?.chrome === "bare" ? "primary" : (entry?.prominence ?? "routine");
	return { prominence, defaultExpanded: entry?.defaultExpanded ?? false };
}

export { DefaultToolRenderer };
