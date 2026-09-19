export function webHost(url: string): string {
	try {
		return new URL(url).hostname.replace(/^www\./, "");
	} catch {
		return url;
	}
}

export function firstWebUrl(args: Record<string, unknown>): string {
	if (typeof args.url === "string" && args.url) return args.url;
	return Array.isArray(args.urls) && typeof args.urls[0] === "string" ? args.urls[0] : "";
}

export function firstWebQuery(args: Record<string, unknown>): string {
	if (typeof args.query === "string" && args.query) return args.query;
	return Array.isArray(args.queries) && typeof args.queries[0] === "string" ? args.queries[0] : "";
}

export function webSearchProvider(result: unknown): string {
	const details = result && typeof result === "object" ? Reflect.get(result, "details") : undefined;
	if (!details || typeof details !== "object") return "";
	const results = Reflect.get(details, "results");
	const provider =
		Reflect.get(details, "provider") ??
		(Array.isArray(results) && results[0] && typeof results[0] === "object"
			? Reflect.get(results[0], "provider")
			: undefined);
	return typeof provider === "string" ? provider : "";
}
