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
