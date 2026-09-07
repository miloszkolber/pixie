import { expect, test } from "bun:test";
import { loadExpandedFolderChain } from "@/files/tree/directory-loader";
import { MAX_FOLDER_CHAIN_READS } from "@/files/tree/folder-chains";

type Node = { kind: "dir" | "file"; name: string; path: string };

function directory(index: number): Node {
	return { kind: "dir", name: `level-${index}`, path: `level-${index}` };
}

test("collapsed directory rows issue no child reads across watcher ticks", async () => {
	let requests = 0;
	for (let projectTick = 0; projectTick < 50; projectTick += 1) {
		const result = await loadExpandedFolderChain(directory(0), {
			expanded: false,
			projectTick,
			loadedTick: null,
			readChildren: async () => {
				requests += 1;
				return [];
			},
		});
		expect(result).toBeNull();
	}
	expect(requests).toBe(0);
});

test("expanded directory loading is tick-aware and caps compressed-chain reads", async () => {
	let requests = 0;
	const readChildren = async (path: string): Promise<Node[]> => {
		requests += 1;
		const index = Number(path.slice("level-".length));
		return index < MAX_FOLDER_CHAIN_READS + 5 ? [directory(index + 1)] : [];
	};
	const first = await loadExpandedFolderChain(directory(0), {
		expanded: true,
		projectTick: 7,
		loadedTick: null,
		readChildren,
	});
	expect(first?.directory.paths).toHaveLength(MAX_FOLDER_CHAIN_READS);
	expect(first?.directory.children[0]?.path).toBe(`level-${MAX_FOLDER_CHAIN_READS}`);
	expect(requests).toBe(MAX_FOLDER_CHAIN_READS);

	expect(
		await loadExpandedFolderChain(directory(0), {
			expanded: true,
			projectTick: 7,
			loadedTick: first?.loadedTick ?? null,
			readChildren,
		}),
	).toBeNull();
	expect(requests).toBe(MAX_FOLDER_CHAIN_READS);

	expect(
		await loadExpandedFolderChain(directory(0), {
			expanded: false,
			projectTick: 8,
			loadedTick: first?.loadedTick ?? null,
			readChildren,
		}),
	).toBeNull();
	expect(requests).toBe(MAX_FOLDER_CHAIN_READS);

	await loadExpandedFolderChain(directory(0), {
		expanded: true,
		projectTick: 8,
		loadedTick: first?.loadedTick ?? null,
		readChildren,
	});
	expect(requests).toBe(MAX_FOLDER_CHAIN_READS * 2);
});
