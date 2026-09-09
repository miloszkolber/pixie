import { randomUUID } from "node:crypto";
import { extensionInventory } from "./extension-inventory.ts";
import { buildBoundedCatalog, compareNativeFile, type CatalogResult, type NativeFileFingerprint, scanNativeHistory, type HistoryScanOptions, type HistoryScanResult } from "./history/catalog.ts";
import {
	classifyRuntimeWork,
	completeIdleRuntimeRelease,
	completeTuiHandoff,
	createResidencyState,
	lastTerminalState,
	releaseIdleRuntime,
	releaseToTui,
	residencyUsage,
	transitionResidency,
	type ResidencyLimits,
	type ResidencyState,
	type RuntimeIdentity,
} from "./residency/index.ts";
import type { ManagedSession, Sessions } from "./sessions.ts";
import { redactSecrets } from "./session/validation.ts";
import { JsonStore, HostError } from "./storage.ts";
import {
	SessionRuntime,
	type PromptRuntimeRequest,
	type PromptRuntimeResult,
	type RuntimeAbortResult,
	type RuntimeResult,
	type SessionRuntimeReplay,
	type SessionRuntimeOptions,
	UncertainPromptError,
} from "./session-runtime.ts";
import type { NativeModel, NativeResource, NativeThinkingLevel, PromptRequest, SessionCreateRequest } from "./session/types.ts";
import type { DraftMutation, DraftState } from "./drafts/continuity.ts";
import type { OutboxState } from "./outbox/types.ts";
import type { PassiveReplay } from "./ui-state/passive-state.ts";

export const RUNTIME_WIRING_VERSION = 1 as const;
export const DEFAULT_RUNTIME_OUTBOX_ENTRIES = 128;
export const DEFAULT_RUNTIME_OUTBOX_BYTES = 64 * 1024 * 1024;
export const DEFAULT_RUNTIME_STATE_BYTES = 16 * 1024 * 1024;

export interface RuntimeWiringOptions {
	readonly agentDir: string;
	readonly bootId: string;
	readonly sessions?: Sessions;
	readonly residencyLimits?: ResidencyLimits;
	readonly maxOutboxEntries?: number;
	readonly maxOutboxBytes?: number;
	readonly persist?: boolean;
}

interface StoredRuntimeSession {
	readonly sessionKey: string;
	readonly sessionId: string;
	readonly nativeSessionId: string;
	readonly bootId: string;
	readonly generation: number;
	readonly cwd: string;
	readonly agentDir: string;
	readonly outbox: OutboxState;
	readonly drafts: readonly DraftState<string>[];
	readonly passive: PassiveReplay;
}

interface StoredRuntimeState {
	readonly version: typeof RUNTIME_WIRING_VERSION;
	readonly sessions: Record<string, StoredRuntimeSession>;
}

interface NativeSessionDetails {
	readonly request: SessionCreateRequest;
	readonly nativeSessionId: string;
}

function validPositive(value: number | undefined, fallback: number): number {
	return Number.isSafeInteger(value) && value !== undefined && value > 0 ? value : fallback;
}

function resultError(result: { readonly ok: false; readonly error: { readonly message: string } }): never {
	throw new HostError(result.error.message, -32000);
}

function modelIdentity(value: unknown): NativeModel | undefined {
	if (!value || typeof value !== "object") return undefined;
	const model = value as { provider?: unknown; id?: unknown; name?: unknown };
	if (typeof model.provider !== "string" || typeof model.id !== "string") return undefined;
	return {
		provider: model.provider,
		id: model.id,
		...(typeof model.name === "string" ? { name: model.name } : {}),
	};
}

/**
 * Integration owner for the pure assistant contracts. This class is an
 * adapter around the existing Sessions owner: it does not create another
 * AgentSession, transport, provider client or agent loop.
 */
export class RuntimeWiring {
	readonly limits: {
		readonly maxOutboxEntries: number;
		readonly maxOutboxBytes: number;
	};
	private readonly store?: JsonStore<StoredRuntimeState>;
	private readonly stored = new Map<string, StoredRuntimeSession>();
	private readonly runtimes = new Map<string, SessionRuntime>();
	private readonly entries = new Map<string, WeakRef<ManagedSession>>();
	private loaded = false;
	private loading?: Promise<void>;
	private closed = false;
	private _residency: ResidencyState;

	constructor(readonly options: RuntimeWiringOptions) {
		this.limits = {
			maxOutboxEntries: validPositive(options.maxOutboxEntries, DEFAULT_RUNTIME_OUTBOX_ENTRIES),
			maxOutboxBytes: validPositive(options.maxOutboxBytes, DEFAULT_RUNTIME_OUTBOX_BYTES),
		};
		this._residency = createResidencyState(options.residencyLimits);
		if (options.persist !== false)
			this.store = new JsonStore(`${options.agentDir}/pixie/runtime-wiring.json`, () => ({ version: RUNTIME_WIRING_VERSION, sessions: {} }));
	}

	get residency(): ResidencyState {
		return this._residency;
	}

	get runtimeSessions(): ReadonlyMap<string, SessionRuntime> {
		return this.runtimes;
	}

	async ready(): Promise<void> {
		if (this.loaded) return;
		this.loading ??= (async () => {
			if (this.store) {
				const state = await this.store.read();
				if (state.version !== RUNTIME_WIRING_VERSION || !state.sessions || Array.isArray(state.sessions))
					throw new Error("Invalid assistant runtime wiring state");
				for (const [id, record] of Object.entries(state.sessions)) {
					if (record && record.sessionId === id) this.stored.set(id, record);
				}
			}
			this.loaded = true;
		})();
		await this.loading;
	}

	private async inspect(entry: ManagedSession): Promise<NativeSessionDetails> {
		const sessionId = entry.session.sessionId;
		const cwd = entry.session.sessionManager.getCwd();
		let resources: NativeResource[] = [];
		let projectTrust: boolean | null | undefined;
		let defaultProjectTrust: SessionCreateRequest["defaultProjectTrust"] = "ask";
		try {
			const inventory = await extensionInventory(this.options.agentDir, cwd, entry.session, "session", sessionId);
			resources = inventory.resources.map((resource) => ({
				path: resource.path,
				scope: resource.scope === "project" ? "project" : "user",
				kind: "extension",
				requiresTrust: resource.scope === "project",
			}));
			projectTrust = inventory.trust.decision;
			// A native "always" default is observable as trusted with no stored
			// decision. The no-resource case is harmless either way.
			if (inventory.trust.projectTrusted && inventory.trust.decision === null) defaultProjectTrust = "always";
		} catch {
			// Resource discovery is optional for the wire; the native session still
			// owns loading and trust. Keep the flow conservative when unavailable.
			resources = [];
		}

		const availableModels: NativeModel[] = [];
		for (const provider of entry.modelRuntime.getProviders()) {
			for (const model of entry.modelRuntime.getModels(provider.id)) {
				const safe = modelIdentity(model);
				if (safe) availableModels.push(safe);
			}
		}
		const selected = modelIdentity(entry.session.model);
		if (selected && !availableModels.some((model) => model.provider === selected.provider && model.id === selected.id))
			availableModels.push(selected);
		const availableThinkingLevels = [...entry.session.getAvailableThinkingLevels()] as NativeThinkingLevel[];
		return {
			nativeSessionId: sessionId,
			request: {
				identity: {
					sessionKey: "pending",
					sessionId,
					nativeSessionId: sessionId,
					bootId: this.options.bootId,
					childGeneration: 0,
					cwd,
					agentDir: this.options.agentDir,
				},
				resources,
				projectTrust,
				defaultProjectTrust,
				availableModels,
				availableThinkingLevels,
				...(selected ? { model: selected } : {}),
				...(entry.session.thinkingLevel ? { thinkingLevel: entry.session.thinkingLevel } : {}),
			},
		};
	}

	private identity(runtime: SessionRuntime): RuntimeIdentity {
		return { sessionKey: runtime.identity.sessionKey, generation: runtime.identity.childGeneration };
	}

	private resident(identity: RuntimeIdentity): boolean {
		return this._residency.residents.some((resident) => resident.sessionKey === identity.sessionKey && resident.generation === identity.generation);
	}

	private applyResidency(event: Parameters<typeof transitionResidency>[1]): void {
		const result = transitionResidency(this._residency, event);
		if (!result.ok) resultError(result);
		this._residency = result.value;
	}

	private admit(runtime: SessionRuntime): void {
		const identity = this.identity(runtime);
		if (this.resident(identity)) return;
		this.applyResidency({ type: "launch.begin", identity });
		// The SDK session has already been constructed by Sessions. This records
		// completion of that one native ownership handoff; it does not launch one.
		this.applyResidency({ type: "launch.complete", identity });
	}

	private async persistRuntime(id: string, runtime: SessionRuntime): Promise<void> {
		if (!this.store) return;
		const record: StoredRuntimeSession = {
			sessionKey: runtime.identity.sessionKey,
			sessionId: runtime.identity.sessionId,
			nativeSessionId: runtime.identity.nativeSessionId,
			bootId: runtime.identity.bootId,
			generation: runtime.identity.childGeneration,
			cwd: runtime.identity.cwd,
			agentDir: runtime.identity.agentDir,
			outbox: redactSecrets(runtime.outbox) as OutboxState,
			drafts: redactSecrets(runtime.draftStates) as readonly DraftState<string>[],
			passive: redactSecrets(runtime.snapshot().passive) as PassiveReplay,
		};
		const encoded = JSON.stringify({ version: RUNTIME_WIRING_VERSION, sessions: { ...Object.fromEntries(this.stored), [id]: record } });
		if (Buffer.byteLength(encoded) > DEFAULT_RUNTIME_STATE_BYTES)
			throw new HostError("Assistant runtime state exceeds the persistence limit", -32000);
		await this.store.update((state) => {
			(state as { version: typeof RUNTIME_WIRING_VERSION }).version = RUNTIME_WIRING_VERSION;
			(state.sessions as Record<string, StoredRuntimeSession>)[id] = record;
		});
		this.stored.set(id, record);
	}

	/** Bind one existing SDK owner to the contract owner and return its state. */
	async bind(entry: ManagedSession): Promise<SessionRuntime> {
		await this.ready();
		if (this.closed) throw new HostError("Assistant runtime wiring is closed", -32000);
		const id = entry.session.sessionId;
		const current = this.runtimes.get(id);
		const bound = this.entries.get(id)?.deref();
		if (current && bound === entry) return current;

		const inspected = await this.inspect(entry);
		const saved = this.stored.get(id);
		if (current) {
			if (current.state.phase !== "ready") throw new HostError("Session generation is not idle; reconcile before reopening", -32003);
			const released = releaseIdleRuntime(this._residency, this.identity(current));
			if (!released.ok) resultError(released);
			this._residency = released.value;
			const finished = completeIdleRuntimeRelease(this._residency, this.identity(current));
			if (!finished.ok) resultError(finished);
			this._residency = finished.value;
			const next = current.reopen({ ...inspected.request, bootId: this.options.bootId });
			if (!next.ok) resultError(next);
			this.admit(current);
			this.entries.set(id, new WeakRef(entry));
			await this.persistRuntime(id, current);
			return current;
		}

		const generation = saved && saved.bootId !== this.options.bootId ? saved.generation + 1 : saved?.generation ?? 0;
		const identity = {
			...inspected.request.identity,
			sessionKey: saved?.sessionKey ?? randomUUID(),
			childGeneration: generation,
			bootId: this.options.bootId,
		};
		const options: SessionRuntimeOptions = {
			...inspected.request,
			identity,
			...(saved?.outbox ? { outbox: saved.outbox } : {}),
			...(saved?.drafts ? { drafts: saved.drafts } : {}),
			...(saved?.passive ? { passive: saved.passive } : {}),
		};
		const runtime = new SessionRuntime(options);
		if (saved && saved.bootId !== this.options.bootId && runtime.outbox.phase === "open") {
			const reconnected = runtime.reconnectGeneration(generation);
			if (!reconnected.ok) resultError(reconnected);
		}
		this.admit(runtime);
		this.runtimes.set(id, runtime);
		this.entries.set(id, new WeakRef(entry));
		await this.persistRuntime(id, runtime);
		return runtime;
	}

	/** A capacity check used before constructing a new SDK session. */
	canCreate(): RuntimeResult<true> {
		const usage = residencyUsage(this._residency);
		return usage.residents >= this._residency.limits.maxResidents
			? { ok: false, error: { code: "resident-capacity", message: "Managed runtime resident capacity is full" } }
			: { ok: true, value: true };
	}

	async prompt(
		entry: ManagedSession,
		input: PromptRuntimeRequest,
		invoke: (request: PromptRequest) => Promise<unknown>,
	): Promise<PromptRuntimeResult> {
		const runtime = await this.bind(entry);
		const known = input.mutationId ? runtime.outbox.entries.some((candidate) => candidate.mutationId === input.mutationId) : false;
		if (runtime.outbox.entries.length >= this.limits.maxOutboxEntries && !known)
			throw new HostError("Session outbox capacity is full", -32000);
		if (!known) {
			const payloadBytes = Buffer.byteLength(JSON.stringify(input.content));
			const retainedBytes = runtime.outbox.entries.reduce((total, candidate) => total + Buffer.byteLength(JSON.stringify(candidate.payload)), 0);
			if (retainedBytes + payloadBytes > this.limits.maxOutboxBytes)
				throw new HostError("Session outbox payload exceeds its byte bound", -32000);
		}
		try {
			const value = await runtime.executePrompt(input, async (request) => {
				await this.persistRuntime(entry.session.sessionId, runtime);
				return invoke(request);
			});
			await this.persistRuntime(entry.session.sessionId, runtime);
			return value;
		} catch (error) {
			// Persist uncertainty as well as successful settlement; a restart must
			// not forget that native handoff may have happened.
			await this.persistRuntime(entry.session.sessionId, runtime);
			throw error;
		}
	}

	async cancel(entry: ManagedSession, requestId: string, invoke: () => Promise<unknown>): Promise<RuntimeAbortResult> {
		const runtime = await this.bind(entry);
		const value = await runtime.executeAbort(requestId, invoke);
		await this.persistRuntime(entry.session.sessionId, runtime);
		return value;
	}

	async configureModel(entry: ManagedSession, model: NativeModel): Promise<RuntimeResult<SessionRuntime>> {
		const runtime = await this.bind(entry);
		const result = runtime.setModel(model);
		if (!result.ok) return result;
		await this.persistRuntime(entry.session.sessionId, runtime);
		return { ok: true, value: runtime };
	}

	async configureThinking(entry: ManagedSession, level: NativeThinkingLevel): Promise<RuntimeResult<SessionRuntime>> {
		const runtime = await this.bind(entry);
		const result = runtime.setThinkingLevel(level);
		if (!result.ok) return result;
		await this.persistRuntime(entry.session.sessionId, runtime);
		return { ok: true, value: runtime };
	}

	async draft(entry: ManagedSession, clientId: string, mutation: DraftMutation<string>): Promise<RuntimeResult<DraftState<string>>> {
		const runtime = await this.bind(entry);
		const result = runtime.applyDraft(clientId, mutation);
		await this.persistRuntime(entry.session.sessionId, runtime);
		if (result.kind === "conflict") return { ok: false, error: { code: result.reason, message: "Draft revision or mutation identity conflicts" } };
		return { ok: true, value: result.state };
	}

	async release(entry: ManagedSession, toTui = false, instruction = ""): Promise<RuntimeResult<SessionRuntime>> {
		const runtime = await this.bind(entry);
		this.syncWork(runtime, entry);
		const identity = this.identity(runtime);
		const began = toTui ? releaseToTui(this._residency, identity, instruction) : releaseIdleRuntime(this._residency, identity);
		if (!began.ok) return { ok: false, error: began.error };
		this._residency = began.value;
		if (this.options.sessions) await this.options.sessions.release(entry.session.sessionId);
		if (this.options.sessions?.entries.has(entry.session.sessionId)) {
			return { ok: false, error: { code: "invalid-state", message: "Native session release did not complete" } };
		}
		const completed = toTui ? completeTuiHandoff(this._residency, identity) : completeIdleRuntimeRelease(this._residency, identity);
		if (!completed.ok) return { ok: false, error: completed.error };
		this._residency = completed.value;
		this.entries.delete(entry.session.sessionId);
		await this.persistRuntime(entry.session.sessionId, runtime);
		return { ok: true, value: runtime };
	}

	private syncWork(runtime: SessionRuntime, entry: ManagedSession): void {
		const identity = this.identity(runtime);
		if (!this.resident(identity)) return;
		const resident = this._residency.residents.find((candidate) => candidate.sessionKey === identity.sessionKey);
		if (!resident || resident.phase !== "resident") return;
		const desired = {
			activeWorkIds: new Set(runtime.activeWorkIds),
			pendingUiIds: new Set(
				((this.options.sessions?.snapshot(entry).pendingDialogs as readonly Record<string, unknown>[] | undefined) ?? [])
					.map((dialog) => String(dialog.requestId ?? ""))
					.filter(Boolean),
			),
			livenessPinKeys: new Set(entry.hasExtensionWork?.() ? ["extension-work"] : []),
		};
		for (const id of desired.activeWorkIds) if (!resident.work.activeWorkIds.includes(id)) this.applyResidency({ type: "work.admit", identity, workId: id });
		for (const id of resident.work.activeWorkIds) if (!desired.activeWorkIds.has(id)) this.applyResidency({ type: "work.settle", identity, workId: id });
		for (const id of desired.pendingUiIds) if (!resident.work.pendingUiIds.includes(id)) this.applyResidency({ type: "ui.pin", identity, requestId: id });
		for (const id of resident.work.pendingUiIds) if (!desired.pendingUiIds.has(id)) this.applyResidency({ type: "ui.unpin", identity, requestId: id });
		if (desired.livenessPinKeys.size && !resident.work.livenessPinKeys.includes("extension-work")) this.applyResidency({ type: "liveness.pin", identity, key: "extension-work" });
		if (!desired.livenessPinKeys.size && resident.work.livenessPinKeys.includes("extension-work")) this.applyResidency({ type: "liveness.unpin", identity, key: "extension-work" });
	}

	/** Feed native UI projections into the generation-safe passive state. */
	onEvent(sessionId: string, event: unknown): void {
		const runtime = this.runtimes.get(sessionId);
		if (!runtime || !event || typeof event !== "object") return;
		if (runtime.applyPassiveEvent(event as Record<string, unknown>)) void this.persistRuntime(sessionId, runtime).catch(() => {});
	}

	snapshot(sessionId: string, clientId?: string): SessionRuntimeReplay | undefined {
		const runtime = this.runtimes.get(sessionId);
		if (!runtime) return undefined;
		this.syncWorkForSnapshot(runtime);
		const resident = this._residency.residents.find((candidate) => candidate.sessionKey === runtime.identity.sessionKey);
		const work = resident ? classifyRuntimeWork(resident.work) : { kind: "idle" as const, releaseable: true, reason: "settled" as const };
		return runtime.snapshot(clientId, resident ? { phase: resident.phase, releaseable: work.releaseable, reason: work.reason } : undefined);
	}

	private syncWorkForSnapshot(runtime: SessionRuntime): void {
		const entry = this.entries.get(runtime.identity.sessionId)?.deref();
		if (entry) this.syncWork(runtime, entry);
	}

	async scanHistory(source: string, options?: HistoryScanOptions): Promise<HistoryScanResult> {
		return scanNativeHistory(source, options);
	}

	catalog(candidates: readonly Parameters<typeof buildBoundedCatalog>[0][number][], options?: Parameters<typeof buildBoundedCatalog>[1]): CatalogResult {
		return buildBoundedCatalog(candidates, options);
	}

	compareHistorySource(previous: NativeFileFingerprint | null | undefined, current: NativeFileFingerprint | null | undefined) {
		return compareNativeFile(previous, current);
	}

	lastReleasedRuntime(): ReturnType<typeof lastTerminalState> {
		return lastTerminalState(this._residency);
	}

	async close(): Promise<void> {
		this.closed = true;
		if (this.store) {
			for (const [id, runtime] of this.runtimes) await this.persistRuntime(id, runtime).catch(() => {});
		}
	}
}

export { UncertainPromptError };
export type { SessionRuntimeReplay } from "./session-runtime.ts";
export const AssistantRuntimeWiring = RuntimeWiring;
