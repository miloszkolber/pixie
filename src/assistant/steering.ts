/**
 * AUX-03 steering prototype.
 *
 * Pi 0.86.1's public `AgentSession.steer` queues input for whatever run is
 * streaming, but it exposes no public active-run identity and no steering
 * receipt. This registry models the strongest binding the host can build from
 * its own AUX-05 allocation generation plus the prompt `preflightResult`
 * acceptance:
 *
 *   - a prompt that preflight accepted opens one binding for that resident
 *     generation;
 *   - the binding is dropped when its prompt settles or the resident is
 *     retired;
 *   - compaction marks the session as unsteerable while it rewrites the run
 *     queue.
 *
 * This is deliberately not wired into a negotiable route. The spike conclusion
 * is that generation plus preflight acceptance is necessary but not sufficient:
 * preflight reports that a *prompt* was accepted, not that a later steer call
 * targets that run, and there is no public receipt that the steer was bound or
 * delivered. `session.steer` therefore stays unavailable until Pi exposes a
 * public active-run identity or a steering acceptance result.
 */

export interface SteeringBinding {
	readonly sessionId: string;
	readonly generation: number;
	readonly acceptedAt: number;
}

export type SteeringEvaluation =
	| { readonly ok: true; readonly binding: SteeringBinding }
	| { readonly ok: false; readonly reason: string };

export class SteeringRegistry {
	private readonly active = new Map<string, SteeringBinding>();
	private readonly compacting = new Set<string>();

	/** Open the binding for a preflight-accepted prompt on one allocation. */
	begin(sessionId: string, generation: number): SteeringBinding {
		const binding: SteeringBinding = { sessionId, generation, acceptedAt: Date.now() };
		this.active.set(sessionId, binding);
		return binding;
	}

	/** Drop exactly this prompt's binding when its request settles. */
	end(binding: SteeringBinding): void {
		if (this.active.get(binding.sessionId) === binding) this.active.delete(binding.sessionId);
	}

	/** Drop any binding for a resident that is being replaced or released. */
	retire(sessionId: string, generation: number): void {
		const binding = this.active.get(sessionId);
		if (binding?.generation === generation) this.active.delete(sessionId);
	}

	/** Mark a session's run queue as being rewritten by compaction. */
	noteCompaction(sessionId: string, compacting: boolean): void {
		if (compacting) this.compacting.add(sessionId);
		else this.compacting.delete(sessionId);
	}

	/**
	 * Decide whether a steering request can be bound to the active run. A
	 * rejection reason is retained for the unavailable-route diagnostic.
	 */
	evaluate(sessionId: string, generation: number, streaming: boolean): SteeringEvaluation {
		const binding = this.active.get(sessionId);
		if (!binding) return { ok: false, reason: "no preflight-accepted run is bound" };
		if (binding.generation !== generation)
			return {
				ok: false,
				reason: "resident generation changed after preflight acceptance",
			};
		if (this.compacting.has(sessionId)) return { ok: false, reason: "compaction is in progress" };
		if (!streaming) return { ok: false, reason: "session is not streaming" };
		return { ok: true, binding };
	}
}
