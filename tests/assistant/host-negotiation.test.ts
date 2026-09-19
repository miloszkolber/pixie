import { describe, expect, test } from "bun:test";
import { FakeSession, rawFrames, rawHost, registerHostCleanup, waitForId } from "./harness.ts";

registerHostCleanup();

/**
 * AUX-33: a peer cannot select v2 by claiming it while omitting the
 * supported-version advertisement the host needs to validate that selection.
 * The empty-advertisement exception survives only for the explicit legacy v1
 * path, where no negotiation fields exist.
 */
describe("Bun host strict v2 hello negotiation (AUX-33)", () => {
	async function helloSent(
		protocol: "auto" | "v2",
		params: Record<string, unknown>,
	): Promise<ReturnType<typeof rawHost>> {
		const raw = rawHost(new FakeSession(`hello-${protocol}`), { protocol });
		await raw.send({ id: 1, method: "runtime.hello", params });
		return raw;
	}

	test("auto requires a non-empty supported-version advertisement to select v2", async () => {
		for (const params of [
			{ protocolVersion: 2 },
			{ protocolVersion: 2, supportedProtocolVersions: [] },
		]) {
			const raw = await helloSent("auto", params);
			expect(rawFrames(raw.socket)).toEqual([]);
			expect(raw.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));
		}
	});

	test("auto selects v2 only from the offered and advertised intersection", async () => {
		const raw = await helloSent("auto", {
			protocolVersion: 1,
			supportedProtocolVersions: [2, 1],
			preferProtocolVersion: 2,
		});
		expect((await waitForId(raw.socket, 1)).result).toMatchObject({
			protocolVersion: 2,
			supportedProtocolVersions: [2, 1],
		});

		// A peer that offers only v1 keeps the explicit legacy v1 path even
		// when it names a higher protocol version in the envelope.
		const legacy = await helloSent("auto", {
			protocolVersion: 2,
			supportedProtocolVersions: [1],
		});
		expect((await waitForId(legacy.socket, 1)).result?.protocolVersion).toBe(1);
	});

	test("keeps the empty-advertisement exception for the explicit legacy v1 path", async () => {
		const bare = await helloSent("auto", { protocolVersion: 1 });
		expect((await waitForId(bare.socket, 1)).result?.protocolVersion).toBe(1);
		const advertised = await helloSent("auto", {
			protocolVersion: 1,
			supportedProtocolVersions: [1],
		});
		expect((await waitForId(advertised.socket, 1)).result?.protocolVersion).toBe(1);
	});

	test("rejects malformed and incompatible v2 advertisements before requests", async () => {
		const malformed = await helloSent("auto", {
			protocolVersion: 2,
			supportedProtocolVersions: [2, "1"],
		});
		expect(malformed.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));

		const incompatible = await helloSent("auto", {
			protocolVersion: 2,
			supportedProtocolVersions: [3],
		});
		expect(incompatible.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));
	});

	test("v2 mode still requires the selected version to be v2", async () => {
		const downgrade = await helloSent("v2", {
			protocolVersion: 1,
			supportedProtocolVersions: [1],
		});
		expect(downgrade.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));

		const v2 = await helloSent("v2", {
			protocolVersion: 2,
			supportedProtocolVersions: [2, 1],
		});
		expect((await waitForId(v2.socket, 1)).result?.protocolVersion).toBe(2);
	});
});
