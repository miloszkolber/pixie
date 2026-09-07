import type { NativeExtensionChange, NativeExtensionInventory, NativeExtensionTarget, NativeResourceState } from "@pixie/contracts";
import type { WsTransport } from "@/connection/transport";
import { createExternalStore, toReadableStore } from "@/store/external-store";

export function nativeStateLabel(state: NativeResourceState): string {
	return { loaded: "Loaded", failed: "Load failed", "not-loaded": "Not loaded in this context",
		"not-observed": "Loaded state unavailable", missing: "Source not installed or missing" }[state];
}
export function nativeReaderLabel(reader: NativeExtensionInventory["context"]["reader"]): string {
	return { session: "Loaded in the selected session", service: "Loaded in the assistant service context, not a project session",
		"not-resident": "The selected session is not resident. It was not opened for inspection.",
		"configured-only": "Configured project resources only. Select an open chat to inspect its loaded extensions." }[reader];
}

export function reloadNotice(result: NativeExtensionChange): string {
	if (result.reload === "reloaded" && result.loaded) {
		return `${result.saved ? "Configuration saved. " : ""}Idle session reopened with the saved native configuration. Inventory below reflects the rebuilt session.`;
	}
	const reason = result.reason === "session-busy"
		? "active work, pending dialogs or registered background work. Nothing was stopped."
		: result.reason === "session-not-resident"
			? "the selected session is not resident. It was not opened."
			: "a configured package is missing, so reopening could install code. Resolve it through native Pi configuration.";
	return `${result.saved ? "Configuration saved. Loaded extensions are unchanged. " : ""}Reload deferred: ${reason}${result.warning ? " Settings cleanup reported a warning. Refresh inventory." : ""}`;
}

export class ExtensionsModel {
	readonly state = createExternalStore<{
		inventory: NativeExtensionInventory | null; loading: boolean; error: string | null;
		busy: "saving" | "checking-reload" | null; notice: string | null;
	}>(() => ({ inventory: null, loading: false, error: null, busy: null, notice: null }));
	readonly readable = toReadableStore(this.state);
	private generation = 0;
	private abort?: AbortController;
	constructor(readonly target: NativeExtensionTarget, private transport: Pick<WsTransport, "request">) {}
	async load(): Promise<void> {
		const generation = ++this.generation;
		this.abort?.abort();
		this.abort = new AbortController();
		this.state.setState({ inventory: null, loading: true, error: null });
		try {
			const inventory = await this.transport.request("pi.nativeExtensions", this.target, { signal: this.abort.signal });
			if (generation !== this.generation) return;
			if (inventory.version !== 1 || inventory.context.sessionId !== (this.target.sessionId ?? null) ||
				(this.target.root && inventory.context.cwd !== this.target.root)) throw new Error("Context mismatch");
			this.state.setState({ inventory, loading: false });
		} catch {
			if (generation === this.generation) this.state.setState({
				inventory: null, loading: false, error: "Native extension inventory unavailable. Retry after checking the assistant connection.",
			});
		}
	}
	async configure(resource: NativeExtensionInventory["resources"][number], confirm: (message: string) => boolean): Promise<void> {
		const state = this.state.getState();
		const scope = resource.scope;
		if (state.busy || state.loading || (scope !== "user" && scope !== "project")) return;
		if (scope === "project" && !this.target.projectId) return;
		const expectedRevision = state.inventory?.configurationRevisions?.[scope];
		const resourceKey = resource.resourceKey;
		if (!expectedRevision || !resourceKey || resource.configurationSupported !== true) return;
		const enabled = !resource.enabled;
		if (!confirm(`${enabled ? "Enable" : "Disable"} this native resource for the next load in ${scope} settings?\n${resource.path}\n\nEnabling runs extension code with the host user's authority on a subsequent native load. No installation or in-process reload will occur. Current sessions remain unchanged.`)) return;
		const generation = ++this.generation;
		this.state.setState({ busy: "saving", error: null, notice: null });
		try {
			const result = await this.transport.request("pi.nativeExtensionConfigure", {
				...this.target, scope, resourceKey, expectedRevision, enabled, confirmed: true,
			});
			if (generation !== this.generation) return;
			if (result.saved !== true || result.loaded !== false || result.reload !== "deferred") throw new Error("Unconfirmed save");
			this.state.setState({ busy: null, notice: reloadNotice(result) });
			await this.load();
		} catch {
			if (generation === this.generation) this.state.setState({ busy: null, inventory: null,
				error: "Save outcome not confirmed. Configuration may have changed or conflicted. Refresh inventory before retrying. Loaded sessions were not reloaded." });
		}
	}
	async requestReload(): Promise<void> {
		if (!this.target.sessionId || this.state.getState().busy || this.state.getState().loading) return;
		const generation = ++this.generation;
		this.state.setState({ busy: "checking-reload", error: null, notice: null });
		try {
			const result = await this.transport.request("pi.nativeExtensionReload", { ...this.target, sessionId: this.target.sessionId });
			if (generation !== this.generation) return;
			if (result.reload !== "deferred" && result.reload !== "reloaded") throw new Error("Unexpected reload outcome");
			this.state.setState({ busy: null, notice: reloadNotice(result) });
			await this.load();
		} catch {
			if (generation === this.generation) this.state.setState({ busy: null, error: "Reload request outcome unavailable. No reload success is confirmed." });
		}
	}
	dispose(): void { this.generation++; this.abort?.abort(); }
}
