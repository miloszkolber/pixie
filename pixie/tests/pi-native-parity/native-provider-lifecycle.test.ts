import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import llama from "../../../pi/pixie-assistant/src/extensions/llama.ts";
import { Providers } from "../../../pi/pixie-assistant/src/providers.ts";
import { Sessions } from "../../../pi/pixie-assistant/src/sessions.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const fn of cleanup.splice(0).reverse()) await fn();
});
async function root() {
	const dir = await mkdtemp("/tmp/opencode/pixie-native-provider-");
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const agentDir = join(dir, "agent"),
		cwd = join(dir, "project");
	await mkdir(agentDir);
	await mkdir(cwd);
	return { agentDir, cwd };
}
async function until(check: () => boolean) {
	for (let i = 0; i < 500 && !check(); i++) await Bun.sleep(10);
	expect(check()).toBe(true);
}

test("native OAuth callbacks persist only successful credentials, cancellation and errors stay out of inventory", async () => {
	const { agentDir, cwd } = await root();
	await writeFile(
		join(agentDir, "settings.json"),
		JSON.stringify({ extensions: [join(import.meta.dir, "lifecycle-provider.ts")] }),
	);
	const sessions = new Sessions(agentDir, [], () => {});
	cleanup.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	const original = entry.modelRuntime
		.getProviders()
		.find((provider) => provider.id === "lifecycle-fixture");
	if (!original) throw new Error("Native fixture provider missing");
	entry.modelRuntime.registerNativeProvider({
		...original,
		auth: {
			oauth: {
				name: "Fixture OAuth",
				login: async (interaction) => {
					interaction.notify({
						type: "auth_url",
						url: "https://oauth.invalid/fixture",
						instructions: "Fixture only",
					});
					const code = await interaction.prompt({ type: "text", message: "Code" });
					if (code !== "accepted") throw new Error("fixture failure with sensitive-code");
					return {
						type: "oauth",
						access: "fixture-access-not-a-secret",
						refresh: "fixture-refresh-not-a-secret",
						expires: Date.now() + 3600000,
					};
				},
				refresh: async (credential) => credential,
				toAuth: async (credential) => ({ apiKey: credential.access }),
			},
		},
	});
	type LoginEvent = { loginId: string; frame: { kind: string } };
	const events: LoginEvent[] = [];
	const providers = new Providers(
		entry.modelRuntime,
		entry.session.settingsManager,
		(event) => events.push(event as LoginEvent),
		agentDir,
	);
	cleanup.push(async () => providers.close());
	for (const mode of ["cancel", "failure", "success"] as const) {
		const id = `fixture-${mode}`;
		providers.start("lifecycle-fixture", "oauth", id);
		await providers.call("provider.loginBegin", { loginId: id });
		await until(() =>
			events.some((event) => event.loginId === id && event.frame.kind === "prompt"),
		);
		if (mode === "cancel") providers.cancel(id);
		else providers.reply(id, mode === "success" ? "accepted" : "rejected");
		await until(() =>
			events.some(
				(event) => event.loginId === id && ["success", "error"].includes(event.frame.kind),
			),
		);
		expect(events.filter((event) => event.loginId === id).at(-1)?.frame.kind).toBe(
			mode === "success" ? "success" : "error",
		);
		if (mode !== "success") {
			const stored = await readFile(join(agentDir, "auth.json"), "utf8").catch(() => "");
			expect(stored).not.toContain("fixture-access");
		}
	}
	const persisted = await readFile(join(agentDir, "auth.json"), "utf8");
	expect(persisted).toContain("fixture-access-not-a-secret");
	expect((await stat(join(agentDir, "auth.json"))).mode & 0o777).toBe(0o600);
	const inventory = await providers.inventory(["lifecycle-fixture"]);
	expect(inventory.entries).toMatchObject([{ configured: true, canOAuth: true }]);
	expect(JSON.stringify({ events, inventory })).not.toMatch(
		/fixture-access|fixture-refresh|sensitive-code/,
	);
	await entry.modelRuntime.logout("lifecycle-fixture");
	expect(await readFile(join(agentDir, "auth.json"), "utf8")).not.toContain("fixture-access");
});

test("existing optional SDK llama bootstrap logs in to a local fixture and restores native default after reopen", async () => {
	const { agentDir, cwd } = await root();
	const requests: { path: string; model?: string }[] = [];
	const server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		async fetch(request) {
			const path = new URL(request.url).pathname;
			if (path === "/models") {
				requests.push({ path });
				return Response.json({
					data: [{ id: "fixture-llama", status: { value: "loaded" }, meta: { n_ctx: 8192 } }],
				});
			}
			if (path === "/v1/chat/completions") {
				const body = (await request.json()) as { model: string };
				requests.push({ path, model: body.model });
				const chunks = [
					{
						id: "fixture",
						object: "chat.completion.chunk",
						created: 1,
						model: body.model,
						choices: [
							{
								index: 0,
								delta: { role: "assistant", content: "local llama fixture" },
								finish_reason: null,
							},
						],
					},
					{
						id: "fixture",
						object: "chat.completion.chunk",
						created: 1,
						model: body.model,
						choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
						usage: { prompt_tokens: 10, completion_tokens: 4, total_tokens: 14 },
					},
				];
				return new Response(
					`${chunks.map((chunk) => `data: ${JSON.stringify(chunk)}\n\n`).join("")}data: [DONE]\n\n`,
					{ headers: { "Content-Type": "text/event-stream" } },
				);
			}
			return new Response("Unexpected fixture route", { status: 404 });
		},
	});
	cleanup.push(async () => {
		await server.stop(true);
	});
	const sessions = new Sessions(agentDir, [llama], () => {});
	cleanup.push(() => sessions.close());
	const control = await sessions.control(cwd);
	cleanup.push(() => control.close());
	await control.modelRuntime.login("llama.cpp", "api_key", {
		prompt: async (prompt) => (prompt.type === "secret" ? "" : `http://127.0.0.1:${server.port}`),
		notify: () => {},
		signal: AbortSignal.timeout(5000),
	});
	const refreshed = await control.modelRuntime.refresh({
		providers: ["llama.cpp"],
		allowNetwork: true,
		signal: AbortSignal.timeout(5000),
	});
	expect(refreshed.errors.size).toBe(0);
	expect(control.modelRuntime.getModel("llama.cpp", "fixture-llama")?.contextWindow).toBe(8192);
	control.session.settingsManager.setDefaultProvider("llama.cpp");
	control.session.settingsManager.setDefaultModel("fixture-llama");
	await control.session.settingsManager.flush();
	let entry = await sessions.create(cwd);
	const id = entry.session.sessionId;
	for (let i = 0; i < 2; i++) {
		expect(entry.session.model?.provider).toBe("llama.cpp");
		expect(entry.session.model?.id).toBe("fixture-llama");
		expect(
			await sessions.call("session.prompt", {
				sessionId: id,
				content: [{ type: "text", text: "hello local model" }],
			}),
		).toEqual({ stopReason: "stop" });
		const last = entry.session.messages.filter((message) => message.role === "assistant").at(-1);
		expect(last?.content).toEqual([{ type: "text", text: "local llama fixture" }]);
		await sessions.release(id);
		entry = await sessions.get(id);
	}
	expect(requests.filter((request) => request.path === "/v1/chat/completions")).toEqual([
		{ path: "/v1/chat/completions", model: "fixture-llama" },
		{ path: "/v1/chat/completions", model: "fixture-llama" },
	]);
}, 15000);
