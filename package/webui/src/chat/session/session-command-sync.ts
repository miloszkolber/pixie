import { getTransport } from "../../connection";
import type { AppState } from "../../store";
import { appStoreApi, selectSkillsStale } from "../../store";

export interface SessionCommandSyncContext {
	sessionId: string;
	projectAreaId: string;
}

export interface SessionCommandSyncInputs {
	sessionReady: boolean;
	connectedGeneration: number;
	commandCatalogGeneration: number;
	piAgent: boolean;
	skillVersion: number;
}

export interface SessionCommandSyncController {
	setContext: (context: SessionCommandSyncContext) => void;
	refresh: () => void;
	destroy: () => void;
}

export function sessionCommandSyncInputs(
	state: AppState,
	{ sessionId, projectAreaId }: SessionCommandSyncContext,
): SessionCommandSyncInputs {
	return {
		sessionReady: state.sessions[sessionId] !== undefined,
		connectedGeneration: state.status === "connected" ? state.connectionGeneration : 0,
		commandCatalogGeneration: state.commandCatalogGeneration,
		piAgent: state.agentProfile?.pi === true,
		skillVersion: selectSkillsStale(state, projectAreaId, sessionId)
			? (state.skillChangeTickByProjectArea[projectAreaId] ?? 0)
			: (state.skillsSyncedTickBySession[sessionId] ?? 0),
	};
}

function sameInputs(left: SessionCommandSyncInputs, right: SessionCommandSyncInputs): boolean {
	return (
		left.sessionReady === right.sessionReady &&
		left.connectedGeneration === right.connectedGeneration &&
		left.commandCatalogGeneration === right.commandCatalogGeneration &&
		left.piAgent === right.piAgent &&
		left.skillVersion === right.skillVersion
	);
}

function sameContext(left: SessionCommandSyncContext, right: SessionCommandSyncContext): boolean {
	return left.sessionId === right.sessionId && left.projectAreaId === right.projectAreaId;
}

export function createSessionCommandSync(
	initialContext: SessionCommandSyncContext,
): SessionCommandSyncController {
	let context = { ...initialContext };
	let inputs = sessionCommandSyncInputs(appStoreApi.getState(), context);
	let abort: AbortController | null = null;
	let destroyed = false;

	function refresh(): void {
		abort?.abort();
		abort = null;
		if (destroyed || !inputs.piAgent || !inputs.sessionReady || inputs.connectedGeneration === 0) {
			return;
		}
		const requestedContext = { ...context };
		const requestedInputs = { ...inputs };
		const startingState = appStoreApi.getState();
		const syncedTick = startingState.skillChangeTickByProjectArea[context.projectAreaId] ?? 0;
		const commandRevision = startingState.sessions[context.sessionId]?.commandRevision ?? 0;
		const requestAbort = new AbortController();
		abort = requestAbort;
		void getTransport()
			.request(
				"session.getCommands",
				{ sessionId: requestedContext.sessionId },
				{ signal: requestAbort.signal },
			)
			.then((commands) => {
				if (requestAbort.signal.aborted || destroyed || !sameContext(context, requestedContext)) {
					return;
				}
				const state = appStoreApi.getState();
				const currentInputs = sessionCommandSyncInputs(state, requestedContext);
				if (!sameInputs(currentInputs, requestedInputs)) return;
				state.setCommands(requestedContext.sessionId, commands, commandRevision);
				state.markSkillsSynced(requestedContext.sessionId, syncedTick);
			})
			.catch(() => {});
	}

	const unsubscribe = appStoreApi.subscribe((state) => {
		if (destroyed) return;
		const next = sessionCommandSyncInputs(state, context);
		if (sameInputs(next, inputs)) return;
		inputs = next;
		refresh();
	});

	refresh();

	return {
		setContext: (nextContext) => {
			if (sameContext(context, nextContext)) return;
			context = { ...nextContext };
			inputs = sessionCommandSyncInputs(appStoreApi.getState(), context);
			refresh();
		},
		refresh,
		destroy: () => {
			if (destroyed) return;
			destroyed = true;
			abort?.abort();
			abort = null;
			unsubscribe();
		},
	};
}
