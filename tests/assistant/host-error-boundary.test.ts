import { describe, expect, test } from "bun:test";
import { createHostLogger } from "../../src/assistant/log.ts";
import {
	credential,
	FakeSession,
	helloV2,
	operatorPath,
	operatorUrl,
	rawHost,
	registerHostCleanup,
	secret,
	waitForId,
} from "./harness.ts";

registerHostCleanup();

// AUX-32: the session SDK failure embeds a credential, an absolute path, an
// endpoint URL and the host bearer. None may cross the browser boundary.
class FailingPromptSession extends FakeSession {
	override async prompt(
		_text: string,
		_options: { preflightResult: (accepted: boolean) => void },
	): Promise<void> {
		this.calls.push("prompt");
		throw new Error(`native prompt failure ${credential} ${operatorPath} ${operatorUrl} ${secret}`);
	}
}

describe("native error browser boundary", () => {
	test("redacts native paths, URLs and credentials from the reply and log", async () => {
		const session = new FailingPromptSession("boundary");
		const logger = createHostLogger({ secrets: [secret] });
		const raw = rawHost(session, { protocol: "auto", logger });
		await helloV2(raw);

		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const created = await waitForId(raw.socket, 2);
		expect(created.error).toBeUndefined();
		const sessionId = (created.result as { sessionId?: string } | undefined)?.sessionId;
		expect(sessionId).toBeTruthy();

		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId, content: [{ type: "text", text: "hi" }] },
		});
		const failed = await waitForId(raw.socket, 3);
		expect(failed.result).toBeUndefined();
		expect(failed.error).toBeDefined();

		const forbidden = [credential, operatorPath, operatorUrl, secret];
		const serialized = JSON.stringify(failed);
		for (const value of forbidden) expect(serialized).not.toContain(value);
		// The message is bounded and the failing class is still recognizable.
		expect(failed.error?.message?.length ?? 0).toBeLessThanOrEqual(512);
		expect(failed.error?.message).toContain("native prompt failure");
		expect(failed.error?.message).toContain("[url]");
		expect(failed.error?.message).toContain("[path]");
		expect(failed.error?.message).toContain("[redacted]");

		// Only the redacted cause is retained in the bounded log.
		const retained = JSON.stringify(logger.entries());
		for (const value of forbidden) expect(retained).not.toContain(value);
		expect(retained).toContain("native prompt failure");
		expect(retained).toContain("host.request.failed");
	});
});
