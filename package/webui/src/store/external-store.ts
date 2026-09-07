export type StoreListener<T> = (state: T, previousState: T) => void;

export interface SetState<T> {
	(partial: T | Partial<T> | ((state: T) => T | Partial<T>), replace?: false): void;
	(state: T | ((state: T) => T), replace: true): void;
}

export interface StoreApi<T> {
	setState: SetState<T>;
	getState: () => T;
	getInitialState: () => T;
	subscribe: (listener: StoreListener<T>) => () => void;
}

export type StateCreator<
	T,
	_InMutators extends [unknown, unknown][] = [],
	_OutMutators extends [unknown, unknown][] = [],
	Slice = T,
> = (set: SetState<T>, get: () => T, store: StoreApi<T>) => Slice;

export interface ReadableStore<T> {
	subscribe: (run: (value: T) => void, invalidate?: () => void) => () => void;
}

export function createExternalStore<T>(initializer: StateCreator<T>): StoreApi<T> {
	let state: T;
	let initialState: T;
	const listeners = new Set<StoreListener<T>>();

	const setState: SetState<T> = (partial, replace) => {
		const nextState =
			typeof partial === "function" ? (partial as (state: T) => T | Partial<T>)(state) : partial;
		if (Object.is(nextState, state)) return;

		const previousState = state;
		if (replace === true || typeof nextState !== "object" || nextState === null) {
			state = nextState as T;
		} else {
			const patch = nextState as Record<PropertyKey, unknown>;
			const current = state as Record<PropertyKey, unknown>;
			if (Reflect.ownKeys(patch).every((key) => Object.is(patch[key], current[key]))) return;
			state = Object.assign({}, state, nextState);
		}
		for (const listener of listeners) listener(state, previousState);
	};

	const store: StoreApi<T> = {
		setState,
		getState: () => state,
		getInitialState: () => initialState,
		subscribe: (listener) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
	};
	initialState = state = initializer(store.setState, store.getState, store);
	return store;
}

export function toReadableStore<T>(store: StoreApi<T>): ReadableStore<T> {
	return {
		subscribe: (run, invalidate) => {
			run(store.getState());
			return store.subscribe((state) => {
				invalidate?.();
				run(state);
			});
		},
	};
}
