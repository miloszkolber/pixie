import { expect, test } from "bun:test";
import { mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { DefaultPackageManager, SettingsManager } from "@earendil-works/pi-coding-agent";

test("public native package resolver can deny missing installs without fetching or mutating configuration", async () => {
	const root = await mkdtemp("/tmp/opencode/pixie-native-package-policy-");
	try {
		const cwd = join(root, "project"),
			agentDir = join(root, "agent");
		await mkdir(cwd);
		await mkdir(agentDir);
		const source = "npm:@pixie-fixture/never-install-this-package@0.0.0";
		await writeFile(join(agentDir, "settings.json"), JSON.stringify({ packages: [source] }));
		const settings = SettingsManager.create(cwd, agentDir);
		const manager = new DefaultPackageManager({ cwd, agentDir, settingsManager: settings });
		const requested: string[] = [];
		const progress: unknown[] = [];
		manager.setProgressCallback((event) => progress.push(event));
		await expect(
			manager.resolve(async (missing) => {
				requested.push(missing);
				return "error";
			}),
		).rejects.toThrow("Missing source");
		expect(requested).toEqual([source]);
		expect(progress).toEqual([]);
		expect(settings.getPackages()).toEqual([source]);
		expect(await readdir(agentDir)).toEqual(["settings.json"]);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});
