import { getContext, setContext } from "svelte";

// AUX-14: the work area owns session lifecycle actions. It exposes the
// edit-from-here handler to the transcript so a per-turn affordance can branch
// the rendered session in-file without every intermediate render component
// threading a callback. The selected native entry id is supplied by the turn;
// the work area builds the SessionLifecycleTarget and sends it.
export interface SessionBranchActions {
	editFromHere(entryId: string): void;
}

const SESSION_BRANCH_CONTEXT = Symbol("pixie.session-branch");

export function setSessionBranchContext(actions: SessionBranchActions): SessionBranchActions {
	return setContext(SESSION_BRANCH_CONTEXT, actions);
}

export function getSessionBranchContext(): SessionBranchActions | null {
	return getContext<SessionBranchActions | undefined>(SESSION_BRANCH_CONTEXT) ?? null;
}
