// Type-only shim for the optional `@juicesharp/rpiv-i18n` peer of
// `@juicesharp/rpiv-todo`. The peer stays uninstalled: rpiv-todo degrades to
// English fallbacks at runtime. This file only lets `tsc` resolve the two
// dynamic imports rpiv-todo guards with try/catch.
declare module "@juicesharp/rpiv-i18n" {
	export function scope(namespace: string): (key: string, fallback: string) => string;
}
declare module "@juicesharp/rpiv-i18n/loader" {
	export function registerLocalesFromDir(
		namespace: string,
		packageUrl: string,
		options?: { label?: string },
	): void;
}
