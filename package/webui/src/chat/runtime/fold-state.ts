import { getContext, setContext } from "svelte";

/** Owned by one session runtime; released with that runtime, never globally retained. */
export function createFoldState() {
	const folds = new Map<string, boolean>();
	const selections = new Map<string, string | null>();
	return {
		readFold: (id: string, fallback = false) => folds.get(id) ?? fallback,
		writeFold(id: string, expanded: boolean) {
			folds.set(id, expanded);
			return expanded;
		},
		toggleFold(id: string, current: boolean) {
			folds.set(id, !current);
			return !current;
		},
		readSelection: (id: string) => selections.get(id) ?? null,
		selectValue(id: string, current: string | null, key: string) {
			const next = current === key ? null : key;
			selections.set(id, next);
			return next;
		},
	};
}
export type FoldState = ReturnType<typeof createFoldState>;
const contextKey = Symbol("chat-disclosures");
export function setFoldStateContext(value: () => FoldState): void {
	setContext(contextKey, value);
}
export function useFoldState(): FoldState {
	const get = getContext<() => FoldState>(contextKey);
	if (!get) return createFoldState();
	return {
		readFold: (...args) => get().readFold(...args),
		writeFold: (...args) => get().writeFold(...args),
		toggleFold: (...args) => get().toggleFold(...args),
		readSelection: (...args) => get().readSelection(...args),
		selectValue: (...args) => get().selectValue(...args),
	};
}
