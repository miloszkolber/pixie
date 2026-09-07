import { describe, expect, test } from "bun:test";

// Stage G §28 contract: what the Browser MCP surface guarantees on Chromium
// with agent-browser 0.34.0. Live behavior is verified by
// tests/go/browser/chromium_parity_test.go (PIXIE_BROWSER_LIVE=1); this file
// locks the documented contract so the model-facing API cannot drift.

const guide = await Bun.file(new URL("../../internal/browser/guide.md", import.meta.url)).text();
const mcpDoc = await Bun.file(new URL("../../../docs/mcp.md", import.meta.url)).text();

describe("browser §28 command contract on Chromium", () => {
	const supported = [
		"`open`",
		"`snapshot`",
		"`screenshot`",
		"`close`",
		"`click`",
		"`fill`",
		"`type`",
		"`press`",
		"`scroll`",
		"`wait`",
		"`get`",
		"`is`",
		"`set`",
		"`read`",
		"`a11y`",
		"`vitals`",
	];
	for (const command of supported) {
		test(`guide documents ${command}`, () => {
			expect(guide).toContain(command);
		});
	}

	test("arbitrary eval stays rejected by policy", () => {
		expect(guide).toMatch(/arbitrary JavaScript/i);
		expect(guide).toMatch(/rejected/);
	});

	test("quotas match the service defaults", () => {
		for (const quota of ["16 browser sessions", "64 MiB", "256 MiB", "120-second"]) {
			expect(guide).toContain(quota);
		}
	});

	test("screenshots resolve to protected artifact URLs", () => {
		expect(guide).toMatch(/artifact/);
		expect(guide).toMatch(/close.*deletes its artifacts/i);
	});
});

describe("browser runs on Chromium", () => {
	test("chromium is the backend", () => {
		expect(mcpDoc).toMatch(/chromium/i);
	});

	test("session length guidance reflects the socket path limit", () => {
		expect(mcpDoc).toMatch(/28 characters/);
	});
});
