/** Pure filename-filter matching for the bounded read-only file tree. */

export interface FilterableNode {
	kind: string;
	name: string;
	children?: readonly FilterableNode[] | undefined;
}

export function normalizeFileFilter(filter: string): string {
	return filter.trim().toLowerCase();
}

export function nodeNameMatchesFilter(node: Pick<FilterableNode, "name">, query: string): boolean {
	if (!query) return true;
	return node.name.toLowerCase().includes(query);
}

/** True when any node in a loaded subtree matches, recursing into nested children. */
export function subtreeHasMatch(
	nodes: readonly FilterableNode[] | null | undefined,
	query: string,
): boolean {
	if (!query) return true;
	if (!nodes) return false;
	return nodes.some(
		(node) => nodeNameMatchesFilter(node, query) || subtreeHasMatch(node.children, query),
	);
}
