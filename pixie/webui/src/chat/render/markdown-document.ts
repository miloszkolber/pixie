import { micromark } from "micromark";
import { gfm, gfmHtml } from "micromark-extension-gfm";

export function renderChatMarkdown(source: string): string {
	return micromark(source, { extensions: [gfm()], htmlExtensions: [gfmHtml()] });
}

export function codeLanguage(className: string): string {
	return /(?:^|\s)language-([^\s]+)/.exec(className)?.[1] ?? "";
}
