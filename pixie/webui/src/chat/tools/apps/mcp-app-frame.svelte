<script module lang="ts">
export interface AppFrameHandle {
	close: () => Promise<void>;
}
</script>

<script lang="ts">
	import type { McpAppAttachment, McpAppPermissions } from "@pixie/contracts";
	import type { McpUiHostContext } from "@modelcontextprotocol/ext-apps/app-bridge";
	import type { ReadResourceResult } from "@modelcontextprotocol/sdk/types.js";
	import Button from "../../../components/button.svelte";
	import Icon from "../../../components/icon.svelte";
	import { errorText, getTransport } from "../../../connection";
	import { randomId } from "../../../lib";
	import type { ToolRenderProps } from "../../render/tool-registry";
	import {
		APP_KEEP_ALIVE_INTERVAL_MS,
		readMcpAppHTML,
		renewMcpAppView,
		revokeMcpAppView,
	} from "./mcp-app-client";
	import { getMcpAppSessionContext } from "./mcp-app-context";
	import { OriginPinnedAppTransport } from "./mcp-app-transport";
	import {
		MCP_APP_IFRAME_SANDBOX,
		mcpAppPermissionLabels,
		toCallToolResult,
		toMcpToolResult,
	} from "./mcp-app-view";

	type AppBridgeModule = typeof import("@modelcontextprotocol/ext-apps/app-bridge");
	type AppBridgeInstance = InstanceType<AppBridgeModule["AppBridge"]>;

	const TEARDOWN_TIMEOUT_MS = 1_000;
	const PROXY_READY_TIMEOUT_MS = 10_000;
	const INITIALIZED_TIMEOUT_MS = 10_000;
	const APP_OPEN_TIMEOUT_MS = 75_000;

	interface Props {
		app: McpAppAttachment;
		toolCallId: string;
		args: Record<string, unknown>;
		result: unknown;
		status: ToolRenderProps["status"];
		onRequestClose: () => Promise<void>;
	}

	interface AppRuntime {
		cancelled: boolean;
		initialized: boolean;
		sandboxReady: boolean;
		bridge: AppBridgeInstance | null;
		viewId: string | null;
		setup: Promise<void>;
		release: Promise<void> | null;
		permissionResolver: ((approved: boolean) => void) | null;
		lifecycle: AbortController;
		timers: Set<ReturnType<typeof setTimeout>>;
		cleanups: (() => void)[];
	}

	let { app, toolCallId, args, result, status, onRequestClose }: Props = $props();
	const sessionContext = getMcpAppSessionContext();
	let projectId = $derived(sessionContext?.projectId);
	let sessionId = $derived(sessionContext?.sessionId);
	let iframe = $state<HTMLIFrameElement>();
	let runtime: AppRuntime | null = null;
	let attempt = $state(0);
	let phase = $state<"loading" | "permission" | "ready" | "error" | "closing">("loading");
	let failure = $state("");
	let permissionLabels = $state<string[]>([]);

	function delayed(ms: number): Promise<void> {
		return new Promise((resolve) => setTimeout(resolve, ms));
	}

	async function closeBridge(bridge: AppBridgeInstance, initialized: boolean): Promise<void> {
		const teardown = initialized
			? Promise.race([bridge.teardownResource({}).catch(() => undefined), delayed(TEARDOWN_TIMEOUT_MS)])
			: Promise.resolve();
		await teardown;
		await bridge.close().catch(() => {});
	}

	async function runAppOperation<T>(
		viewId: string,
		signal: AbortSignal,
		operation: (transport: ReturnType<typeof getTransport>, operationId: string) => Promise<T>,
	): Promise<T> {
		const transport = getTransport();
		const operationId = randomId("operation");
		const cancel = () => {
			void transport.request("session.appOperationCancel", { viewId, operationId }, { timeoutMs: 5_000 }).catch(() => {});
		};
		if (signal.aborted) cancel();
		else signal.addEventListener("abort", cancel, { once: true });
		try {
			return await operation(transport, operationId);
		} finally {
			signal.removeEventListener("abort", cancel);
		}
	}

	function currentTheme(query: MediaQueryList): "light" | "dark" {
		return query.matches ? "light" : "dark";
	}

	function cancelRuntime(target: AppRuntime): void {
		target.cancelled = true;
		target.lifecycle.abort(new Error("App view closed"));
		for (const timer of target.timers) clearTimeout(timer);
		target.timers.clear();
		const resolver = target.permissionResolver;
		target.permissionResolver = null;
		resolver?.(false);
	}

	function releaseRuntime(target: AppRuntime, graceful: boolean): Promise<void> {
		if (target.release) return target.release;
		target.release = (async () => {
			for (const timer of target.timers) clearTimeout(timer);
			target.timers.clear();
			for (const cleanup of target.cleanups.splice(0)) cleanup();
			const bridge = target.bridge;
			target.bridge = null;
			if (bridge) await closeBridge(bridge, graceful && target.initialized);
			const viewId = target.viewId;
			target.viewId = null;
			if (viewId) await revokeMcpAppView(viewId).catch(() => {});
		})();
		return target.release;
	}

	export async function close(): Promise<void> {
		const current = runtime;
		if (!current) return;
		phase = "closing";
		cancelRuntime(current);
		void current.setup.finally(() => releaseRuntime(current, true));
	}

	function choosePermissions(approved: boolean): void {
		const current = runtime;
		const resolver = current?.permissionResolver;
		if (!current || !resolver || current.cancelled) return;
		current.permissionResolver = null;
		resolver(approved);
	}

	$effect(() => {
		const frame = iframe;
		const currentProjectId = projectId;
		const currentSessionId = sessionId;
		const currentApp = app;
		const currentToolCallId = toolCallId;
		const currentArgs = args;
		const currentResult = result;
		const currentStatus = status;
		const currentAttempt = attempt;
		void currentAttempt;
		if (!currentProjectId || !currentSessionId || !frame?.contentWindow) {
			phase = "error";
			failure = "This app is no longer attached to an open chat.";
			return;
		}

		const target: AppRuntime = {
			cancelled: false,
			initialized: false,
			sandboxReady: false,
			bridge: null,
			viewId: null,
			setup: Promise.resolve(),
			release: null,
			permissionResolver: null,
			lifecycle: new AbortController(),
			timers: new Set(),
			cleanups: [],
		};
		runtime = target;
		phase = "loading";
		failure = "";
		permissionLabels = [];

		const fail = (error: unknown, fallback: string) => {
			if (target.cancelled || runtime !== target) return;
			cancelRuntime(target);
			phase = "error";
			failure = errorText(error, fallback);
			void releaseRuntime(target, true);
		};
		const deadline = (ms: number, message: string) => {
			const timer = setTimeout(() => {
				target.timers.delete(timer);
				fail(new Error(message), message);
			}, ms);
			target.timers.add(timer);
			return timer;
		};
		const clearDeadline = (timer: ReturnType<typeof setTimeout> | undefined) => {
			if (timer === undefined) return;
			clearTimeout(timer);
			target.timers.delete(timer);
		};

		target.setup = (async () => {
			try {
				const bridgeModule = import("@modelcontextprotocol/ext-apps/app-bridge");
				const protocolModule = import("@modelcontextprotocol/sdk/types.js");
				const opened = await getTransport().request(
					"session.appOpen",
					{
						projectId: currentProjectId,
						sessionId: currentSessionId,
						toolCallId: currentToolCallId,
						parentOrigin: window.location.origin,
					},
					{ timeoutMs: APP_OPEN_TIMEOUT_MS },
				);
				target.viewId = opened.viewId;
				if (target.cancelled) return;
				const viewScope = {
					projectId: currentProjectId,
					sessionId: currentSessionId,
					toolCallId: currentToolCallId,
					viewId: opened.viewId,
				};
				let renewalPending = false;
				const renewLease = () => {
					if (target.cancelled || renewalPending) return;
					renewalPending = true;
					void renewMcpAppView(viewScope, target.lifecycle.signal)
						.catch((error) => {
							if (!target.lifecycle.signal.aborted) fail(error, "The app view expired.");
						})
						.finally(() => { renewalPending = false; });
				};
				const leaseTimer = setInterval(renewLease, APP_KEEP_ALIVE_INTERVAL_MS);
				target.cleanups.push(() => clearInterval(leaseTimer));

				const [{ AppBridge, buildAllowAttribute }, { JSONRPCMessageSchema }, loadedHTML] = await Promise.all([
					bridgeModule,
					protocolModule,
					readMcpAppHTML(opened, viewScope, target.lifecycle.signal),
				]);
				let appHTML: string | null = loadedHTML;
				if (target.cancelled || !frame.contentWindow) return;
				const sandboxOrigin = new URL(opened.url).origin;
				const labels = mcpAppPermissionLabels(opened.resource.permissions);
				let grantedPermissions: McpAppPermissions | undefined;
				if (labels.length > 0) {
					permissionLabels = labels;
					phase = "permission";
					const approved = await new Promise<boolean>((resolve) => { target.permissionResolver = resolve; });
					target.permissionResolver = null;
					if (target.cancelled) return;
					if (approved) grantedPermissions = opened.resource.permissions;
					phase = "loading";
				}
				if (target.cancelled || !frame.contentWindow) return;
				frame.allow = buildAllowAttribute(grantedPermissions);

				const themeQuery = window.matchMedia("(prefers-color-scheme: light)");
				let hostContext: McpUiHostContext = {
					theme: currentTheme(themeQuery),
					displayMode: "fullscreen",
					availableDisplayModes: ["fullscreen"],
					locale: navigator.language,
					timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
					platform: "web",
				};
				const bridge = new AppBridge(
					null,
					{ name: "Pixie", version: "1" },
					{
						serverTools: {},
						serverResources: {},
						sandbox: {
							...(opened.resource.csp ? { csp: opened.resource.csp } : {}),
							...(grantedPermissions ? { permissions: grantedPermissions } : {}),
						},
					},
					{ hostContext },
				);
				target.bridge = bridge;

				const updateTheme = () => {
					if (target.cancelled) return;
					hostContext = { ...hostContext, theme: currentTheme(themeQuery) };
					bridge.setHostContext(hostContext);
				};
				themeQuery.addEventListener("change", updateTheme);
				target.cleanups.push(() => themeQuery.removeEventListener("change", updateTheme));

				bridge.oncalltool = async (params, extra) => {
					if (target.cancelled || !target.initialized) throw new Error("The app has not completed initialization.");
					return toCallToolResult(
						await runAppOperation(opened.viewId, extra.signal, (transport, operationId) =>
							transport.request(
								"session.appToolCall",
								{
									projectId: currentProjectId,
									sessionId: currentSessionId,
									toolCallId: currentToolCallId,
									viewId: opened.viewId,
									operationId,
									name: params.name,
									...(params.arguments ? { arguments: params.arguments } : {}),
								},
								{ signal: extra.signal },
							),
						),
					);
				};
				bridge.onreadresource = async (params, extra) => {
					if (target.cancelled || !target.initialized) throw new Error("The app has not completed initialization.");
					const resource = await runAppOperation(opened.viewId, extra.signal, (transport, operationId) =>
						transport.request(
							"session.appResourceRead",
							{
								projectId: currentProjectId,
								sessionId: currentSessionId,
								toolCallId: currentToolCallId,
								viewId: opened.viewId,
								operationId,
								uri: params.uri,
							},
							{ signal: extra.signal },
						),
					);
					return resource as ReadResourceResult;
				};
				bridge.onlistresources = async (params) => ({
					resources: params?.cursor ? [] : [{ uri: currentApp.resourceUri, name: currentApp.toolName, mimeType: "text/html;profile=mcp-app" }],
				});
				bridge.onlistresourcetemplates = async () => ({ resourceTemplates: [] });
				let proxyReadyTimer: ReturnType<typeof setTimeout> | undefined;
				let initializedTimer: ReturnType<typeof setTimeout> | undefined;
				const onSandboxReady = () => {
					if (target.cancelled || target.sandboxReady) return;
					target.sandboxReady = true;
					bridge.removeEventListener("sandboxready", onSandboxReady);
					clearDeadline(proxyReadyTimer);
					initializedTimer = deadline(INITIALIZED_TIMEOUT_MS, "The app did not finish starting.");
					const html = appHTML;
					appHTML = null;
					if (html === null) {
						fail(new Error("The app content was already consumed."), "The app content could not be loaded.");
						return;
					}
					void bridge.sendSandboxResourceReady({
						html,
						...(opened.resource.csp ? { csp: opened.resource.csp } : {}),
						...(grantedPermissions ? { permissions: grantedPermissions } : {}),
					}).catch((error) => fail(error, "The app content could not be loaded."));
				};
				const onInitialized = () => {
					if (target.cancelled || target.initialized || !target.sandboxReady) return;
					target.initialized = true;
					clearDeadline(initializedTimer);
					void (async () => {
						await bridge.sendToolInput({ arguments: currentArgs });
						await bridge.sendToolResult(toMcpToolResult(currentResult, currentStatus === "error"));
						if (!target.cancelled && runtime === target) phase = "ready";
					})().catch((error) => fail(error, "The app result could not be delivered."));
				};
				const onAppRequestClose = () => {
					if (!target.cancelled) void onRequestClose();
				};
				bridge.addEventListener("sandboxready", onSandboxReady);
				bridge.addEventListener("initialized", onInitialized);
				bridge.addEventListener("requestteardown", onAppRequestClose);
				target.cleanups.push(
					() => bridge.removeEventListener("sandboxready", onSandboxReady),
					() => bridge.removeEventListener("initialized", onInitialized),
					() => bridge.removeEventListener("requestteardown", onAppRequestClose),
				);
				const transportTarget = frame.contentWindow;
				await bridge.connect(new OriginPinnedAppTransport(transportTarget, sandboxOrigin, JSONRPCMessageSchema));
				if (target.cancelled) return;
				frame.src = opened.url;
				proxyReadyTimer = deadline(PROXY_READY_TIMEOUT_MS, "The app sandbox did not become ready.");
			} catch (error) {
				fail(error, "The app could not be opened.");
			}
		})();

		return () => {
			cancelRuntime(target);
			if (runtime === target) runtime = null;
			void target.setup.finally(() => releaseRuntime(target, true));
		};
	});
</script>

<div class="relative min-h-0 flex-1 bg-container-project-bg">
	{#key attempt}
		<iframe
			bind:this={iframe}
			title={`${app.toolName} interactive app`}
			sandbox={MCP_APP_IFRAME_SANDBOX}
			src="about:blank"
			class="h-full min-h-0 w-full border-0"
		></iframe>
	{/key}
	{#if phase !== "ready"}
		<div class="absolute inset-0 flex items-center justify-center bg-container-project-bg p-lg">
			{#if phase === "permission"}
				<section aria-labelledby="mcp-app-permissions-title" class="flex w-full max-w-[28rem] flex-col gap-md">
					<div class="flex flex-col gap-xs">
						<h3 id="mcp-app-permissions-title" class="tr-title-section text-text-default">{app.extensionName} requests browser access</h3>
						<p class="tr-text-ui text-text-muted">Choose whether to delegate these capabilities to this interactive view.</p>
					</div>
					<ul class="list-disc pl-lg tr-text-ui text-text-default">{#each permissionLabels as permission (permission)}<li>{permission}</li>{/each}</ul>
					<div class="flex flex-wrap justify-end gap-sm">
						<Button variant="outline" size="sm" onclick={() => choosePermissions(false)}>Open without access</Button>
						<Button size="sm" aria-label={`Allow ${app.extensionName} to use ${permissionLabels.join(", ")}`} onclick={() => choosePermissions(true)}>Allow access</Button>
					</div>
				</section>
			{:else if phase === "loading" || phase === "closing"}
				<div class="flex items-center gap-sm text-text-muted tr-text-ui" role="status">
					<Icon name="loader-circle" size={16} class="animate-spin motion-reduce:animate-none" />
					{phase === "closing" ? "Closing app…" : "Loading app…"}
				</div>
			{:else}
				<div class="flex w-full max-w-[28rem] flex-col items-center gap-md text-center" role="alert">
					<p class="tr-text-ui text-feedback-error">{failure}</p>
					<Button variant="outline" size="sm" onclick={() => (attempt += 1)}><Icon name="rotate-cw" size={12} />Retry</Button>
				</div>
			{/if}
		</div>
	{/if}
</div>
