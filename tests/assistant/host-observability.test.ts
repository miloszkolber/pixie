import { describe, expect, test } from "bun:test";
import { createHostLogger } from "../../src/assistant/log.ts";
import { FakeSession, rawHost, registerHostCleanup, secret, waitForId } from "./harness.ts";

registerHostCleanup();

describe("Bun host lifecycle logging", () => {
	test("emits bounded secret-safe lifecycle and error entries without paths", async () => {
		const capacity = 6;
		const logged: string[] = [];
		const logger = createHostLogger({
			capacity,
			secrets: [secret],
			sink: (entry) => logged.push(entry.event),
		});
		const raw = rawHost(new FakeSession("logging"), { logger });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: raw.agentDir } });
		const created = (await waitForId(raw.socket, 2)).result as unknown as { sessionId: string };
		await raw.send({
			id: 3,
			method: "session.release",
			params: { sessionId: created.sessionId, cwd: raw.agentDir },
		});
		await waitForId(raw.socket, 3);
		// The default SDK has no SettingsManager, so this fails and is logged.
		await raw.send({ id: 4, method: "pi.preferences.read", params: {} });
		await waitForId(raw.socket, 4);
		await raw.host.close();
		for (const event of [
			"host.starting",
			"host.ready",
			"session.created",
			"session.released",
			"host.draining",
			"host.closed",
			"host.request.failed",
		]) {
			expect(logged).toContain(event);
		}
		const retained = logger.entries();
		expect(retained.length).toBeLessThanOrEqual(capacity);
		const serialized = JSON.stringify(retained);
		expect(serialized).not.toContain(secret);
		expect(serialized).not.toContain(raw.agentDir);
	});
});
