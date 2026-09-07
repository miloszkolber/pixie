// Wire-frame serialization for the host protocol. Native Pi events are
// forwarded verbatim, so a malformed or self-referential event payload must
// degrade to a documented stub instead of throwing inside the emit loop or
// wedging a peer connection.

const UNSERIALIZABLE_EVENT_TYPE = "host.unserializable";

export interface EventFrame {
	/** Buffered object form, used for session-load backlog replay. */
	message: { method: "session.event"; params: { sessionId: string; event: unknown; sequence: number } };
	/** Serialized form, used for direct fan-out and byte accounting. */
	data: string;
}

export function serializeFrame(value: unknown): string {
	try {
		return JSON.stringify(value);
	} catch {
		const record = value as { id?: unknown } | null;
		const id =
			record && typeof record === "object" && Number.isSafeInteger(record.id) ? record.id : undefined;
		return JSON.stringify(
			id === undefined
				? { method: UNSERIALIZABLE_EVENT_TYPE }
				: { id, error: { code: -32000, message: "Frame is not serializable" } },
		);
	}
}

export function buildEventFrame(sessionId: string, event: unknown, sequence: number): EventFrame {
	const message = {
		method: "session.event" as const,
		params: { sessionId, event, sequence },
	};
	try {
		return { message, data: JSON.stringify(message) };
	} catch {
		const originalType = (event as { type?: unknown } | null)?.type;
		const stub = {
			method: "session.event" as const,
			params: {
				sessionId,
				event: {
					type: UNSERIALIZABLE_EVENT_TYPE,
					originalType: typeof originalType === "string" ? originalType : null,
				},
				sequence,
			},
		};
		return { message: stub, data: JSON.stringify(stub) };
	}
}
