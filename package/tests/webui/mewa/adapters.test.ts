import { expect, test } from "bun:test";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);
const webuiRoot = new URL("../../../webui/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

test("button wrapper keeps the documented mewa contract", async () => {
	const button = await source("components/button.svelte");
	expect(button).toContain("class={`btn");
	expect(button).toContain("data-variant={variant}");
	expect(button).toContain("data-size");
	for (const variant of [
		'"default"',
		'"secondary"',
		'"outline"',
		'"ghost"',
		'"destructive"',
		'"destructive-outline"',
		'"link"',
	]) {
		expect(button).toContain(variant);
	}
	for (const size of ['"default"', '"sm"', '"icon"', '"icon-sm"']) {
		expect(button).toContain(size);
	}
	expect(button).not.toContain("!important");
});

test("dialog wrapper keeps the documented mewa contract and lifecycle", async () => {
	const dialog = await source("components/dialog.svelte");
	for (const contract of [
		"<dialog",
		"class={`dialog",
		"dialog-content",
		"dialog-header",
		"dialog-title",
		"dialog-description",
		"dialog-body",
		"dialog-footer",
		'showModal()',
		"onDestroy",
		'aria-labelledby',
	]) {
		expect(dialog).toContain(contract);
	}
	expect(dialog).toContain('role');
	expect(dialog).not.toContain("!important");
});

test("icon wrapper resolves exactly the locked vendor icon set", async () => {
	const icon = await source("components/icon.svelte");
	const lock = (await Bun.file(new URL("vendor/mewa.lock.json", webuiRoot)).json()) as {
		icons: string[];
	};
	const files = [...icon.matchAll(/vendor\/mewa-icons\/icons\/([a-z0-9-]+)\.svg/g)].map(
		(match) => match[1] ?? "",
	);
	expect(files).toHaveLength(lock.icons.length);
	expect([...files].sort()).toEqual([...lock.icons].sort());
	expect(icon).toContain("aria-hidden");
});

test("interactive wrappers use scoped attachments with one lifecycle owner", async () => {
	const mewaSvelte = await Bun.file(
		new URL("vendor/mewa-svelte/index.js", webuiRoot),
	).text();
	expect(mewaSvelte).toContain("behavior.destroy");

	for (const path of [
		"workspace/projects/add-project-menu.svelte",
		"workspace/projects/project-chat-history.svelte",
		"files/changes/git-scope-menu.svelte",
	]) {
		const wrapper = await source(path);
		expect(wrapper).toContain("@attach mewa(");
		expect(wrapper).toContain("vendor/mewa-ui/components/");
		expect(wrapper).not.toContain("vendor/mewa-ui/auto/");
	}
	const icon = await source("components/icon.svelte");
	expect(icon).not.toContain("vendor/mewa-ui/auto/");
	const button = await source("components/button.svelte");
	expect(button).not.toContain("vendor/mewa-ui/auto/");
	const dialog = await source("components/dialog.svelte");
	expect(dialog).not.toContain("vendor/mewa-ui/auto/");
});
