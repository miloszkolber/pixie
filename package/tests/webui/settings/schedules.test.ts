import { expect, test } from "bun:test";
import type { Schedule } from "@pixie/contracts";
import type { WsTransport } from "@/connection/transport";
import { activeExecution, SchedulesModel, scheduleSessionHref } from "@/schedules/schedules-model";
import { resolveSettingsSection, settingsTabs } from "@/settings/settings-dialog";
import { SettingsSection } from "@/settings/state";
import { parseFragment } from "@/workspace/navigation/location";

const job = (id: string, projectId = "p"): Schedule => ({
	id,
	projectId,
	root: "/p",
	prompt: "Review",
	cron: "0 9 * * *",
	timezone: "UTC",
	paused: false,
	nextRun: "2027-01-01T09:00:00Z",
	runs: [],
});

function fixture(projectId = "p") {
	let jobs = [job("first", projectId), job("second", projectId)];
	let failRead = false;
	let loseMutationReply = false;
	let dispatches = 0;
	const calls: { method: string; params: Record<string, unknown> }[] = [];
	const results = new Map<string, unknown>();
	const transport = {
		request: async (method: string, params: Record<string, unknown>) => {
			calls.push({ method, params: structuredClone(params) });
			if (method === "schedule.health") return { error: "" };
			if (method === "schedule.list") {
				if (failRead) throw new Error("offline");
				return structuredClone(jobs);
			}
			const key = String(params.mutationId);
			if (results.has(key)) return results.get(key);
			let result: unknown = { ok: true };
			if (method === "schedule.create") {
				const created = { ...job("created", projectId), ...params };
				jobs.push(created);
				result = created;
			}
			const current = jobs.find((item) => item.id === params.scheduleId);
			if (method === "schedule.update" && current) {
				Object.assign(current, params);
				result = current;
			}
			if (method === "schedule.delete") jobs = jobs.filter((item) => item.id !== params.scheduleId);
			if (method === "schedule.runNow" && current) {
				dispatches++;
				current.runs.unshift({
					id: "run",
					sessionId: "native/session",
					status: "running",
					startedAt: "2027-01-01T08:00:00Z",
				});
			}
			if (method === "schedule.stop" && current?.runs[0]) current.runs[0].status = "interrupted";
			results.set(key, structuredClone(result));
			if (loseMutationReply) {
				loseMutationReply = false;
				throw new Error("reply timed out");
			}
			return result;
		},
	} as unknown as Pick<WsTransport, "request">;
	return {
		model: new SchedulesModel(projectId, transport),
		transport,
		calls,
		jobs: () => jobs,
		dispatches: () => dispatches,
		failRead: () => {
			failRead = true;
		},
		healRead: () => {
			failRead = false;
		},
		loseReply: () => {
			loseMutationReply = true;
		},
	};
}

test("schedule CRUD retains selection, reconciles deletion and filters project scope", async () => {
	const f = fixture();
	f.jobs().push(job("foreign", "other"));
	await f.model.load();
	f.model.select("second");
	await f.model.load();
	expect(f.model.state.getState().selectedId).toBe("second");
	expect(f.model.state.getState().jobs).toHaveLength(2);
	expect(
		await f.model.mutate(
			"schedule.create",
			{ root: "/p", prompt: "New", cron: "0 10 * * *", timezone: "Europe/Warsaw" },
			"created",
		),
	).toBe(true);
	expect(f.model.state.getState().selectedId).toBe("created");
	await f.model.mutate("schedule.update", { scheduleId: "created", prompt: "Changed" }, "saved");
	expect(f.model.state.getState().jobs.find((item) => item.id === "created")?.prompt).toBe(
		"Changed",
	);
	await f.model.mutate("schedule.delete", { scheduleId: "created" }, "deleted");
	expect(f.model.state.getState().selectedId).toBe("first");
	expect(f.calls.every((call) => call.params.projectId === "p")).toBe(true);
});

test("uncertain run-now retains its mutation identity across view recreation and cannot duplicate dispatch", async () => {
	const f = fixture("retry-project");
	await f.model.load();
	f.loseReply();
	expect(await f.model.mutate("schedule.runNow", { scheduleId: "first" }, "run requested")).toBe(
		false,
	);
	expect(f.model.state.getState().notice).toContain("not confirmed");
	expect(await f.model.mutate("schedule.runNow", { scheduleId: "first" }, "another run")).toBe(
		false,
	);
	const other = fixture("unrelated-project");
	expect(other.model.state.getState().pending).toBeNull();
	const reopened = new SchedulesModel("retry-project", f.transport);
	expect(await reopened.retry()).toBe(true);
	const runs = f.calls.filter((call) => call.method === "schedule.runNow");
	expect(runs).toHaveLength(2);
	expect(runs[0]?.params.mutationId).toBeTruthy();
	expect(runs[1]?.params).toEqual(runs[0]?.params);
	expect(f.dispatches()).toBe(1);
	expect(reopened.state.getState().pending).toBeNull();
});

test("pause, paused run-now, stop and resume reflect server outcomes and preserve native session links", async () => {
	const f = fixture("actions");
	await f.model.load();
	await f.model.mutate("schedule.update", { scheduleId: "first", paused: true }, "paused");
	await f.model.mutate("schedule.runNow", { scheduleId: "first" }, "run requested");
	const running = f.model.state.getState().jobs[0];
	if (!running || !running.runs[0]) throw new Error("execution missing");
	expect(running.paused).toBe(true);
	expect(activeExecution(running)?.sessionId).toBe("native/session");
	const href = scheduleSessionHref("actions", running.runs[0]);
	if (!href) throw new Error("native session link missing");
	expect(parseFragment(href)).toEqual({
		kind: "chat",
		projectId: "actions",
		projectAreaId: "actions",
		sessionId: "native/session",
	});
	await f.model.mutate("schedule.stop", { scheduleId: "first" }, "stop requested");
	const stopped = f.model.state.getState().jobs[0];
	if (!stopped) throw new Error("schedule missing after stop");
	expect(activeExecution(stopped)).toBeUndefined();
	expect(f.model.state.getState().jobs[0]?.runs[0]?.status).toBe("interrupted");
	await f.model.mutate("schedule.update", { scheduleId: "first", paused: false }, "resumed");
	expect(f.model.state.getState().jobs[0]?.paused).toBe(false);
	const ids = f.calls
		.filter((call) => call.params.mutationId)
		.map((call) => call.params.mutationId);
	expect(new Set(ids).size).toBe(4);
});

test("refresh failure preserves the ledger and an acknowledged write is not retried as a failed mutation", async () => {
	const f = fixture("read-failure");
	await f.model.load();
	f.failRead();
	expect(
		await f.model.mutate("schedule.update", { scheduleId: "first", paused: true }, "paused"),
	).toBe(true);
	expect(f.model.state.getState().jobs).toHaveLength(2);
	expect(f.model.state.getState().error).toContain("Last known results");
	expect(f.model.state.getState().pending).toBeNull();
	expect(f.model.state.getState().notice).toBe("paused");
});

test("reconnect after a read failure refreshes the ledger and clears the retained error", async () => {
	const f = fixture("reconnect");
	await f.model.load();
	f.failRead();
	await f.model.load();
	expect(f.model.state.getState().error).toContain("Last known results");
	expect(f.model.state.getState().jobs).toHaveLength(2);
	f.healRead();
	f.jobs().push(job("created-while-away", "reconnect"));
	await f.model.load();
	expect(f.model.state.getState().error).toBeNull();
	expect(f.model.state.getState().jobs.map((item) => item.id)).toEqual([
		"first",
		"second",
		"created-while-away",
	]);
});

test("concurrent refresh calls share one read and missing native sessions do not produce invented links", async () => {
	const f = fixture("polling");
	await Promise.all([f.model.load(), f.model.load(), f.model.load()]);
	expect(f.calls.filter((call) => call.method === "schedule.list")).toHaveLength(1);
	expect(
		scheduleSessionHref("polling", {
			id: "run",
			status: "failed",
			startedAt: "2027-01-01T00:00:00Z",
		}),
	).toBeNull();
});

test("Schedules is a Pixie section even when native administration is unavailable", () => {
	expect(resolveSettingsSection(SettingsSection.Schedules, null)).toBe(SettingsSection.Schedules);
	for (const tabs of [settingsTabs(), settingsTabs(true), settingsTabs(false, true)]) {
		expect(tabs.some((tab) => tab.label === "Schedules")).toBe(true);
	}
	for (const tabs of [settingsTabs(true), settingsTabs(false, true)]) {
		expect(tabs.some((tab) => tab.label === "Extensions")).toBe(false);
	}
});
