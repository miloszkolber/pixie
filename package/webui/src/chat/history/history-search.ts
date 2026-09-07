import type { HistoryScope, HistorySearchResult, MessageHit, PromptHit } from "@pixie/contracts";
import { appStoreApi, type ChatLocationRequest, projectArea } from "@/store";
import { getTransport } from "../../connection";

export type { ChatLocationRequest };

export type HistoryStage = "compact" | "zoomed";

export interface HistorySearchState {
	open: boolean;
	stage: HistoryStage;
	query: string;
	scope: HistoryScope;
	result: HistorySearchResult | null;
	selected: number;
	error: boolean;
}

export type HistorySelection =
	| { kind: "prompt"; hit: PromptHit }
	| { kind: "message"; hit: MessageHit };

export const SCOPE_ORDER = ["chat", "project", "all"] as const;
export type ScopeKind = (typeof SCOPE_ORDER)[number];

export interface HistorySearchContext {
	sessionId: string;
	projectAreaId: string;
	projectId?: string;
}

export interface HistorySearchController {
	subscribe: (run: (state: HistorySearchState) => void) => () => void;
	getState: () => HistorySearchState;
	setContext: (context: HistorySearchContext) => void;
	openOverlay: (seedQuery: string) => void;
	close: () => void;
	setQuery: (query: string) => void;
	cycleScope: () => void;
	setScope: (kind: ScopeKind) => void;
	toggleStage: () => void;
	moveSelection: (delta: number) => void;
	openMessage: (target: ChatLocationRequest) => void;
	destroy: () => void;
}

export function buildHistoryScope(
	kind: ScopeKind,
	sessionId: string,
	projectAreaId: string,
	projectId: string | undefined,
): HistoryScope {
	switch (kind) {
		case "chat":
			return { kind: "chat", sessionId };
		case "project":
			return { kind: "project", projectId: projectId ?? projectAreaId };
		case "all":
			return { kind: "all" };
	}
}

export function historySelectionCount(
	stage: HistoryStage,
	result: HistorySearchResult | null,
): number {
	if (!result) return 0;
	return stage === "compact"
		? result.prompts.length
		: result.prompts.length + result.messages.length;
}

export function resolveHistorySelection(
	stage: HistoryStage,
	result: HistorySearchResult | null,
	selected: number,
): HistorySelection | null {
	if (!result) return null;
	if (selected < result.prompts.length) {
		const hit = result.prompts[selected];
		return hit ? { kind: "prompt", hit } : null;
	}
	if (stage !== "zoomed") return null;
	const hit = result.messages[selected - result.prompts.length];
	return hit ? { kind: "message", hit } : null;
}

export function jumpTarget(hit: PromptHit | MessageHit): ChatLocationRequest | null {
	if (!hit.projectId || hit.messageIndex == null || hit.anchorText == null) return null;
	return {
		projectAreaId: hit.projectId,
		projectId: hit.projectId,
		sessionId: hit.sessionId,
		messageIndex: hit.messageIndex,
		anchorText: hit.anchorText,
	};
}

export function historyOptionKey(item: HistorySelection): string {
	return item.kind === "prompt"
		? `p:${item.hit.sessionId}:${item.hit.messageIndex ?? item.hit.timestamp}`
		: `m:${item.hit.sessionId}:${item.hit.messageIndex}`;
}

export interface HighlightPart {
	key: string;
	text: string;
	highlighted: boolean;
}

function escapeRegExp(term: string): string {
	return term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function highlightHistoryText(text: string, query: string): HighlightPart[] {
	const terms = [...new Set(query.toLocaleLowerCase().split(/\s+/).filter(Boolean))].sort(
		(a, b) => b.length - a.length,
	);
	if (terms.length === 0) return [{ key: "0", text, highlighted: false }];
	const pattern = new RegExp(`(${terms.map(escapeRegExp).join("|")})`, "gi");
	let offset = 0;
	return text.split(pattern).map((part) => {
		const start = offset;
		offset += part.length;
		return {
			text: part,
			key: `${start}:${part}`,
			highlighted: terms.includes(part.toLocaleLowerCase()),
		};
	});
}

export function historySelectionAnnouncement(
	stage: HistoryStage,
	result: HistorySearchResult | null,
	selected: number,
): string {
	const item = resolveHistorySelection(stage, result, selected);
	if (!item) return "No history result selected";
	const firstLine = item.hit.text.split("\n")[0] ?? item.hit.text;
	return `Selected ${selected + 1} of ${historySelectionCount(stage, result)}: ${firstLine}`;
}

function sameContext(left: HistorySearchContext, right: HistorySearchContext): boolean {
	return (
		left.sessionId === right.sessionId &&
		left.projectAreaId === right.projectAreaId &&
		left.projectId === right.projectId
	);
}

export function createHistorySearch(initialContext: HistorySearchContext): HistorySearchController {
	let context = { ...initialContext };
	let scopeKind: ScopeKind = "project";
	let state: HistorySearchState = {
		open: false,
		stage: "compact",
		query: "",
		scope: buildHistoryScope(
			scopeKind,
			context.sessionId,
			context.projectAreaId,
			context.projectId,
		),
		result: null,
		selected: 0,
		error: false,
	};
	const listeners = new Set<(state: HistorySearchState) => void>();
	let searchTimer: ReturnType<typeof setTimeout> | null = null;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let requestToken = 0;
	let destroyed = false;

	function emit(next: Partial<HistorySearchState>): void {
		if (destroyed) return;
		state = { ...state, ...next };
		const length = historySelectionCount(state.stage, state.result);
		if (length === 0) state = { ...state, selected: 0 };
		else if (state.selected >= length) state = { ...state, selected: length - 1 };
		for (const listener of listeners) listener(state);
	}

	function clearTimers(): void {
		if (searchTimer !== null) clearTimeout(searchTimer);
		if (retryTimer !== null) clearTimeout(retryTimer);
		searchTimer = null;
		retryTimer = null;
	}

	function invalidateSearch(): number {
		requestToken += 1;
		clearTimers();
		return requestToken;
	}

	function scheduleRetry(): void {
		if (destroyed || !state.open || !state.result?.indexing) return;
		if (retryTimer !== null) clearTimeout(retryTimer);
		const token = requestToken;
		retryTimer = setTimeout(() => {
			retryTimer = null;
			void requestSearch(token);
		}, 300);
	}

	async function requestSearch(token: number): Promise<void> {
		const query = state.query;
		const scope = state.scope;
		try {
			const result = await getTransport().request("history.search", { query, scope, limit: 50 });
			if (destroyed || token !== requestToken || !state.open) return;
			emit({ result, error: false });
			if (result.indexing) scheduleRetry();
		} catch {
			if (destroyed || token !== requestToken || !state.open) return;
			emit({ error: true });
		}
	}

	function scheduleSearch(): void {
		const token = invalidateSearch();
		if (!state.open || destroyed) return;
		searchTimer = setTimeout(() => {
			searchTimer = null;
			void requestSearch(token);
		}, 100);
	}

	function resetForParamsChange(next: Partial<HistorySearchState> = {}): void {
		emit({ selected: 0, result: null, error: false, ...next });
		scheduleSearch();
	}

	return {
		subscribe: (run) => {
			run(state);
			listeners.add(run);
			return () => listeners.delete(run);
		},
		getState: () => state,
		setContext: (nextContext) => {
			if (sameContext(context, nextContext)) return;
			context = { ...nextContext };
			emit({
				scope: buildHistoryScope(
					scopeKind,
					context.sessionId,
					context.projectAreaId,
					context.projectId,
				),
			});
			scheduleSearch();
		},
		openOverlay: (seedQuery) => {
			scopeKind = "project";
			resetForParamsChange({
				open: true,
				stage: "compact",
				query: seedQuery,
				scope: buildHistoryScope(
					scopeKind,
					context.sessionId,
					context.projectAreaId,
					context.projectId,
				),
			});
		},
		close: () => {
			emit({ open: false });
			invalidateSearch();
		},
		setQuery: (query) => resetForParamsChange({ query }),
		cycleScope: () => {
			scopeKind =
				SCOPE_ORDER[(SCOPE_ORDER.indexOf(scopeKind) + 1) % SCOPE_ORDER.length] ?? scopeKind;
			resetForParamsChange({
				scope: buildHistoryScope(
					scopeKind,
					context.sessionId,
					context.projectAreaId,
					context.projectId,
				),
			});
		},
		setScope: (kind) => {
			scopeKind = kind;
			resetForParamsChange({
				scope: buildHistoryScope(
					scopeKind,
					context.sessionId,
					context.projectAreaId,
					context.projectId,
				),
			});
		},
		toggleStage: () => {
			emit({ stage: state.stage === "compact" ? "zoomed" : "compact" });
		},
		moveSelection: (delta) => {
			const length = historySelectionCount(state.stage, state.result);
			emit({ selected: length === 0 ? 0 : (state.selected + delta + length) % length });
		},
		openMessage: (target) => {
			try {
				if (!appStoreApi.getState().projectAreas[target.projectId]?.length) {
					const project = appStoreApi
						.getState()
						.projects.find((candidate) => candidate.id === target.projectId);
					if (project) {
						appStoreApi.getState().setProjectAreas(target.projectId, [projectArea(project)]);
					}
				}
			} catch {
				// A stale project catalog must not prevent the explicit history jump.
			}
			appStoreApi.getState().requestChatLocation(target);
			emit({ open: false });
			invalidateSearch();
		},
		destroy: () => {
			if (destroyed) return;
			destroyed = true;
			invalidateSearch();
			listeners.clear();
		},
	};
}
