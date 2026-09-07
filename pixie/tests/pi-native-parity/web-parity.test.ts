import { afterEach, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import rpivWeb from "@juicesharp/rpiv-web-tools";
import { cleanups, findTool, fixture } from "./helpers.ts";

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

async function localServer(body: string, contentType = "text/plain") {
	const server = Bun.serve({
		port: 0,
		hostname: "127.0.0.1",
		fetch: () => new Response(body, { headers: { "content-type": contentType } }),
	});
	cleanups.push(async () => server.stop(true));
	return `http://127.0.0.1:${server.port}/fixture`;
}

test("upstream web profiles register web_search, web_fetch, and the config command", async () => {
	const { dir, sessions } = await fixture([rpivWeb]);
	const entry = await sessions.create(dir);
	const names = entry.session.getActiveToolNames();
	expect(names).toContain("web_search");
	expect(names).toContain("web_fetch");
	expect(entry.session.extensionRunner.getRegisteredCommands().map((c: any) => c.name)).toContain(
		"web-tools",
	);
});

test("upstream web_fetch keeps its SSRF guard for private IPs and protocols", async () => {
	const { dir, sessions } = await fixture([rpivWeb]);
	const tool = findTool(await sessions.create(dir), "web_fetch");
	const signal = new AbortController().signal;
	for (const url of [
		"http://localhost:8080/fixture",
		"http://10.0.0.5/fixture",
		"http://192.168.1.10/fixture",
		"http://169.254.169.254/latest/meta-data/",
	]) {
		await expect(tool.execute(`parity-ssrf-${url}`, { url }, signal)).rejects.toThrow(
			/private\/loopback/i,
		);
	}
	await expect(
		tool.execute("parity-ssrf-file", { url: "file:///etc/hostname" }, signal),
	).rejects.toThrow(/protocol|URL/i);
});

test("upstream web_fetch reads live HTML as text with title metadata", async () => {
	const { dir, sessions } = await fixture([rpivWeb]);
	const result = await findTool(await sessions.create(dir), "web_fetch").execute(
		"parity-live-html",
		{ url: "https://example.com" },
		new AbortController().signal,
	);
	expect(result.content[0].text).toContain("Example Domain");
	expect(result.details).toMatchObject({ url: "https://example.com", title: "Example Domain" });
	expect(result.details.truncation?.truncated ?? false).toBe(false);
	expect(result.details.fullOutputPath).toBeUndefined();
}, 30000);

test("upstream web_fetch reads live JSON as raw text", async () => {
	const { dir, sessions } = await fixture([rpivWeb]);
	const result = await findTool(await sessions.create(dir), "web_fetch").execute(
		"parity-live-json",
		{ url: "https://httpbin.org/json" },
		new AbortController().signal,
	);
	expect(result.content[0].text).toContain("slideshow");
	expect(result.details.contentType).toMatch(/json/i);
}, 30000);

test("upstream web_fetch spills large live responses to a temp file", async () => {
	const { dir, sessions } = await fixture([rpivWeb]);
	const result = await findTool(await sessions.create(dir), "web_fetch").execute(
		"parity-live-spill",
		{ url: "https://jsonplaceholder.typicode.com/photos" },
		new AbortController().signal,
	);
	expect(result.details.truncation?.truncated).toBe(true);
	const spilled = String(result.details.fullOutputPath);
	expect(spilled).toContain("rpiv-fetch-");
	const full = await readFile(spilled, "utf8");
	expect(full.length).toBeGreaterThan(result.content[0].text.length);
}, 60000);

test("upstream web_fetch honors cancellation", async () => {
	const { dir, sessions } = await fixture([rpivWeb]);
	const controller = new AbortController();
	controller.abort();
	await expect(
		findTool(await sessions.create(dir), "web_fetch").execute(
			"parity-cancel",
			{ url: "https://example.com" },
			controller.signal,
		),
	).rejects.toThrow();
});

test("upstream web_search defaults to SearXNG and reports its backend", async () => {
	const responses: string[] = [];
	const searxng = Bun.serve({
		port: 0,
		hostname: "127.0.0.1",
		fetch: (request) => {
			responses.push(new URL(request.url).searchParams.get("q") ?? "");
			return Response.json({
				results: [{ title: "Example", url: "https://example.com", content: "Fixture snippet" }],
			});
		},
	});
	cleanups.push(async () => searxng.stop(true));
	process.env.WEB_SEARCH_PROVIDER = "searxng";
	process.env.SEARXNG_URL = `http://127.0.0.1:${searxng.port}`;
	try {
		const { dir, sessions } = await fixture([rpivWeb]);
		const result = await findTool(await sessions.create(dir), "web_search").execute(
			"parity-search",
			{ query: "fixture query", max_results: 3 },
			new AbortController().signal,
		);
		expect(responses).toEqual(["fixture query"]);
		expect(result.content[0].text).toContain("https://example.com");
		expect(result.details).toMatchObject({ backend: "searxng", resultCount: 1 });
		expect(result.details.results).toMatchObject([{ title: "Example" }]);
	} finally {
		delete process.env.WEB_SEARCH_PROVIDER;
		delete process.env.SEARXNG_URL;
	}
});

test("upstream web_search rejects unknown provider overrides", async () => {
	const { dir, sessions } = await fixture([rpivWeb]);
	await expect(
		findTool(await sessions.create(dir), "web_search").execute(
			"parity-search-unknown",
			{ query: "fixture", provider: "nope" },
			new AbortController().signal,
		),
	).rejects.toThrow(/Unknown web_search provider/i);
});
