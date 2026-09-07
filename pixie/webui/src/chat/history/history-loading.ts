import type { TranscriptPage } from "@pixie/contracts";

export type TranscriptLoadOutcome = "loaded" | "reloaded" | "exhausted" | "ignored" | "failed";

/** Load serialized pages until a history target is present or progress stops. */
export async function loadTranscriptUntil(
	messageIndex: number,
	getTranscript: () => TranscriptPage | null | undefined,
	loadEarlier: () => Promise<TranscriptLoadOutcome>,
	isCurrent: () => boolean,
): Promise<void> {
	const visited = new Set<string>();
	while (isCurrent()) {
		const transcript = getTranscript();
		if (!transcript || messageIndex >= transcript.start) return;
		const cursor = `${transcript.projectionId}:${transcript.start}`;
		if (visited.has(cursor)) return;
		visited.add(cursor);
		const outcome = await loadEarlier();
		if (outcome !== "loaded" && outcome !== "reloaded") return;
	}
}
