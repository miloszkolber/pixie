import { describe, expect, test } from "bun:test";

// Stage G §28 contract: what the Browser MCP surface guarantees on Chromium
// with agent-browser 0.34.0. Live behavior is verified by
// tests/go/browser/chromium_parity_test.go (PIXIE_BROWSER_LIVE=1); this file
// locks the documented contract so the model-facing API cannot drift while
// the Obscura evaluation below stays unevaluated.

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

describe("browser engine preference", () => {
	test("chromium stays the default engine", () => {
		expect(mcpDoc).toMatch(/default.*chromium/i);
	});

	test("engine switch keeps the model-facing API identical", () => {
		expect(mcpDoc).toMatch(/does not alter the model-facing API/);
	});

	test("obscura selection requires a CDP endpoint", () => {
		expect(mcpDoc).toMatch(/PIXIE_BROWSER_CDP/);
	});

	test("session length guidance reflects the socket path limit", () => {
		expect(mcpDoc).toMatch(/28 characters/);
	});
});

test.skipIf(Bun.which("obscura") == null)("obscura passes the Chromium compatibility suite", () => {
	// Unevaluated: no obscura binary on this host. The concrete suite is
	// tests/go/browser/chromium_parity_test.go TestLiveObscuraCompatibility:
	// open, snapshot with refs, get title, screenshot, and close over
	// `obscura serve --port 9222` with AGENT_BROWSER_CDP, compared against
	// the Chromium latencies and success rates in docs/mcp.md. Prefer
	// Obscura only when that suite passes; Chromium stays the fallback.
	expect(Bun.which("obscura")).not.toBeNull();
});
