import { expect, test } from "bun:test";
import type { DeletionRecovery } from "@pixie/shared";
import {
	deletionProjectLabel,
	deletionReconciliationKey,
	normalizeDeletionReconciliation,
} from "@/session/deletion-reconciliation";
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
	phase: "confirmed",
	reason: "local cleanup is pending",
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

test("ungrouped reconciliation uses the explicit empty project key and rejects malformed rows", () => {
	const ungrouped: DeletionRecovery = {
		projectId: "",
		sessionId: "chat-a",
		phase: "requested",
		reason: "native outcome is uncertain",
	};
	expect(deletionReconciliationKey(ungrouped)).toBe("\0chat-a");
	expect(deletionProjectLabel(ungrouped)).toBe("Ungrouped");
	expect(
		normalizeDeletionReconciliation([
			ungrouped,
			{ ...ungrouped, projectId: "\0invalid" },
			{ ...ungrouped, sessionId: "" },
			{ ...ungrouped, phase: "quarantined" },
			{ ...ungrouped, reason: " " },
			ungrouped,
		]),
	).toEqual([ungrouped]);
});

test("the recovery list labels an explicit empty project as ungrouped", async () => {
	const markup = await renderSvelte("src/settings/sections/deletion-recovery.svelte", {
		records: [
			{
				projectId: "",
				sessionId: "chat-a",
				phase: "requested",
				reason: "native outcome is uncertain",
			},
		],
		pendingKey: null,
		error: null,
		onConfirm: () => {},
		onRetain: () => {},
	});
	expect(markup).toContain('data-project-id=""');
	expect(markup).toContain("Ungrouped");
});

test("the recovery list renders reconciliation state and exactly two native actions", async () => {
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
		"confirmed",
		"local cleanup is pending",
	]) {
		expect(markup).toContain(value);
	}
	expect(markup).toContain("Confirm deletion happened");
	expect(markup).toContain("Retain record");
	expect(markup).toContain("Outcome is uncertain");
	expect(markup).toContain("Confirmed; local cleanup may remain");
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

test("confirm and retain reconcile through explicit server operations", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/deletion-recovery.svelte", import.meta.url),
	).text();
	expect(source).toContain("onclick={() => onConfirm(record)}");
	expect(source).toContain("onclick={() => onRetain(record)}");
	expect(source).toMatch(/<button\s+type="button"/);
	// Exactly the two explicit actions, and no blind clear entry point.
	expect(source.match(/data-testid="deletion-recovery-(?:confirm|retain)"/g)).toHaveLength(2);
	expect(source).not.toMatch(/data-testid="[^"]*clear/i);

	const system = await Bun.file(
		new URL("../../../webui/src/settings/sections/system-settings.svelte", import.meta.url),
	).text();
	expect(system).toContain('"session.confirmExternalDeletion"');
	expect(system).toContain('"session.retainExternalDeletion"');
	expect(system).toContain('"session.deletionRecovery"');
	expect(system).toContain("removeDeletionRecovery");
	expect(system).toContain("Retained deletion record for");
	const retainBody = system.match(/async function retainDeletion[\s\S]*?\n\}/)?.[0] ?? "";
	expect(retainBody).toContain("request");
	expect(retainBody).not.toContain("removeDeletionRecovery");
	expect(retainBody).not.toContain("confirmExternalDeletion");
	expect(system).toContain("void refresh(connectionGeneration)");
});
