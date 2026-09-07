import { randomUUID } from "node:crypto";
import { join } from "node:path";
import type { AuthEvent, AuthPrompt } from "@earendil-works/pi-ai";
import type { ModelRuntime, SettingsManager } from "@earendil-works/pi-coding-agent";
import { JsonStore, object, type RecordValue, required, text } from "./storage.ts";

type Login = {
	id: string;
	providerId: string;
	frame: RecordValue;
	abort: AbortController;
	resolve?: (value: string) => void;
	reject?: (error: Error) => void;
	begin?: () => void;
	timer: ReturnType<typeof setTimeout>;
};
export class Providers {
	private logins = new Map<string, Login>();
	constructor(
		readonly runtime: ModelRuntime,
		readonly settings: SettingsManager,
		readonly notify: (frame: RecordValue) => void,
		readonly agentDir: string,
	) {}
	async inventory(ids: string[] = []): Promise<RecordValue> {
		const available = await this.runtime.getAvailable(undefined, {
			signal: AbortSignal.timeout(10000),
		});
		const ready = new Set(available.map((m) => m.provider));
		const entries = await Promise.all(
			this.runtime
				.getProviders()
				.filter((p) => !ids.length || ids.includes(p.id))
				.map(async (p) => {
					const auth = await this.runtime.checkAuth(p.id, { signal: AbortSignal.timeout(5000) });
					return {
						providerId: p.id,
						providerName: p.name ?? p.id,
						configured: !!auth,
						readinessCheck: true,
						available: ready.has(p.id),
						canOAuth: !!p.auth.oauth,
						canApiKey: !!p.auth.apiKey?.login,
						configKeys: [
							...(p.auth.apiKey?.login
								? [{ name: "api_key", secret: true, required: true, primary: true }]
								: []),
							...(p.auth.oauth ? [{ name: "oauth", oauthFlow: true }] : []),
						],
						models: this.runtime.getModels(p.id).map((m) => ({
							id: m.id,
							name: m.name,
							contextLimit: m.contextWindow,
							maxOutputTokens: m.maxTokens,
							reasoning: m.reasoning,
							modalities: m.input,
						})),
					};
				}),
		);
		return { entries };
	}
	modelInfo(provider: string, id: string): RecordValue {
		const m = this.runtime.getModel(provider, id);
		if (!m) throw new Error("Unknown model");
		return {
			modelInfo: {
				provider,
				model: id,
				contextLimit: m.contextWindow,
				maxOutputTokens: m.maxTokens,
				reasoning: m.reasoning,
				currency: "USD",
				inputTokenCost: m.cost.input,
				outputTokenCost: m.cost.output,
				cacheReadTokenCost: m.cost.cacheRead,
				cacheWriteTokenCost: m.cost.cacheWrite,
			},
		};
	}
	private publish(login: Login, frame: RecordValue): void {
		login.frame = frame;
		this.notify({ loginId: login.id, providerId: login.providerId, frame });
	}
	start(providerId: string, type: string, id: string = randomUUID()): RecordValue {
		if (type !== "api_key" && type !== "oauth") throw new Error("Invalid authentication method");
		if ([...this.logins.values()].some((l) => l.providerId === providerId))
			throw new Error("Authentication already in progress");
		if (this.logins.has(id)) throw new Error("Duplicate login ID");
		const abort = new AbortController();
		const frame = { kind: "progress", message: "Starting Pi authentication…" };
		const login: Login = {
			id,
			providerId,
			abort,
			frame,
			timer: setTimeout(() => this.cancel(id), 600000),
		};
		this.logins.set(id, login);
		// Defer callbacks until the caller can associate the returned ID with its UI.
		login.begin = () => {
			void this.runtime
				.login(providerId, type, {
					signal: abort.signal,
					prompt: async (p: AuthPrompt) => {
						const frame =
							p.type === "select"
								? { kind: "select", message: p.message, options: p.options }
								: {
										kind: "prompt",
										message: p.message,
										placeholder: p.placeholder,
										secret: p.type === "secret",
										allowEmpty: false,
									};
						return new Promise<string>((resolve, reject) => {
							const signal = p.signal ? AbortSignal.any([abort.signal, p.signal]) : abort.signal;
							const cancel = () => {
								login.resolve = undefined;
								login.reject = undefined;
								reject(new Error("Authentication cancelled"));
							};
							if (signal.aborted) {
								cancel();
								return;
							}
							signal.addEventListener("abort", cancel, { once: true });
							login.resolve = (v) => {
								signal.removeEventListener("abort", cancel);
								login.resolve = undefined;
								login.reject = undefined;
								resolve(v);
							};
							login.reject = reject;
							this.publish(login, frame);
						});
					},
					notify: (event: AuthEvent) => {
						if (event.type === "auth_url")
							this.publish(login, {
								kind: "authUrl",
								url: event.url,
								instructions: event.instructions,
							});
						else if (event.type === "device_code")
							this.publish(login, {
								kind: "deviceCode",
								userCode: event.userCode,
								verificationUri: event.verificationUri,
								expiresInSeconds: event.expiresInSeconds,
							});
						else this.publish(login, { kind: "progress", message: event.message });
					},
				})
				.then(
					() => this.publish(login, { kind: "success" }),
					() =>
						this.publish(login, {
							kind: "error",
							message: "Pi authentication failed or was cancelled.",
						}),
				)
				.finally(() => {
					clearTimeout(login.timer);
					this.logins.delete(id);
				});
		};
		return { loginId: id, frame };
	}
	reply(id: string, value: string): void {
		const login = this.logins.get(id);
		if (!login?.resolve) throw new Error("No pending authentication question");
		login.resolve(value);
	}
	cancel(id: string): void {
		const login = this.logins.get(id);
		if (!login) return;
		login.abort.abort();
		clearTimeout(login.timer);
		this.logins.delete(id);
	}
	async call(method: string, p: RecordValue): Promise<unknown> {
		switch (method) {
			case "pi.providers.list":
				return this.inventory(Array.isArray(p.providerIds) ? p.providerIds.map(text) : []);
			case "pi.providers.canonical-model-info":
				return this.modelInfo(required(p.provider, "provider"), required(p.model, "model"));
			case "pi.providers.inventory.refresh": {
				const result = await this.runtime.refresh({
					allowNetwork: true,
					signal: AbortSignal.timeout(25000),
				});
				return { started: [], skipped: [], errors: [...result.errors.keys()] };
			}
			case "pi.providers.readiness.check": {
				const id = required(p.providerId, "provider");
				const configured = !!(await this.runtime.checkAuth(id, {
					signal: AbortSignal.timeout(5000),
				}));
				return { providerId: id, ready: configured, hasIssue: !configured, verified: false };
			}
			case "provider.loginStart":
				return this.start(
					required(p.providerId, "provider"),
					text(p.type) || "api_key",
					required(p.loginId, "login"),
				);
			case "provider.loginBegin": {
				const login = this.logins.get(required(p.loginId, "login"));
				if (!login?.begin) throw new Error("Login cannot be started");
				const begin = login.begin;
				login.begin = undefined;
				begin();
				return { ok: true };
			}
			case "provider.loginReply":
				this.reply(required(p.loginId, "login"), text(p.value));
				return { ok: true };
			case "provider.loginCancel":
				this.cancel(required(p.loginId, "login"));
				return { ok: true };
			case "pi.providers.config.read":
				return {
					fields: [
						{
							isSet: !!(await this.runtime.checkAuth(required(p.providerId, "provider"), {
								signal: AbortSignal.timeout(5000),
							})),
						},
					],
				};
			case "pi.providers.config.delete":
				await this.runtime.logout(required(p.providerId, "provider"));
				return { ok: true };
			case "pi.defaults.read":
				return {
					providerId: this.settings.getDefaultProvider() || null,
					modelId: this.settings.getDefaultModel() || null,
				};
			case "pi.defaults.save":
				this.settings.setDefaultProvider(required(p.providerId, "provider"));
				this.settings.setDefaultModel(p.modelId ? required(p.modelId, "model") : "");
				await this.settings.flush();
				return this.call("pi.defaults.read", {});
			case "pi.defaults.clear": {
				await this.settings.flush();
				await new JsonStore<RecordValue>(join(this.agentDir, "settings.json"), () => ({})).update(
					(s) => {
						delete s.defaultProvider;
						delete s.defaultModel;
					},
				);
				await this.settings.reload();
				return this.call("pi.defaults.read", {});
			}
			case "pi.preferences.read": {
				await this.settings.reload();
				const global = this.settings.getGlobalSettings();
				return {
					values: [
						{ key: "piThinkingEffort", value: global.defaultThinkingLevel ?? null },
						{ key: "compactionReserveTokens", value: global.compaction?.reserveTokens ?? null },
					],
				};
			}
			case "pi.preferences.save":
			case "pi.preferences.reset": {
				const store = new JsonStore<RecordValue>(join(this.agentDir, "settings.json"), () => ({}));
				await this.settings.flush();
				await store.update((s) => {
					const values = method.endsWith(".reset")
						? (Array.isArray(p.keys) ? p.keys : []).map((key) => ({ key, value: null }))
						: Array.isArray(p.values)
							? p.values
							: [];
					for (const raw of values) {
						const v = object(raw);
						if (v.key === "piThinkingEffort") {
							if (v.value === null) delete s.defaultThinkingLevel;
							else {
								if (!["off", "minimal", "low", "medium", "high", "xhigh"].includes(text(v.value)))
									throw new Error("Unsupported thinking effort");
								s.defaultThinkingLevel = v.value;
							}
						} else if (v.key === "compactionReserveTokens") {
							const compaction = { ...object(s.compaction) };
							if (v.value === null) delete compaction.reserveTokens;
							else {
								if (
									typeof v.value !== "number" ||
									!Number.isSafeInteger(v.value) ||
									v.value < 1024 ||
									v.value > 1000000
								)
									throw new Error("Invalid compaction reserve");
								compaction.reserveTokens = v.value;
							}
							s.compaction = compaction;
						} else throw new Error("Unknown preference");
					}
				});
				await this.settings.reload();
				return this.call("pi.preferences.read", {});
			}
			default:
				throw new Error(`Unsupported provider operation: ${method}`);
		}
	}
	close(): void {
		for (const id of this.logins.keys()) this.cancel(id);
	}
}
