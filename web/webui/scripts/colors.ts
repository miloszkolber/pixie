import { readFileSync } from "node:fs";
import { join } from "node:path";

export const STYLES_DIR = join(import.meta.dir, "..", "src", "styles");
export const SOURCE_PATH = join(STYLES_DIR, "colors.json");
export const GENERATED_CSS_PATH = join(STYLES_DIR, "generated", "colors.css");

export const paletteVar = (key: string) =>
	`--${key.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase()}`;

export function themeColorKeys(): string[] {
	return [
		"accent",
		"accentHover",
		"accentSolid",
		"onAccent",
		"onDanger",
		"bubbleAccent",
		"background",
		"header",
		"content",
		"sidebar",
		"input",
		"elevated",
		"hover",
		"border",
		"borderStrong",
		"text",
		"muted",
		"hint",
		"selection",
		"selectionForeground",
		"editorSelection",
		"editorSelectionForeground",
		"info",
		"success",
		"danger",
		"warning",
	];
}

export interface Role {
	readonly from: string;
	readonly alpha?: string;
	readonly fallback?: string;
	readonly note?: string;
}

export interface Effect {
	readonly dark: string;
	readonly light: string;
	readonly note?: string;
}

export interface Colors {
	readonly metadata: { readonly version: string; readonly note?: string };
	readonly scale: Readonly<Record<string, number>>;
	readonly roles: Readonly<Record<string, Role>>;
	readonly effects: Readonly<Record<string, Effect>>;
}

export function loadColors(path = SOURCE_PATH): Colors {
	return JSON.parse(readFileSync(path, "utf8")) as Colors;
}

export const roleVar = (name: string) => `--${name}`;

export function derive(colors: Colors, role: Role): string {
	const source = paletteVar(role.from);
	if (!role.alpha) return `var(${source}${role.fallback ? `, ${role.fallback}` : ""})`;
	return `color-mix(in srgb, var(${source}) ${colors.scale[role.alpha]}%, transparent)`;
}

function aliasesPaletteVar(name: string, role: Role): boolean {
	return paletteVar(role.from) === roleVar(name) && !role.alpha && !role.fallback;
}

export function validate(colors: Colors): string[] {
	const issues: string[] = [];
	if (!/^\d+\.\d+\.\d+$/.test(colors.metadata?.version ?? "")) {
		issues.push("metadata.version must be semver");
	}
	for (const [step, pct] of Object.entries(colors.scale)) {
		if (!Number.isInteger(pct) || pct <= 0 || pct >= 100) {
			issues.push(`scale.${step} must be an integer percentage in (0, 100)`);
		}
	}
	const keys = themeColorKeys();
	for (const [name, role] of Object.entries(colors.roles)) {
		if (!keys.includes(role.from)) {
			issues.push(`roles.${name}.from is not a theme manifest key: ${role.from}`);
		}
		if (role.alpha !== undefined && colors.scale[role.alpha] === undefined) {
			issues.push(
				`roles.${name}.alpha must be a scale step (${Object.keys(colors.scale).join(", ")}), got ${role.alpha}`,
			);
		}
		if (role.alpha !== undefined && role.fallback !== undefined) {
			issues.push(`roles.${name} cannot combine alpha with fallback`);
		}
		for (const key of Object.keys(role)) {
			if (!["from", "alpha", "fallback", "note"].includes(key)) {
				issues.push(`roles.${name}: unexpected property ${key}`);
			}
		}
		if (!/^[a-z][a-z0-9-]*$/.test(name)) issues.push(`roles.${name} must be a kebab-case slug`);
	}
	for (const [name, effect] of Object.entries(colors.effects)) {
		for (const appearance of ["dark", "light"] as const) {
			if (typeof effect[appearance] !== "string" || effect[appearance].length === 0) {
				issues.push(`effects.${name}.${appearance} must be a non-empty string`);
			}
		}
		for (const key of Object.keys(effect)) {
			if (!["dark", "light", "note"].includes(key)) {
				issues.push(`effects.${name}: unexpected property ${key}`);
			}
		}
	}
	const used = new Set(Object.values(colors.roles).map((r) => r.from));
	for (const key of keys) {
		if (!used.has(key)) issues.push(`no role reads the theme manifest key "${key}"`);
	}
	return issues;
}

const HEADER = (version: string, kind: string) => `/*
 * GENERATED — do not edit. Source: \`src/styles/colors.json\` (v${version}).
 * Regenerate with \`bun run colors:generate\`; \`colors:check\` fails when this file is stale.
 *
 * ${kind}
 */
`;

export function renderCss(colors: Colors): string {
	const roles = Object.entries(colors.roles);

	const rootLines = roles
		.filter(([name, role]) => !aliasesPaletteVar(name, role))
		.map(([name, role]) => {
			const note = role.note ? ` /* ${role.note} */` : "";
			return `\t${roleVar(name)}: ${derive(colors, role)};${note}`;
		});

	const effectBlocks = [
		[
			":root {",
			...Object.entries(colors.effects).map(([name, effect]) => `\t--${name}: ${effect.dark};`),
			"}",
		].join("\n"),
		[
			"@media (prefers-color-scheme: light) {",
			"\t:root {",
			...Object.entries(colors.effects).map(([name, effect]) => `\t\t--${name}: ${effect.light};`),
			"\t}",
			"}",
		].join("\n"),
	];

	return [
		HEADER(
			colors.metadata.version,
			"Semantic roles and appearance effects. The palette they read is written to the document\n * root by the fixed system palette before Svelte mounts.",
		),
		":root {",
		...rootLines,
		"}",
		"",
		"/* Appearance-level effects follow the system color scheme with no JavaScript theme runtime. */",
		...effectBlocks,
		"",
	].join("\n");
}
