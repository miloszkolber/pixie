import { micromark } from "micromark";
import { gfm, gfmHtml } from "micromark-extension-gfm";

export type AlertVariant = "note" | "tip" | "important" | "warning" | "caution";

const ALERT_MARKER = /^\[!(note|tip|important|warning|caution)\]/i;

export function renderMarkdown(source: string): string {
	return micromark(source, { extensions: [gfm()], htmlExtensions: [gfmHtml()] });
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
