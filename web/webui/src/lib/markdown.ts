import { micromark } from "micromark";
import { gfm, gfmHtml } from "micromark-extension-gfm";

export type AlertVariant = "note" | "tip" | "important" | "warning" | "caution";

const ALERT_MARKER = /^\[!(note|tip|important|warning|caution)\]/i;

export function renderMarkdown(source: string): string {
	return sanitizeMarkdownHtml(
		micromark(source, { extensions: [gfm()], htmlExtensions: [gfmHtml()] }),
	);
}

/** Remote image schemes a transcript is never allowed to auto-load. */
function safeImageSource(url: string): boolean {
	const value = url.trim().toLowerCase();
	if (value.startsWith("//")) return false; // protocol-relative resolves to a remote host
	return (
		value.startsWith("data:image/") ||
		value.startsWith("blob:") ||
		value.startsWith("/") ||
		value.startsWith("./") ||
		value.startsWith("../")
	);
}

/**
 * AUX-01 transcript egress hardening. CSP is the enforcement boundary, but the
 * renderer must not emit a live remote `src` in the first place: a markdown
 * image pointing at an arbitrary host would otherwise auto-load and beacon
 * regardless of how the document was served. Remote sources are dropped and
 * marked so the UI can render a neutral placeholder.
 */
export function sanitizeMarkdownHtml(html: string): string {
	if (!html.includes("<img")) return html;
	return html.replace(/<img\b[^>]*>/gi, (tag) => rewriteImageTag(tag));
}

function findSrcAttribute(tag: string): { value: string } | null {
	for (let index = 0; index < tag.length; index += 1) {
		if (!isAttributeNameStart(tag, index, "src")) continue;
		let cursor = index + 3;
		while (cursor < tag.length && (tag[cursor] === " " || tag[cursor] === "\t")) cursor += 1;
		if (tag[cursor] !== "=") continue;
		cursor += 1;
		while (cursor < tag.length && (tag[cursor] === " " || tag[cursor] === "\t")) cursor += 1;
		const quote = tag[cursor];
		if (quote === '"' || quote === "'") {
			const close = tag.indexOf(quote, cursor + 1);
			const value = close < 0 ? tag.slice(cursor + 1) : tag.slice(cursor + 1, close);
			return { value };
		}
		let end = cursor;
		while (end < tag.length && tag[end] !== " " && tag[end] !== "\t" && tag[end] !== ">") end += 1;
		return { value: tag.slice(cursor, end) };
	}
	return null;
}

function rewriteImageTag(tag: string): string {
	const source = findSrcAttribute(tag);
	if (!source || safeImageSource(source.value)) return tag;
	return '<img data-pixie-remote-image="blocked" alt="" />';
}

/** Reports whether position index is the standalone start of a named attribute. */
function isAttributeNameStart(tag: string, index: number, name: string): boolean {
	if (!tag.startsWith(name, index)) return false;
	const before = index === 0 ? "" : tag[index - 1];
	if (before !== "" && before !== " " && before !== "\t") return false;
	const after = tag[index + name.length];
	return (
		after === "=" ||
		after === undefined ||
		after === " " ||
		after === "\t" ||
		after === "/" ||
		after === ">"
	);
}

export function slugify(text: string): string {
	return text
		.trim()
		.toLowerCase()
		.replace(/[^\w\s-]/g, "")
		.replace(/\s+/g, "-");
}

export function parseAlertMarker(text: string): { variant: AlertVariant; rest: string } | null {
	const match = ALERT_MARKER.exec(text);
	const marker = match?.[0];
	const variant = match?.[1];
	if (!marker || !variant) return null;
	return {
		variant: variant.toLowerCase() as AlertVariant,
		rest: text.slice(marker.length).replace(/^[^\S\n]*\n?/, ""),
	};
}

export function codeLanguage(className: string): string {
	return /(?:^|\s)language-([^\s]+)/.exec(className)?.[1] ?? "";
}
