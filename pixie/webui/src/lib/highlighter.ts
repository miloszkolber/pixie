import type { HighlighterCore } from "shiki/core";
import { PIXIE_SHIKI_THEME, PIXIE_SHIKI_THEME_NAME } from "./shiki-theme";

const LANGUAGE_LOADERS = {
	typescript: () => import("@shikijs/langs/typescript"),
	tsx: () => import("@shikijs/langs/tsx"),
	javascript: () => import("@shikijs/langs/javascript"),
	jsx: () => import("@shikijs/langs/jsx"),
	json: () => import("@shikijs/langs/json"),
	bash: () => import("@shikijs/langs/bash"),
	python: () => import("@shikijs/langs/python"),
	go: () => import("@shikijs/langs/go"),
	css: () => import("@shikijs/langs/css"),
	html: () => import("@shikijs/langs/html"),
	markdown: () => import("@shikijs/langs/markdown"),
	diff: () => import("@shikijs/langs/diff"),
	yaml: () => import("@shikijs/langs/yaml"),
} as const;
type CanonicalLanguage = keyof typeof LANGUAGE_LOADERS;

const ALIAS: Record<string, string> = {
	ts: "typescript",
	js: "javascript",
	mjs: "javascript",
	cjs: "javascript",
	py: "python",
	golang: "go",
	sh: "bash",
	shell: "bash",
	zsh: "bash",
	md: "markdown",
	yml: "yaml",
};

export { languageForPath } from "./language";

class RetryablePromiseCache<Key, Value> {
	readonly #pending = new Map<Key, Promise<Value>>();

	get(key: Key, load: () => Promise<Value>): Promise<Value> {
		const current = this.#pending.get(key);
		if (current) return current;
		const pending = Promise.resolve().then(load);
		this.#pending.set(key, pending);
		void pending.catch(() => {
			if (this.#pending.get(key) === pending) this.#pending.delete(key);
		});
		return pending;
	}
}

async function createHighlighter(): Promise<HighlighterCore> {
	const [{ createHighlighterCore }, { createJavaScriptRegexEngine }] = await Promise.all([
		import("shiki/core"),
		import("shiki/engine/javascript"),
	]);
	return createHighlighterCore({
		themes: [PIXIE_SHIKI_THEME],
		langs: [],
		engine: createJavaScriptRegexEngine(),
	});
}

interface HighlighterRuntimeDependencies<Highlighter> {
	createHighlighter: () => Promise<Highlighter>;
	loadLanguage: (highlighter: Highlighter, language: CanonicalLanguage) => Promise<void>;
	codeToHtml: (highlighter: Highlighter, code: string, language: CanonicalLanguage) => string;
}

export function createHighlighterRuntime<Highlighter>(
	dependencies: HighlighterRuntimeDependencies<Highlighter>,
): (code: string, lang: string) => Promise<string | null> {
	const highlighterLoads = new RetryablePromiseCache<"core", Highlighter>();
	const languageLoads = new RetryablePromiseCache<CanonicalLanguage, void>();

	return async (code: string, lang: string): Promise<string | null> => {
		const key = lang.toLowerCase();
		const canonical = ALIAS[key] ?? key;
		if (!(canonical in LANGUAGE_LOADERS)) return null;
		try {
			const language = canonical as CanonicalLanguage;
			const highlighter = await highlighterLoads.get("core", dependencies.createHighlighter);
			await languageLoads.get(language, () => dependencies.loadLanguage(highlighter, language));
			return dependencies.codeToHtml(highlighter, code, language);
		} catch {
			return null;
		}
	};
}

const runHighlightCode = createHighlighterRuntime<HighlighterCore>({
	createHighlighter,
	loadLanguage: async (highlighter, language) => {
		const module = await LANGUAGE_LOADERS[language]();
		await highlighter.loadLanguage(module.default);
	},
	codeToHtml: (highlighter, code, language) =>
		highlighter.codeToHtml(code, {
			lang: language,
			theme: PIXIE_SHIKI_THEME_NAME,
		}),
});

export function highlightCode(code: string, lang: string): Promise<string | null> {
	return runHighlightCode(code, lang);
}
