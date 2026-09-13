const DOCUMENT_ID = "[a-z2-7]{16,128}";
const ARTIFACT_ID = "(?:cover|[a-z0-9][a-z0-9_-]{0,126})";
const DESIGN_REFERENCE = new RegExp(
	`^pixie://design/artifacts/(${DOCUMENT_ID})/(${ARTIFACT_ID})\\.png$`,
);
const DESIGN_PATH = new RegExp(`^/api/design/artifacts/(${DOCUMENT_ID})/(${ARTIFACT_ID})\\.png$`);

function safeBaseUrl(baseUrl: string | URL | undefined): string | null {
	if (baseUrl === undefined) return "";
	const value = String(baseUrl);
	if (!value || value.includes("?") || value.includes("#") || value.includes("\\")) return null;
	if (value.startsWith("/") && !value.startsWith("//"))
		return value.endsWith("/") ? value.slice(0, -1) : value;
	try {
		const parsed = new URL(value);
		if (
			(parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
			parsed.username ||
			parsed.password ||
			parsed.search ||
			parsed.hash
		) {
			return null;
		}
		return parsed.origin + parsed.pathname.replace(/\/$/, "");
	} catch {
		return null;
	}
}

function joinArtifactPath(path: string, baseUrl: string | URL | undefined): string | null {
	const base = safeBaseUrl(baseUrl);
	if (base === null) return null;
	return `${base}${path}`;
}

/** Build a same-origin, cookie-authenticated URL for a saved Design artifact. */
export function designArtifactUrl(reference: string, baseUrl?: string | URL): string | null;
export function designArtifactUrl(
	documentId: string,
	artifact: string,
	baseUrl?: string | URL,
): string | null;
export function designArtifactUrl(
	first: string,
	second?: string | URL,
	third?: string | URL,
): string | null {
	if (first.includes("?") || first.includes("#")) return null;
	let documentId: string;
	let artifact: string;
	let baseUrl: string | URL | undefined;
	if (first.startsWith("pixie://") || first.startsWith("/api/design/")) {
		const match = DESIGN_REFERENCE.exec(first) ?? DESIGN_PATH.exec(first);
		if (!match?.[1] || !match[2]) return null;
		documentId = match[1];
		artifact = match[2];
		baseUrl = second;
	} else {
		documentId = first;
		artifact = typeof second === "string" ? second : "cover";
		baseUrl = second instanceof URL ? second : third;
	}
	if (!new RegExp(`^${DOCUMENT_ID}$`).test(documentId)) return null;
	if (!new RegExp(`^${ARTIFACT_ID}$`).test(artifact) || artifact.includes("?")) return null;
	return joinArtifactPath(`/api/design/artifacts/${documentId}/${artifact}.png`, baseUrl);
}

export function buildDesignArtifactUrl(
	documentId: string,
	artifact = "cover",
	baseUrl?: string | URL,
): string | null {
	return designArtifactUrl(documentId, artifact, baseUrl);
}

export const authenticatedDesignArtifactUrl = designArtifactUrl;
