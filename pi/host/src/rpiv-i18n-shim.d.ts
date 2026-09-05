// Type-only shim for the optional `@juicesharp/rpiv-i18n` peer of
// `@juicesharp/rpiv-todo` and `@juicesharp/rpiv-ask-user-question`. The peer
// stays uninstalled: both extensions degrade to English fallbacks at runtime.
// This file only lets `tsc` resolve the dynamic imports they guard with
// try/catch.
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
