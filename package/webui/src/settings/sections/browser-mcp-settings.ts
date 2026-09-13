import type { BrowserMCPStatus } from "@pixie/contracts";
import { errorText } from "@/connection/error-text";
import type { WsTransport } from "@/connection/transport";
import { createExternalStore, toReadableStore } from "@/store/external-store";

/**
 * Browser MCP is an external endpoint that Pi dials directly. Pixie owns one
 * setting and registers it in Pi's MCP configuration; it never hosts a browser
 * or proxies MCP traffic. These helpers keep the form, validation and status
 * wording out of the component and fail closed on an invalid draft.
 */

export interface BrowserMcpDraft {
	name: string;
	url: string;
	enabled: boolean;
}

export type BrowserMcpBusy = "saving" | "removing" | null;

export interface BrowserMcpState {
	status: BrowserMCPStatus | null;
	loading: boolean;
	busy: BrowserMcpBusy;
	error: string | null;
	notice: string | null;
}

export function emptyBrowserMcpDraft(): BrowserMcpDraft {
	return { name: "pixie-browser", url: "http://127.0.0.1:3000/mcp", enabled: false };
}

export function browserMcpDraftFromStatus(status: BrowserMCPStatus): BrowserMcpDraft {
	return { name: status.name, url: status.url, enabled: status.enabled };
}

export function browserMcpNameError(value: string): string | null {
	const name = value.trim();
	if (name === "") return "Enter a name for the Pi MCP entry.";
	if (new TextEncoder().encode(name).byteLength > 128)
		return "Use a name of at most 128 UTF-8 bytes.";
	if (name.includes("\u0000")) return "The name cannot contain a NUL character.";
	if (!/^[\p{L}\p{N}._-]+$/u.test(name))
		return "Use letters, numbers, dots, underscores or hyphens.";
	return null;
}

export function browserMcpUrlError(value: string): string | null {
	const text = value.trim();
	if (text === "") return "Enter the endpoint URL.";
	let parsed: URL;
	try {
		parsed = new URL(text);
	} catch {
		return "Enter an absolute http(s) URL.";
	}
	if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
		return "The endpoint must use http or https.";
	if (!parsed.host) return "Enter an absolute http(s) URL.";
	if (parsed.username || parsed.password)
		return "Credentials cannot be embedded in the URL; use per-deployment hardening instead.";
	return null;
}

export function browserMcpRegistrationLabel(status: BrowserMCPStatus): string {
	return status.registered ? "Registered in Pi" : "Not registered in Pi";
}

export function browserMcpReachabilityLabel(status: BrowserMCPStatus): string {
	return status.reachable ? "Reachable" : "Unreachable";
}

export function browserMcpServerLabel(status: BrowserMCPStatus | null): string | null {
	if (!status?.serverInfo) return null;
	const name = typeof status.serverInfo.name === "string" ? status.serverInfo.name : "";
	const version = typeof status.serverInfo.version === "string" ? status.serverInfo.version : "";
	const label = [name, version].filter((part) => part !== "").join(" ");
	return label === "" ? null : label;
}

export function browserMcpToolSummary(status: BrowserMCPStatus | null): string {
	if (!status || status.tools.length === 0) return "No tools reported.";
	const count = status.tools.length;
	return `${count} tool${count === 1 ? "" : "s"} reported.`;
}

export class BrowserMcpModel {
	readonly state = createExternalStore<BrowserMcpState>(() => ({
		status: null,
		loading: false,
		busy: null,
		error: null,
		notice: null,
	}));
	readonly readable = toReadableStore(this.state);
	private generation = 0;

	constructor(private readonly transport: Pick<WsTransport, "request">) {}

	async load(): Promise<BrowserMCPStatus | null> {
		return this.run(this.transport.request("browserMcp.status", {}), { loading: true }, null);
	}

	async save(draft: BrowserMcpDraft): Promise<BrowserMCPStatus | null> {
		const nameError = browserMcpNameError(draft.name);
		if (nameError) {
			this.state.setState({ error: nameError, notice: null });
			return null;
		}
		const urlError = browserMcpUrlError(draft.url);
		if (urlError) {
			this.state.setState({ error: urlError, notice: null });
			return null;
		}
		return this.run(
			this.transport.request("browserMcp.configure", {
				name: draft.name.trim(),
				url: draft.url.trim(),
				enabled: draft.enabled,
			}),
			{ busy: "saving" },
			draft.enabled
				? "Browser MCP registration saved in Pi."
				: "Browser MCP entry removed from Pi.",
		);
	}

	async remove(): Promise<BrowserMCPStatus | null> {
		return this.run(
			this.transport.request("browserMcp.remove", {}),
			{ busy: "removing" },
			"Browser MCP entry removed from Pi.",
		);
	}

	// One request with a monotonically increasing generation, so a late reply
	// from a superseded request can never overwrite newer state. The in-flight
	// indicator is cleared on every terminal path.
	private async run(
		request: Promise<BrowserMCPStatus>,
		pending: Partial<Pick<BrowserMcpState, "loading" | "busy">>,
		notice: string | null,
	): Promise<BrowserMCPStatus | null> {
		const generation = ++this.generation;
		this.state.setState({ loading: false, busy: null, ...pending, error: null, notice: null });
		try {
			const status = await request;
			if (generation !== this.generation) return null;
			this.state.setState({ status, loading: false, busy: null, notice });
			return status;
		} catch (cause) {
			if (generation === this.generation)
				this.state.setState({ loading: false, busy: null, error: errorText(cause) });
			return null;
		}
	}

	dispose(): void {
		this.generation++;
	}
}
