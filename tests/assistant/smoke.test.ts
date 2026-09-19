import { describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { startBunHostFromVerifiedPi } from "../../src/assistant/host.ts";
import { verifyPiPackage } from "../../src/assistant/probe.ts";

type HostFrame = {
	readonly id?: number;
	readonly method?: string;
	readonly params?: Record<string, unknown>;
	readonly result?: Record<string, unknown> & {
		readonly sessionId?: string;
		readonly sessions?: Array<{
			readonly sessionId?: string;
			readonly cwd?: string;
		}>;
		readonly sources?: Array<{
			readonly name?: string;
			readonly path?: string;
			readonly revision?: string;
			readonly description?: string;
		}>;
		readonly source?: {
			readonly name?: string;
			readonly path?: string;
			readonly revision?: string;
			readonly description?: string;
		};
		readonly agents?: Array<{ readonly mention?: string; readonly name?: string }>;
		readonly entries?: Array<{ readonly providerId?: string }>;
		readonly values?: Array<{ readonly key?: string }>;
		readonly availableCommands?: Array<{ readonly name?: string }>;
		readonly servers?: unknown[];
		readonly commands?: unknown[];
	};
	readonly error?: { readonly code?: number; readonly message?: string };
};

// From assistant/tests this resolves to /repo/pixie/node_modules/@earendil-works/pi-coding-agent,
// the same "../node_modules/..." location viewed from assistant/. Absolute paths are required
// by verifyPiPackage; no workspace/global/CWD lookup fallback exists.
const REAL_PI_PACKAGE_PATH = resolve(
	import.meta.dir,
	"../../node_modules/@earendil-works/pi-coding-agent",
);
const SECRET = "s".repeat(32);

async function openSocket(endpoint: string, timeoutMs = 8000): Promise<WebSocket> {
	const ws = new WebSocket(endpoint, {
		headers: { Authorization: `Bearer ${SECRET}` },
	} as unknown as string[]);
	await new Promise<void>((resolve, reject) => {
		const timer = setTimeout(() => reject(new Error("websocket open timed out")), timeoutMs);
		ws.onopen = () => {
			clearTimeout(timer);
			resolve();
		};
		ws.onerror = () => {
			clearTimeout(timer);
			reject(new Error("websocket open failed"));
		};
	});
	return ws;
}

function createRpc(ws: WebSocket) {
	const stash: HostFrame[] = [];
	const waiters = new Map<
		number,
		{
			readonly resolve: (frame: HostFrame) => void;
			readonly timer: ReturnType<typeof setTimeout>;
		}
	>();
	ws.onmessage = (event) => {
		const frame = JSON.parse(event.data) as HostFrame;
		if (typeof frame.id === "number" && waiters.has(frame.id)) {
			const pending = waiters.get(frame.id);
			if (!pending) return;
			waiters.delete(frame.id);
			clearTimeout(pending.timer);
			pending.resolve(frame);
		} else {
			stash.push(frame);
		}
	};
	async function request(
		id: number,
		method: string,
		params: Record<string, unknown>,
		timeoutMs = 15000,
	): Promise<HostFrame> {
		const hit = stash.findIndex((frame) => frame.id === id);
		if (hit >= 0) return stash.splice(hit, 1)[0] as HostFrame;
		return new Promise<HostFrame>((resolve, reject) => {
			if (waiters.has(id)) {
				reject(new Error(`request id ${id} is already waiting`));
				return;
			}
			const timer = setTimeout(() => {
				if (waiters.delete(id)) reject(new Error(`timed out waiting for ${method} (id ${id})`));
			}, timeoutMs);
			waiters.set(id, { resolve, timer });
			try {
				ws.send(JSON.stringify({ id, method, params }));
			} catch (error) {
				waiters.delete(id);
				clearTimeout(timer);
				reject(error instanceof Error ? error : new Error("websocket send failed"));
			}
		});
	}
	return { stash, request };
}

async function closeSocket(ws: WebSocket, timeoutMs = 5000): Promise<void> {
	if (ws.readyState === WebSocket.CLOSED) return;
	await new Promise<void>((resolve, reject) => {
		const timer = setTimeout(() => reject(new Error("websocket close timed out")), timeoutMs);
		ws.onclose = () => {
			clearTimeout(timer);
			resolve();
		};
		ws.onerror = () => {
			clearTimeout(timer);
			reject(new Error("websocket close failed"));
		};
		ws.close();
	});
}

describe("real Pi Bun host smoke (no credentials, no network)", () => {
	// biome-ignore format: Keep the existing bounded test callback layout stable while extending its real-SDK coverage.
	test(
		"proves concurrent session lifecycle, source, and admin reads against the installed Pi 0.85.1 SDK",
		async () => {
			// Real package identity first: nothing below runs against a fake SDK.
			const verified = await verifyPiPackage(REAL_PI_PACKAGE_PATH);
			expect(verified.packageName).toBe("@earendil-works/pi-coding-agent");
			expect(verified.packageVersion).toBe("0.85.1");

			// TMPDIR=/home/data when run as instructed, so mkdtemp stays under /home/data.
			// Both directories are fresh temp roots; all host writes (sessions, agents,
			// mcp.json, auth/models, host-identity) must land inside agentDir.
			const parent = tmpdir();
			const agentDir = mkdtempSync(join(parent, "pixie-real-pi-agent-"));
			const cwd = mkdtempSync(join(parent, "pixie-real-pi-cwd-"));
			expect(agentDir.startsWith(parent)).toBe(true);
			expect(cwd.startsWith(parent)).toBe(true);

			// Port 0 proves the library path binds an ephemeral loopback port. The serve
			// CLI deliberately rejects port 0 (see serve port parity tests); only the
			// in-process Bun.serve host used here accepts it.
			const host = await startBunHostFromVerifiedPi({
				host: "127.0.0.1",
				port: 0,
				secret: SECRET,
				agentDir,
				verifiedPi: verified,
			});
			expect(host.endpoint.startsWith("ws://127.0.0.1:")).toBe(true);
			expect(host.endpoint.endsWith("/pi")).toBe(true);
			expect(host.endpoint).not.toContain(":0/pi");

			let ws: WebSocket | undefined;
			try {
				ws = await openSocket(host.endpoint);
				const { request } = createRpc(ws);

				const hello = await request(1, "runtime.hello", { protocolVersion: 1 });
				expect(hello.error).toBeUndefined();
				expect(hello.result?.version).toBe("0.85.1");
				expect(hello.result?.capabilities).toEqual({ sessions: 1, agents: 1, images: 1 });
				const runtimeId = hello.result?.runtimeId;
				expect(typeof runtimeId).toBe("string");

				// The real host must route two in-flight creation requests independently.
				// A multiple-waiter client is required here: serial requests cannot expose
				// cross-request completion or resident-map defects.
				const [firstCreated, secondCreated] = await Promise.all([
					request(2, "session.create", { cwd }),
					request(3, "session.create", { cwd }),
				]);
				for (const created of [firstCreated, secondCreated]) expect(created.error).toBeUndefined();
				const firstSessionId = firstCreated.result?.sessionId;
				const secondSessionId = secondCreated.result?.sessionId;
				expect(typeof firstSessionId).toBe("string");
				expect(typeof secondSessionId).toBe("string");
				if (
					typeof firstSessionId !== "string" ||
					firstSessionId === "" ||
					typeof secondSessionId !== "string" ||
					secondSessionId === ""
				)
					throw new Error("real concurrent session.create returned no sessionId");
				expect(secondSessionId).not.toBe(firstSessionId);
				const sessionIds = [firstSessionId, secondSessionId] as const;

				// Resident listing (no cwd) reflects the live host; native listing (cwd)
				// goes through the real SessionManager. A fresh prompt-less session has no
				// transcript file yet, so the native list can be empty while the resident
				// list contains both native IDs. Both shapes prove the real path.
				const residentList = await request(4, "session.list", {});
				expect(residentList.error).toBeUndefined();
				for (const sessionId of sessionIds)
					expect(
						residentList.result?.sessions?.some((entry) => entry.sessionId === sessionId),
					).toBe(true);
				const nativeList = await request(5, "session.list", { cwd });
				expect(nativeList.error).toBeUndefined();
				expect(Array.isArray(nativeList.result?.sessions)).toBe(true);

				// Each target must survive independent resident load and statistics calls;
				// returning the other resident's native ID is observable cross-session leakage.
				for (const [offset, sessionId] of sessionIds.entries()) {
					const loaded = await request(6 + offset * 2, "session.load", { sessionId, cwd });
					expect(loaded.error).toBeUndefined();
					expect(loaded.result?.sessionId).toBe(sessionId);
					const stats = await request(7 + offset * 2, "session.stats", { sessionId });
					expect(stats.error).toBeUndefined();
					expect(stats.result?.sessionId).toBe(sessionId);
					expect(typeof (stats.result as Record<string, unknown>)?.userMessages).toBe(
						"number",
					);
					expect(typeof (stats.result as Record<string, unknown>)?.assistantMessages).toBe(
						"number",
					);
					const sessionFile = (stats.result as { sessionFile?: unknown })?.sessionFile;
					if (typeof sessionFile === "string" && sessionFile !== "")
						expect(sessionFile.startsWith(agentDir)).toBe(true);
				}

				const switched = await request(10, "session.switch", { sessionId: firstSessionId });
				expect(switched.error).toBeUndefined();
				expect(switched.result?.sessionId).toBe(firstSessionId);

				const commands = await request(11, "session.commands", { sessionId: firstSessionId });
				expect(commands.error).toBeUndefined();
				expect(Array.isArray(commands.result?.commands)).toBe(true);
				expect(Array.isArray(commands.result?.availableCommands)).toBe(true);

				// Socket lifetime must not own the resident native sessions. Re-authenticate
				// on a new socket before loading both IDs, rather than relying on a stale
				// authenticated connection.
				await closeSocket(ws);
				ws = await openSocket(host.endpoint);
				const reconnected = createRpc(ws);
				const reconnectedHello = await reconnected.request(1, "runtime.hello", {
					protocolVersion: 1,
				});
				expect(reconnectedHello.error).toBeUndefined();
				expect(reconnectedHello.result?.runtimeId).toBe(runtimeId);
				for (const [offset, sessionId] of sessionIds.entries()) {
					const loaded = await reconnected.request(2 + offset, "session.load", { sessionId, cwd });
					expect(loaded.error).toBeUndefined();
					expect(loaded.result?.sessionId).toBe(sessionId);
				}

				// These are idle, credential-free residents. Cancel must remain a safe
				// control acknowledgement when repeated, and must not release either one.
				for (const [offset, sessionId] of sessionIds.entries()) {
					for (const retry of [0, 1]) {
						const cancelled = await reconnected.request(4 + offset * 2 + retry, "session.cancel", {
							sessionId,
						});
						expect(cancelled.error).toBeUndefined();
						expect(cancelled.result).toEqual({});
					}
				}
				const afterCancel = await reconnected.request(8, "session.list", {});
				expect(afterCancel.error).toBeUndefined();
				for (const sessionId of sessionIds)
					expect(
						afterCancel.result?.sessions?.some((entry) => entry.sessionId === sessionId),
					).toBe(true);

				// Filesystem sources stay inside the temp agentDir.
				const sourceCreated = await reconnected.request(9, "pi.sources.create", {
					name: "smoke-helper",
					description: "helps",
					content: "body",
				});
				expect(sourceCreated.error).toBeUndefined();
				const sourcePath = sourceCreated.result?.source?.path;
				const sourceRevision = sourceCreated.result?.source?.revision;
				expect(sourceCreated.result?.source?.name).toBe("smoke-helper");
				expect(typeof sourcePath).toBe("string");
				expect(typeof sourceRevision).toBe("string");
				if (typeof sourcePath !== "string" || typeof sourceRevision !== "string")
					throw new Error("real pi.sources.create returned no path/revision");
				expect(sourcePath.startsWith(agentDir)).toBe(true);
				expect(sourceRevision.startsWith("sha256:")).toBe(true);
				expect(existsSync(sourcePath)).toBe(true);

				const sourceListed = await reconnected.request(10, "pi.sources.list", {});
				expect(sourceListed.error).toBeUndefined();
				expect(
					sourceListed.result?.sources?.some((entry) => entry.name === "smoke-helper"),
				).toBe(true);

				const mentions = await reconnected.request(11, "pi.agent-mentions.list", {});
				expect(mentions.error).toBeUndefined();
				expect(
					mentions.result?.agents?.some((agent) => agent.mention === "@smoke-helper"),
				).toBe(true);

				const sourceUpdated = await reconnected.request(12, "pi.sources.update", {
					path: sourcePath,
					name: "smoke-helper",
					description: "helps more",
					content: "body2",
					expectedRevision: sourceRevision,
				});
				expect(sourceUpdated.error).toBeUndefined();
				const nextRevision = sourceUpdated.result?.source?.revision;
				expect(typeof nextRevision).toBe("string");
				expect(nextRevision).not.toBe(sourceRevision);

				const sourceDeleted = await reconnected.request(13, "pi.sources.delete", {
					path: sourcePath,
					expectedRevision: nextRevision,
				});
				expect(sourceDeleted.error).toBeUndefined();
				expect(sourceDeleted.result).toEqual({ ok: true });
				expect(existsSync(sourcePath)).toBe(false);

				// Read-only admin surface over the real SDK. No save/probe/refresh calls
				// here, so nothing touches the network or mutates global Pi config.
				const providers = await reconnected.request(14, "pi.providers.list", { providerIds: [] });
				expect(providers.error).toBeUndefined();
				expect(Array.isArray(providers.result?.entries)).toBe(true);
				expect((providers.result?.entries ?? []).length).toBeGreaterThan(0);

				const defaults = await reconnected.request(15, "pi.defaults.read", {});
				expect(defaults.error).toBeUndefined();
				expect(defaults.result).toEqual(
					expect.objectContaining({ providerId: null, modelId: null }),
				);

				const preferences = await reconnected.request(16, "pi.preferences.read", {});
				expect(preferences.error).toBeUndefined();
				expect(
					(preferences.result?.values ?? []).some(
						(entry) => entry.key === "piThinkingEffort",
					),
				).toBe(true);

				const extensions = await reconnected.request(17, "pi.extensions.list", {});
				expect(extensions.error).toBeUndefined();
				expect(extensions.result).toMatchObject({ version: 1 });

				const slash = await reconnected.request(18, "pi.slash-commands.list", {});
				expect(slash.error).toBeUndefined();
				expect(
					(slash.result?.availableCommands ?? []).some(
						(command) => command.name === "compact",
					),
				).toBe(true);

				const mcp = await reconnected.request(19, "pi.mcp.servers.read", {});
				expect(mcp.error).toBeUndefined();
				expect(Array.isArray(mcp.result?.servers)).toBe(true);

				// No credentials and no network: the real SDK rejects at preflight
				// instead of hanging. This must resolve quickly with a clear error.
				const promptStarted = Date.now();
				const prompt = await reconnected.request(
					20,
					"session.prompt",
					{
						sessionId: firstSessionId,
						content: [{ type: "text", text: "hello without credentials" }],
					},
					20000,
				);
				expect(Date.now() - promptStarted).toBeLessThan(20000);
				expect(prompt.result).toBeUndefined();
				expect(prompt.error?.code).toBe(-32000);
				expect(prompt.error?.message).toMatch(/prompt rejected/i);

				for (const [offset, sessionId] of sessionIds.entries()) {
					const released = await reconnected.request(21 + offset, "session.release", {
						sessionId,
						cwd,
					});
					expect(released.error).toBeUndefined();
					expect(released.result).toEqual({ ok: true });
				}

				// Reconnecting after every resident is released must not manufacture a
				// replacement session. A fresh native transcript is still absent without a
				// successful credential-gated prompt, so both IDs take the unknown-native path.
				await closeSocket(ws);
				ws = await openSocket(host.endpoint);
				const afterReleaseReconnect = createRpc(ws);
				const afterReleaseHello = await afterReleaseReconnect.request(1, "runtime.hello", {
					protocolVersion: 1,
				});
				expect(afterReleaseHello.error).toBeUndefined();
				expect(afterReleaseHello.result?.runtimeId).toBe(runtimeId);
				const afterRelease = await afterReleaseReconnect.request(2, "session.list", {});
				expect(afterRelease.error).toBeUndefined();
				expect(afterRelease.result?.sessions).toEqual([]);
				for (const [offset, sessionId] of sessionIds.entries()) {
					const unknownLoad = await afterReleaseReconnect.request(3 + offset, "session.load", {
						sessionId,
						cwd,
					});
					expect(unknownLoad.result).toBeUndefined();
					expect(unknownLoad.error?.message).toMatch(/unknown native session/i);
				}
			} finally {
				try {
					ws?.close();
				} catch {
					/* closing a test socket must not mask the test result */
				}
				await host.close();
				rmSync(agentDir, { recursive: true, force: true });
				rmSync(cwd, { recursive: true, force: true });
			}
		},
		55000,
	);

	// Credential-gated: settlement requires a model credential and a provider network
	// call. Skipped by design; a fake success would be worse than a skip.
	test.skip("credential-gated agent settlement requires model credentials and a provider network call", () => {});

	// Credential-gated: active-stream cancellation requires the same model credential
	// and provider network call. The idle session.cancel control response above is not
	// presented as stream-cancellation evidence.
	test.skip("credential-gated active-stream cancellation requires model credentials and a provider network call", () => {});

	// Credential-gated: reconnect settlement requires the same model credential and
	// provider network call; the reconnect evidence above covers only resident lifecycle.
	test.skip("credential-gated reconnect settlement requires model credentials and a provider network call", () => {});
});
