import { expect, test } from "bun:test";
import { parseDevPort } from "../../../webui/scripts/dev";

test("local development uses a bounded configurable same-origin port", () => {
	expect(parseDevPort(undefined)).toBe(24269);
	expect(parseDevPort("3100")).toBe(3100);
	expect(() => parseDevPort("80")).toThrow("between 1024 and 65535");
	expect(() => parseDevPort("99999")).toThrow("between 1024 and 65535");
	expect(() => parseDevPort("3.1")).toThrow("must be an integer");
});

test("the direct Bun workflow serves UI, authentication, files, and WebSockets from one origin", async () => {
	const source = await Bun.file(new URL("../../../webui/scripts/dev.ts", import.meta.url)).text();
	expect(source).toContain(
		"the same-origin Go fixture serves static files, HTTP APIs, and WebSockets",
	);
	expect(source).toContain("PIXIE_UI_STATIC_DIR");
	expect(source).not.toMatch(/\bvite\b|@vitejs/);
});
