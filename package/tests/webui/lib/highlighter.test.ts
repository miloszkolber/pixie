import { expect, test } from "bun:test";
import { createHighlighterRuntime, highlightCode, languageForPath } from "@/lib/highlighter";

test("a preview retries highlighter initialization after a cached rejection", async () => {
	let initializationAttempts = 0;
	let grammarLoads = 0;
	const render = createHighlighterRuntime({
		createHighlighter: async () => {
			initializationAttempts += 1;
			if (initializationAttempts === 1) throw new Error("initialization failed");
			return { ready: true };
		},
		loadLanguage: async () => {
			grammarLoads += 1;
		},
		codeToHtml: (_highlighter, code, language) => `<pre data-lang="${language}">${code}</pre>`,
	});

	const [firstPreview, concurrentPreview] = await Promise.all([
		render("package main", "go"),
		render("package concurrent", "golang"),
	]);
	expect(firstPreview).toBeNull();
	expect(concurrentPreview).toBeNull();
	expect(await render("package retry", "go")).toBe('<pre data-lang="go">package retry</pre>');
	expect(await render("package cached", "go")).toBe('<pre data-lang="go">package cached</pre>');
	expect(initializationAttempts).toBe(2);
	expect(grammarLoads).toBe(1);
});

test("a preview retries a rejected grammar without reloading successful grammars", async () => {
	let initializationAttempts = 0;
	const grammarLoads = new Map<string, number>();
	const render = createHighlighterRuntime({
		createHighlighter: async () => {
			initializationAttempts += 1;
			return { ready: true };
		},
		loadLanguage: async (_highlighter, language) => {
			const attempts = (grammarLoads.get(language) ?? 0) + 1;
			grammarLoads.set(language, attempts);
			if (language === "go" && attempts === 1) throw new Error("grammar failed");
		},
		codeToHtml: (_highlighter, code, language) => `<pre data-lang="${language}">${code}</pre>`,
	});

	expect(await render("const ready = true", "typescript")).toContain('data-lang="typescript"');
	expect(await render("package main", "go")).toBeNull();
	expect(await render("package retry", "golang")).toContain('data-lang="go"');
	expect(await render("const cached = true", "typescript")).toContain('data-lang="typescript"');
	expect(initializationAttempts).toBe(1);
	expect(grammarLoads.get("go")).toBe(2);
	expect(grammarLoads.get("typescript")).toBe(1);
});

test("Go source and fenced aliases share the lazy grammar", async () => {
	expect(languageForPath("controller/main.go")).toBe("go");
	const [source, fence] = await Promise.all([
		highlightCode("package main\nfunc main() {}", "go"),
		highlightCode("package main\nfunc main() {}", "golang"),
	]);
	expect(source).toContain("package");
	expect(source).toContain("<span style=");
	expect(fence).toBe(source);
});
