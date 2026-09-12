export const DEFAULT_SERVICE_DRAIN_DEADLINE_MS = 25_000;
export const DEFAULT_ADMIN_DEADLINE_MS = 30_000;
export const DEFAULT_HELLO_DEADLINE_MS = 10_000;
export const DEFAULT_AUTH_DEADLINE_MS = 10 * 60 * 1000;

export class DeadlineExceededError extends Error {
	readonly code = "DEADLINE_EXCEEDED";

	constructor(
		readonly operation: string,
		readonly timeoutMs: number,
	) {
		super(`${operation} exceeded ${timeoutMs}ms deadline`);
		this.name = "DeadlineExceededError";
	}
}

export interface Deadline {
	readonly signal: AbortSignal;
	readonly expiresAt: number;
	remaining(): number;
	race<T>(operation: PromiseLike<T>, label: string): Promise<T>;
	abort(reason?: unknown): void;
	dispose(): void;
}

/**
 * One deadline is shared by every part of a lifecycle operation. Individual
 * races therefore cannot extend the service drain by starting a fresh timer.
 */
export function createDeadline(timeoutMs: number, operation = "Operation"): Deadline {
	const bounded = Number.isFinite(timeoutMs) ? Math.max(1, Math.floor(timeoutMs)) : 1;
	const expiresAt = Date.now() + bounded;
	const controller = new AbortController();
	const timer = setTimeout(
		() => controller.abort(new DeadlineExceededError(operation, bounded)),
		bounded,
	);
	timer.unref?.();
	return {
		signal: controller.signal,
		expiresAt,
		remaining: () => Math.max(0, expiresAt - Date.now()),
		race: <T>(promise: PromiseLike<T>, label: string): Promise<T> => {
			if (controller.signal.aborted)
				return Promise.reject(
					controller.signal.reason ?? new DeadlineExceededError(label, bounded),
				);
			const remaining = Math.max(0, expiresAt - Date.now());
			if (!remaining) return Promise.reject(new DeadlineExceededError(label, bounded));
			let timeout: ReturnType<typeof setTimeout> | undefined;
			const cutoff = new Promise<T>((_resolve, reject) => {
				timeout = setTimeout(() => {
					const error = new DeadlineExceededError(label, bounded);
					controller.abort(error);
					reject(error);
				}, remaining);
				timeout.unref?.();
			});
			return Promise.race([promise, cutoff]).finally(() => {
				if (timeout !== undefined) clearTimeout(timeout);
			});
		},
		abort: (reason?: unknown) => controller.abort(reason),
		dispose: () => clearTimeout(timer),
	};
}
