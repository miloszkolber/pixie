import { expect, test } from "bun:test";
import type { Schedule } from "@pixie/contracts";
import { scheduleTime } from "@/schedules/schedules-model";
import {
	filterSchedules,
	resolveScheduleSelection,
	resolveWorkspaceSettingsSection,
	scheduleStatusLabel,
} from "@/schedules/schedules-workspace";

const job = (id: string, overrides: Partial<Schedule> = {}): Schedule => ({
	id,
	projectId: "p",
	root: "/p",
	prompt: "Review inbox",
	cron: "0 9 * * *",
	timezone: "UTC",
	paused: false,
	nextRun: "2027-01-01T09:00:00Z",
	runs: [],
	...overrides,
});

test("schedule filtering matches prompt, cron, timezone and id without hiding all on empty", () => {
	const jobs = [
		job("first", { prompt: "Review inbox", timezone: "Europe/Warsaw" }),
		job("second", { prompt: "Nightly backup", cron: "0 2 * * *" }),
	];
	expect(filterSchedules(jobs, "").map((item) => item.id)).toEqual(["first", "second"]);
	expect(filterSchedules(jobs, "  ").map((item) => item.id)).toEqual(["first", "second"]);
	expect(filterSchedules(jobs, "review").map((item) => item.id)).toEqual(["first"]);
	expect(filterSchedules(jobs, "WARSAW").map((item) => item.id)).toEqual(["first"]);
	expect(filterSchedules(jobs, "0 2").map((item) => item.id)).toEqual(["second"]);
	expect(filterSchedules(jobs, "second").map((item) => item.id)).toEqual(["second"]);
	expect(filterSchedules(jobs, "no-match")).toEqual([]);
});

test("schedule selection resolves the requested run without redirecting when it is missing", () => {
	const jobs = [job("first"), job("second")];
	expect(resolveScheduleSelection(jobs, null, "p")).toEqual({
		job: null,
		requestedId: null,
		missing: false,
	});
	expect(resolveScheduleSelection(jobs, { kind: "session", sessionId: "s" }, "p")).toEqual({
		job: null,
		requestedId: null,
		missing: false,
	});
	expect(resolveScheduleSelection(jobs, { kind: "settings", sectionId: "providers" }, "p")).toEqual(
		{ job: null, requestedId: null, missing: false },
	);
	expect(
		resolveScheduleSelection(
			jobs,
			{ kind: "schedule", scheduleId: "first", projectId: "other" },
			"p",
		),
	).toEqual({ job: null, requestedId: null, missing: false });
	const found = resolveScheduleSelection(
		jobs,
		{ kind: "schedule", scheduleId: "second", projectId: "p" },
		"p",
	);
	expect(found.requestedId).toBe("second");
	expect(found.missing).toBe(false);
	expect(found.job?.id).toBe("second");
	const missing = resolveScheduleSelection(
		jobs,
		{ kind: "schedule", scheduleId: "deleted", projectId: "p" },
		"p",
	);
	expect(missing).toEqual({ job: null, requestedId: "deleted", missing: true });
	// The jobs are retained for the list; the detail column recovers without picking the first job.
	expect(jobs).toHaveLength(2);
});

test("schedule status reuses paused, running and ledger outcome labels", () => {
	expect(scheduleStatusLabel(job("a"), false)).toBe("Enabled · Not run yet");
	expect(scheduleStatusLabel(job("a", { paused: true }), false)).toBe("Paused · Not run yet");
	expect(
		scheduleStatusLabel(
			job("a", { runs: [{ id: "r", status: "completed", startedAt: "2027-01-01T00:00:00Z" }] }),
			false,
		),
	).toBe("Enabled · completed");
	expect(scheduleStatusLabel(job("a"), true)).toBe("Enabled · Running");
	expect(scheduleStatusLabel(job("a", { paused: true }), true)).toBe("Paused · Running");
});

test("workspace settings resolution prefers the primary selection and validates the fallback", () => {
	expect(
		resolveWorkspaceSettingsSection({ kind: "settings", sectionId: "providers" }, "system"),
	).toBe("providers");
	expect(
		resolveWorkspaceSettingsSection({ kind: "settings", sectionId: "schedules" }, "system"),
	).toBe("schedules");
	expect(resolveWorkspaceSettingsSection(null, "providers")).toBe("providers");
	expect(
		resolveWorkspaceSettingsSection({ kind: "schedule", scheduleId: "s", projectId: "p" }, "pi"),
	).toBe("pi");
	expect(
		resolveWorkspaceSettingsSection({ kind: "settings", sectionId: "invented" }, "models"),
	).toBe("models");
	expect(resolveWorkspaceSettingsSection(null, "invented")).toBe("system");
});

test("schedule times reuse the IANA timezone and fall back without inventing a time", () => {
	const instant = "2027-07-01T12:00:00Z";
	const utc = scheduleTime(instant, "UTC");
	const warsaw = scheduleTime(instant, "Europe/Warsaw");
	expect(utc).not.toBe(instant);
	expect(warsaw).not.toBe(instant);
	expect(warsaw).not.toBe(utc);
	expect(scheduleTime(instant, "Not/AZone")).toBe(instant);
	expect(scheduleTime("not-a-date", "UTC")).toBe("not-a-date");
});
