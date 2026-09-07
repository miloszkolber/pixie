export type HrefKind = "empty" | "anchor" | "external" | "relative";

export function classifyHref(href: string | undefined): HrefKind {
	if (!href) return "empty";
	if (href.startsWith("#")) return "anchor";
	if (href.startsWith("//") || /^[a-z][a-z0-9+.-]*:/i.test(href)) return "external";
	return "relative";
}

export function resolveRelativePath(fromFile: string, href: string): string {
	const dir = fromFile.includes("/") ? fromFile.slice(0, fromFile.lastIndexOf("/")) : "";
	const segments = href.startsWith("/") || dir === "" ? [] : dir.split("/");
	for (const encodedSegment of href.split("/")) {
		let segment = encodedSegment;
		try {
			segment = decodeURIComponent(encodedSegment);
		} catch {}
		if (segment === "" || segment === ".") continue;
		if (segment === "..") segments.pop();
		else segments.push(segment);
	}
	return segments.join("/");
}

export function splitHash(href: string): { path: string; hash: string } {
	const index = href.indexOf("#");
	return index < 0
		? { path: href, hash: "" }
		: { path: href.slice(0, index), hash: href.slice(index + 1) };
}

function encodePath(path: string): string {
	return path.split("/").map(encodeURIComponent).join("/");
}

export function projectFileUrl(
	httpBase: string,
	projectAreaId: string,
	fromPath: string,
	source: string,
): string | undefined {
	const suffixAt = source.search(/[?#]/);
	const sourcePath = suffixAt < 0 ? source : source.slice(0, suffixAt);
	const suffix = suffixAt < 0 ? "" : source.slice(suffixAt);
	const target = resolveRelativePath(fromPath, sourcePath);
	if (!target) return undefined;
	return `${httpBase}/files/${encodeURIComponent(projectAreaId)}/${encodePath(target)}${suffix}`;
}
