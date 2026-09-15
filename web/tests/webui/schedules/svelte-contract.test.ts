import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const schedulesRoot = new URL("../../../webui/src/schedules/", import.meta.url);
const components = [
	"schedule-list.svelte",
	"schedule-detail.svelte",
	"schedules-view.svelte",
	"schedule-form.svelte",
] as const;

test("every schedules Svelte component parses without compiler warnings or React imports", async () => {
	for (const relativePath of components) {
		const url = new URL(relativePath, schedulesRoot);
		const source = await Bun.file(url).text();
		expect(source.length).toBeGreaterThan(0);
		expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
		const result = compile(source, { filename: url.pathname, generate: false });
		expect(result.warnings).toEqual([]);
	}
});

test("the workspace schedules surface reuses backend ledger semantics", async () => {
	const sources = await Promise.all(
		components.map((relativePath) => Bun.file(new URL(relativePath, schedulesRoot)).text()),
	);
	const source = sources.join("\n");
	for (const contract of [
		'schedules-filter"',
		'schedule-row"',
		'schedule-detail"',
		'schedule-missing"',
		'schedule-empty"',
		'schedule-runs"',
		'schedule-create"',
		"Missed occurrences coalesce",
		"Runs never overlap",
		"Run now",
		"Stop execution",
		"Execution ledger",
		"Next dispatch paused",
		"scheduleTime",
		"activeExecution",
		"scheduleSessionHref",
		"Retry same action",
		"Discard retry",
		"Maximum runtime",
		"Use controller default",
		"Unlimited",
		"Cancellation was accepted by Pixie",
		"Pixie cannot confirm that Pi stopped.",
		"Retry cancellation",
		"Native Pi default",
		"Open Pi session",
	]) {
		expect(source).toContain(contract);
	}
});

test("the workspace shell keeps schedules and settings list/detail slots", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/workspace/views/project-work-area.svelte", import.meta.url),
	).text();
	for (const contract of [
		'schedules-sidebar"',
		'settings-sidebar"',
		'settings-detail"',
		'settings-section-row"',
		"ScheduleList",
		"ScheduleDetail",
		"settingsTabs",
		"resolveWorkspaceSettingsSection",
		"selectSettingsSection",
	]) {
		expect(source).toContain(contract);
	}
	// The shell keeps its slots; schedules/settings render inside them without new rails or nav.
	for (const contract of ['data-slot="primary-view"', 'data-testid="primary-view"']) {
		expect(source).toContain(contract);
	}
});

test("the schedules surfaces keep one list-level help sentence and one detail selection state", async () => {
	const [view, list, detail] = await Promise.all(
		["schedules-view.svelte", "schedule-list.svelte", "schedule-detail.svelte"].map((name) =>
			Bun.file(new URL(name, schedulesRoot)).text(),
		),
	);
	const explanation =
		"Pixie must be running to dispatch schedules. Missed occurrences coalesce into one run. Runs never overlap for the same schedule.";
	// The list owns the shared explanation; the detail does not repeat it.
	expect(list).toContain(explanation);
	expect(view).not.toContain(explanation);
	expect(detail).not.toContain(explanation);
	// The detail owns the distinct selection prompt and no list-level empty copy.
	expect(detail).toContain(
		"Select a schedule in the list to inspect its timing, next occurrence and run ledger.",
	);
	expect(detail).not.toContain("No schedules in this project.");
	// Each list surface still states the empty case exactly once.
	expect(list).toContain("No schedules in this project.");
	expect(view).toContain("No schedules in this project.");
});
