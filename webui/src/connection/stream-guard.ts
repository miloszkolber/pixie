/**
 * AUX-16 client-side stream guard.
 *
 * Server channel frames carry a monotonic `seq` and, for appendable snapshots,
 * a `rev`/`baseRev` chain with an `appended` suffix. This guard tracks the
 * chain and reports a gap the moment it sees one, so the transport can request
 * a fresh snapshot instead of continuing with partial state.
 */

export interface StreamGuardFrame {
	channel: string | undefined;
	seq: number | undefined;
	rev: number | undefined;
	baseRev: number | undefined;
	appended?: unknown;
	data?: unknown;
}

export interface StreamGuardDecision {
	/** True when the frame must be dropped because the chain is broken. */
	resync: boolean;
}

/** A fresh snapshot (welcome/resync) resets every channel chain. */
export class StreamGuard {
	private seq = 0;
	private revisions = new Map<string, number>();

	acceptWelcome(): void {
		this.revisions.clear();
		// The welcome snapshot precedes any sequenced frame; the server restarts
		// the sequence on a new socket, so the next frame is the new baseline.
		this.seq = 0;
	}

	/**
	 * Accepts one channel frame. A non-finite/gapped sequence or a delta whose
	 * baseRev does not match the stored chain returns `resync`. A snapshot or a
	 * matching delta updates the chain and returns `resync: false`.
	 */
	accept(frame: StreamGuardFrame): StreamGuardDecision {
		const channel = frame.channel ?? "";
		const seq = frameNumber(frame.seq);
		const rev = frameNumber(frame.rev);
		const baseRev = frameNumber(frame.baseRev);
		if (channel === "" || seq === null || rev === null || baseRev === null) {
			// Unframed or legacy frame: pass it through unchanged.
			return { resync: false };
		}
		if (this.seq !== 0 && seq !== this.seq + 1) {
			return { resync: true };
		}
		this.seq = seq;
		if (baseRev === 0) {
			this.revisions.set(channel, rev);
			return { resync: false };
		}
		if (this.revisions.get(channel) !== baseRev) {
			return { resync: true };
		}
		this.revisions.set(channel, rev);
		return { resync: false };
	}
}

/** Reads a non-negative safe integer from a frame field; null when absent. */
function frameNumber(value: number | undefined): number | null {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}
