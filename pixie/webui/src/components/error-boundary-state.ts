const CHUNK_ERROR_PATTERNS = [
	"dynamically imported module",
	"importing a module script failed",
	"error loading dynamically imported module",
];

export function isChunkLoadError(error: unknown): boolean {
	const message = (error instanceof Error ? error.message : String(error ?? "")).toLowerCase();
	return CHUNK_ERROR_PATTERNS.some((pattern) => message.includes(pattern));
}
