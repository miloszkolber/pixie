const EXTENSION_LANGUAGE: Record<string, string> = {
	ts: "typescript",
	tsx: "tsx",
	js: "javascript",
	jsx: "jsx",
	mjs: "javascript",
	cjs: "javascript",
	json: "json",
	jsonc: "json",
	sh: "bash",
	bash: "bash",
	zsh: "bash",
	py: "python",
	go: "go",
	css: "css",
	html: "html",
	htm: "html",
	md: "markdown",
	mdx: "markdown",
	yaml: "yaml",
	yml: "yaml",
};

export function languageForPath(path: string): string {
	const name = path.split("/").at(-1)?.toLowerCase() ?? "";
	if (["dockerfile", "containerfile"].includes(name)) return "bash";
	const extension = name.includes(".") ? (name.split(".").at(-1) ?? "") : "";
	return EXTENSION_LANGUAGE[extension] ?? "text";
}
