import { expect, test } from "bun:test";
import { createExternalStore, toReadableStore } from "@/store/external-store";

interface CounterState {
	count: number;
	label: string;
}

function createCounterStore() {
	return createExternalStore<CounterState>(() => ({ count: 0, label: "initial" }));
}

test("imperative subscribers receive merged updates and the previous state", () => {
	const store = createCounterStore();
	const changes: [CounterState, CounterState][] = [];
	const unsubscribe = store.subscribe((state, previous) => changes.push([state, previous]));

	store.setState((state) => ({ count: state.count + 1 }));
	unsubscribe();
	store.setState({ count: 2 });

	expect(store.getState()).toEqual({ count: 2, label: "initial" });
	expect(changes).toEqual([
		[
			{ count: 1, label: "initial" },
			{ count: 0, label: "initial" },
		],
	]);
});

test("replacement and initial-state semantics remain stable", () => {
	const store = createCounterStore();
	const initialState = store.getInitialState();
	let updates = 0;
	store.subscribe(() => {
		updates += 1;
	});

	store.setState((state) => state);
	store.setState({ count: 4, label: "replacement" }, true);

	expect(updates).toBe(1);
	expect(store.getState()).toEqual({ count: 4, label: "replacement" });
	expect(store.getInitialState()).toBe(initialState);
	expect(initialState).toEqual({ count: 0, label: "initial" });
});

test("equivalent partial updates do not invalidate readable subscribers", () => {
	const store = createCounterStore();
	let updates = 0;
	store.subscribe(() => {
		updates += 1;
	});

	store.setState({});
	store.setState({ count: 0 });
	store.setState((state) => ({ label: state.label }));

	expect(updates).toBe(0);
	expect(store.getState()).toBe(store.getInitialState());
});

test("readable subscribers receive the current state immediately", () => {
	const store = createCounterStore();
	const readable = toReadableStore(store);
	const counts: number[] = [];
	let invalidations = 0;
	const unsubscribe = readable.subscribe(
		(state) => counts.push(state.count),
		() => {
			invalidations += 1;
		},
	);

	store.setState({ count: 1 });
	unsubscribe();
	store.setState({ count: 2 });

	expect(counts).toEqual([0, 1]);
	expect(invalidations).toBe(1);
});
