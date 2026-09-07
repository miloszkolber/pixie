export function snapshotReferences(snapshot: string): string[] {
	const references: string[] = [];
	const seen = new Set<string>();
	for (const match of snapshot.matchAll(/@[A-Za-z0-9_-]{1,128}|\bref=([A-Za-z0-9_-]{1,128})\b/g)) {
		const reference = match[0].startsWith("@") ? match[0] : `@${match[1]}`;
		if (seen.has(reference)) continue;
		seen.add(reference);
		references.push(reference);
	}
	return references;
}

export function browserPanelScreenState(
	loading: boolean,
	error: string | null,
	screenshot: string | null,
): "loading" | "error" | "empty" | "ready" {
	if (loading) return "loading";
	if (error) return "error";
	return screenshot ? "ready" : "empty";
}
