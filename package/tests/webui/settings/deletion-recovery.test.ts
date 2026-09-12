import { expect, test } from "bun:test";
import type { DeletionRecovery } from "@pixie/contracts";
import {
	deletionRecoveryKey,
	dropDeletionRecovery,
	normalizeDeletionRecovery,
} from "@/settings/deletion-recovery";
import { renderSvelte } from "../chat/svelte-render";

const first: DeletionRecovery = {
	projectId: "project-a",
	sessionId: "chat-a",
	phase: "requested",
	reason: "native delete is unsupported",
};
const second: DeletionRecovery = {
	projectId: "project-b",
	sessionId: "chat-b",
	phase: "quarantined",
	reason: "binding did not match the host identity",
};
const records: DeletionRecovery[] = [first, second];

test("the welcome/readiness payload is validated before tombstones are trusted", () => {
	expect(normalizeDeletionRecovery(undefined)).toEqual([]);
	expect(normalizeDeletionRecovery("not-an-array")).toEqual([]);
	expect(
		normalizeDeletionRecovery([
			first,
			{ projectId: "project-c", sessionId: "chat-c", phase: "requested" },
			null,
			{ projectId: 1, sessionId: "chat-d", phase: "requested", reason: "bad" },
		]),
	).toEqual([first]);
});

test("a confirmed tombstone drops exactly one identity", () => {
	expect(deletionRecoveryKey(first)).toBe("project-a\0chat-a");
	expect(dropDeletionRecovery(records, "project-a", "chat-a")).toEqual([second]);
	expect(dropDeletionRecovery(records, "project-z", "chat-z")).toEqual(records);
});

test("the recovery list renders project, session, phase, reason and exactly two actions", async () => {
	const markup = await renderSvelte("src/settings/sections/deletion-recovery.svelte", {
		records,
		pendingKey: null,
		error: null,
		onConfirm: () => {},
		onRetain: () => {},
	});
	expect(markup).toContain('data-testid="deletion-recovery"');
	expect(markup.match(/data-testid="deletion-recovery-row"/g)).toHaveLength(2);
	for (const value of [
		"project-a",
		"chat-a",
		"requested",
		"native delete is unsupported",
		"project-b",
		"chat-b",
		"quarantined",
		"binding did not match the host identity",
	]) {
		expect(markup).toContain(value);
	}
	expect(markup).toContain("Confirm deletion happened");
	expect(markup).toContain("Retain record");
	expect(markup.match(/deletion-recovery-confirm/g)).toHaveLength(2);
	expect(markup.match(/deletion-recovery-retain/g)).toHaveLength(2);
	expect(markup).not.toMatch(/clear/i);
});

test("the empty recovery list never invents a record or a clear control", async () => {
	const markup = await renderSvelte("src/settings/sections/deletion-recovery.svelte", {
		records: [],
		pendingKey: null,
		error: null,
		onConfirm: () => {},
		onRetain: () => {},
	});
	expect(markup).toContain('data-testid="deletion-recovery-empty"');
	expect(markup).not.toContain("deletion-recovery-row");
	expect(markup).not.toContain("Confirm deletion happened");
	expect(markup).not.toContain("Retain record");
});

test("confirm reconciles through the server; retain never mutates", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/deletion-recovery.svelte", import.meta.url),
	).text();
	expect(source).toContain("onclick={() => onConfirm(record)}");
	expect(source).toContain("onclick={() => onRetain(record)}");
	// Exactly the two explicit actions, and no blind clear entry point.
	expect(source.match(/data-testid="deletion-recovery-(?:confirm|retain)"/g)).toHaveLength(2);
	expect(source).not.toMatch(/data-testid="[^"]*clear/i);

	const system = await Bun.file(
		new URL("../../../webui/src/settings/sections/system-settings.svelte", import.meta.url),
	).text();
	expect(system).toContain('"session.confirmExternalDeletion"');
	expect(system).toContain('"session.deletionRecovery"');
	expect(system).toContain("removeDeletionRecovery");
	const retainBody = system.match(/function retainDeletion\(\): void \{([\s\S]*?)\n\}/)?.[1] ?? "";
	expect(retainBody).not.toContain("request");
	expect(retainBody).not.toContain("confirmExternalDeletion");
});
