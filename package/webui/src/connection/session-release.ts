import type { WsParams, WsResult } from "@pixie/contracts";
import type { WsTransport } from "./transport";
import { getTransport } from "./wire-transport";

export interface ReleaseSessionInput {
	projectId: string;
	sessionId: string;
}

type ReleaseTransport = Pick<WsTransport, "request">;

/**
 * Release one session's idle runtime through the authoritative
 * `session.release` transport. The backend verifies idleness and retains
 * history, drafts, queue, selection, and metadata for later reattachment, so
 * callers must gate the affordance on backend-reported eligibility and
 * surface backend refusal verbatim. Lease synchronization stays with the
 * existing `session.setLeases` owner; this wrapper never mutates local store.
 */
export function releaseSession(
	input: ReleaseSessionInput,
	transport: ReleaseTransport = getTransport(),
): Promise<WsResult<"session.release">> {
	const params: WsParams<"session.release"> = {
		projectId: input.projectId,
		sessionId: input.sessionId,
	};
	return transport.request("session.release", params);
}
