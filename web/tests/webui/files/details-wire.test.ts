import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";
import { errorText } from "@/connection";
import { releaseSession } from "@/connection/session-release";
import type { WsTransport } from "@/connection/transport";
import { describeContextUsage, describeTokenCount } from "@/files/changes/details-model";

const webuiRoot = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiRoot)).text();
}

test("release stays on the authoritative session.release transport", async () => {
	const calls: Array<{ method: string; params: unknown }> = [];
	const transport = {
		request: (async (method: string, params: unknown) => {
			calls.push({ method, params });
			return { ok: true as const };
		}) as unknown as WsTransport["request"],
	};
	const result = await releaseSession({ projectId: "project", sessionId: "session" }, transport);
	expect(calls).toEqual([
		{ method: "session.release", params: { projectId: "project", sessionId: "session" } },
	]);
	expect(result).toEqual({ ok: true });
});

test("release surfaces backend refusal verbatim", async () => {
	const failure = new Error("session is not idle: session still running");
	const transport = {
		request: (async () => {
			throw failure;
		}) as unknown as WsTransport["request"],
	};
	try {
		await releaseSession({ projectId: "project", sessionId: "session" }, transport);
		expect.unreachable();
	} catch (error) {
		expect(errorText(error)).toBe("session is not idle: session still running");
	}
});

test("wired stats never render unknown tokens or context as zero", () => {
	expect(describeTokenCount(0, undefined)).toBe("Unknown");
	expect(describeTokenCount(0, false)).toBe("Unknown");
	expect(describeContextUsage(null)).toBe("Unknown");
	expect(describeContextUsage(undefined)).toBe("Unknown");
});

test("details panel compiles and wires live stats with gated release control", async () => {
	const panel = await source("files/changes/details-panel.svelte");
	compile(panel, { filename: "details-panel.svelte", generate: "client", modernAst: true });
	for (const contract of [
		"session.list",
		"session.getStats",
		"connection/session-release",
		"releaseSession",
		"describeTokenCount",
		"describeContextUsage",
		"formatSessionModel",
		"formatThinkingLevel",
		"sessionStatusText",
		"shouldShowReleaseAffordance",
		"releaseAffordanceReason",
		"removedProjectAreaIds",
		"connectionGeneration",
		"Read-only",
		'role="alert"',
		"disabled={releaseBusy}",
	]) {
		expect(panel).toContain(contract);
	}
	for (const hook of [
		'data-testid="details-panel"',
		'data-testid="details-session"',
		'data-testid="details-model"',
		'data-testid="details-thinking"',
		'data-testid="details-status"',
		'data-testid="details-usage"',
		'data-testid="details-context"',
		'data-testid="details-release"',
		'data-testid="details-release-reason"',
		'data-testid="details-release-error"',
		'data-testid="details-stale"',
	]) {
		expect(panel).toContain(hook);
	}
	expect(panel).toContain('"Unknown"');
	expect(panel).not.toContain("session.setLeases");
});

test("secondary sidebar details region delegates to the wired panel", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	compile(workArea, { filename: "project-work-area.svelte", generate: false });
	expect(workArea).toContain('import DetailsPanel from "../../files/changes/details-panel.svelte"');
	expect(workArea).toContain('data-testid="details-sidebar"');
	expect(workArea).toContain("<DetailsPanel");
	expect(workArea).toContain("sessionId={sessionDetailsVisible");
	expect(workArea).toContain('ErrorBoundary label="details"');
	expect(workArea).not.toContain("session.release");
	expect(workArea).not.toContain("session.setLeases");
});
