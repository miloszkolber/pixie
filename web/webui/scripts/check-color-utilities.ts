#!/usr/bin/env bun
import { readFileSync } from "node:fs";
import { loadColors } from "./colors";

const colors = loadColors();
const published = new Set([
	...Object.entries(colors.roles).flatMap(([name, role]) => (role.publish ? [name] : [])),
	...Object.entries(colors.effects).flatMap(([name, effect]) => (effect.publish ? [name] : [])),
]);
const semanticPrefix =
	/^(?:bg|text|border)-(container|control|feedback|primary|bubble|text)-([A-Za-z0-9-]+)$/;
const classToken = /(?:^|[\s"'`])((?:bg|text|border)-[A-Za-z0-9-]+)/g;
const issues: string[] = [];

for await (const path of new Bun.Glob("src/**/*.{svelte,ts,tsx}").scan({
	cwd: `${import.meta.dir}/..`,
})) {
	const source = readFileSync(new URL(`../${path}`, import.meta.url), "utf8");
	for (const match of source.matchAll(classToken)) {
		const token = match[1];
		if (!token) continue;
		const semantic = semanticPrefix.exec(token);
		if (!semantic) continue;
		const role = `${semantic[1]}-${semantic[2]}`;
		if (!published.has(role)) issues.push(`${path}: unknown semantic colour utility ${token}`);
	}
}

if (issues.length > 0) {
	console.error(issues.join("\n"));
	process.exit(1);
}
