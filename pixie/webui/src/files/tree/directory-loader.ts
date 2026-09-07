import {
	type FolderChainNode,
	MAX_FOLDER_CHAIN_READS,
	type ResolvedFolderChain,
	resolveFolderChain,
} from "./folder-chains";

interface DirectoryLoadOptions<TNode extends FolderChainNode> {
	expanded: boolean;
	projectTick: number;
	loadedTick: number | null;
	readChildren: (path: string) => Promise<readonly TNode[]>;
	maxReads?: number;
}

export interface DirectoryLoadResult<TNode extends FolderChainNode> {
	directory: ResolvedFolderChain<TNode>;
	loadedTick: number;
}

export async function loadExpandedFolderChain<TNode extends FolderChainNode>(
	node: TNode,
	options: DirectoryLoadOptions<TNode>,
): Promise<DirectoryLoadResult<TNode> | null> {
	if (node.kind !== "dir" || !options.expanded || options.loadedTick === options.projectTick) {
		return null;
	}
	return {
		directory: await resolveFolderChain(
			node,
			options.readChildren,
			options.maxReads ?? MAX_FOLDER_CHAIN_READS,
		),
		loadedTick: options.projectTick,
	};
}
