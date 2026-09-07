import { expect, test } from "bun:test";
import type { SlashCommandInfo } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import {
	agentMentionLabel,
	agentMentionSummary,
	agentMentionTypeLabel,
	clampedMentionActiveIndex,
	clipboardImageName,
	composerEnterBehavior,
	insertedMention,
	insertImageTags,
	type MentionCandidate,
	mentionCompletionKeyAction,
	removeImageTag,
	removeImageTags,
	reserveClipboardImageNames,
	streamingSendModes,
	streamingSubmitBehavior,
} from "@/chat/composer/composer-state";
import {
	clampedSlashActiveIndex,
	matchSlashCommands,
	selectedSlashCommandValue,
	slashCommandQuery,
	slashCompletionKeyAction,
} from "@/chat/composer/slash-command-completion";

const agentMention: MentionCandidate = {
	kind: "agent",
	name: "Reviewer",
	description: "Review the current change",
	sourceType: "agent",
	mention: "@reviewer",
};

const command: SlashCommandInfo = {
	name: "review",
	description: "Review the change",
	inputHint: "optional focus",
	source: "pi",
	sourceInfo: { path: "builtin", source: "pi", scope: "user", origin: "top-level" },
};

test("keeps Pi agent mentions exact while preserving file mention syntax", () => {
	expect(insertedMention(agentMention)).toBe("@reviewer");
	expect(insertedMention({ kind: "file", name: "main.ts", path: "src/main.ts" })).toBe(
		"@src/main.ts",
	);
	expect(insertedMention({ kind: "dir", name: "src", path: "src" })).toBe("@src/");
});

test("defines a distinct accessible agent mention completion entry", () => {
	if (agentMention.kind !== "agent") throw new Error("agent mention fixture is invalid");
	expect(agentMentionLabel(agentMention)).toBe(
		"agent mention: Reviewer. Review the current change",
	);
	expect(agentMentionSummary(agentMention)).toBe("agent · Review the current change");
});

test("supports every official mention source type without inventing discovery", () => {
	for (const sourceType of ["skill", "builtinSkill", "agent", "project"] as const) {
		const candidate: MentionCandidate = {
			kind: "agent",
			name: "Source",
			description: "Exact Pi target",
			sourceType,
			mention: "@exact-pi-target",
		};
		expect(insertedMention(candidate)).toBe("@exact-pi-target");
		expect(agentMentionLabel(candidate)).toContain(agentMentionTypeLabel(sourceType));
	}
});

test("shrinking completion candidates clamps selection to a visible entry", () => {
	expect(clampedMentionActiveIndex(4, 1)).toBe(0);
	expect(clampedMentionActiveIndex(4, 0)).toBe(0);
	expect(mentionCompletionKeyAction("Enter", true, 4, 1)).toEqual({ type: "select", index: 0 });
	expect(mentionCompletionKeyAction("Tab", true, 4, 1)).toEqual({ type: "select", index: 0 });
	expect(clampedSlashActiveIndex(4, 1)).toBe(0);
	expect(slashCompletionKeyAction("Enter", true, 4, 1)).toEqual({ type: "select", index: 0 });
});

test("streaming shortcuts preserve steer, queue, interrupt, and IME boundaries", () => {
	expect(streamingSubmitBehavior(false)).toBe("queue");
	expect(streamingSubmitBehavior(true)).toBe("steer");
	expect(streamingSendModes(false).map((mode) => mode.behavior)).toEqual(["queue", "interrupt"]);
	expect(
		composerEnterBehavior(
			{ key: "Enter", shiftKey: false, metaKey: false, ctrlKey: false },
			true,
			true,
		),
	).toBe("steer");
	expect(
		composerEnterBehavior(
			{ key: "Enter", shiftKey: false, metaKey: true, ctrlKey: false },
			true,
			true,
		),
	).toBe("queue");
	expect(
		composerEnterBehavior(
			{ key: "Enter", shiftKey: true, metaKey: false, ctrlKey: true },
			true,
			true,
		),
	).toBe("interrupt");
	expect(
		composerEnterBehavior(
			{ key: "Enter", shiftKey: false, metaKey: false, ctrlKey: false, isComposing: true },
			false,
			true,
		),
	).toBeNull();
	expect(
		composerEnterBehavior(
			{ key: "Enter", shiftKey: false, metaKey: false, ctrlKey: false, keyCode: 229 },
			false,
			true,
		),
	).toBeNull();
	expect(
		composerEnterBehavior(
			{ key: "Enter", shiftKey: true, metaKey: false, ctrlKey: false },
			false,
			true,
		),
	).toBeNull();
});

test("slash commands match, select, and retain separate names and hints", () => {
	expect(slashCommandQuery("/rev")).toBe("rev");
	expect(slashCommandQuery("/review now")).toBeNull();
	expect(matchSlashCommands("/REV", [command])).toEqual([command]);
	expect(selectedSlashCommandValue(command)).toBe("/review ");
});

test("clipboard image tags use safe unique names and remove only their matching tag", () => {
	expect(clipboardImageName("", "image/png", ["image-1.png"], "[image-2.png]")).toBe("image-3.png");
	expect(clipboardImageName("image.jpeg", "image/jpeg", [], "")).toBe("image-1.jpg");
	expect(clipboardImageName("", "image/jpeg", ["image-1.png"], "")).toBe("image-2.jpg");
	expect(clipboardImageName("photo.png", "image/png", ["photo.png"], "")).toBe("photo-2.png");
	const inserted = insertImageTags("Inspect this", 7, 12, ["image-1.png", "image-2.jpg"]);
	expect(inserted).toEqual({
		value: "Inspect [image-1.png] [image-2.jpg]",
		caret: 35,
	});
	expect(removeImageTag(inserted.value, "image-1.png")).toBe("Inspect  [image-2.jpg]");
});

test("concurrent clipboard reservations keep paste order when conversions settle in reverse", async () => {
	const first = reserveClipboardImageNames([{ name: "", type: "image/png" }], [], "");
	const firstDraft = insertImageTags("", 0, 0, first);
	const second = reserveClipboardImageNames(
		[{ name: "", type: "image/jpeg" }],
		first,
		firstDraft.value,
	);
	const secondDraft = insertImageTags(firstDraft.value, firstDraft.caret, firstDraft.caret, second);
	expect(first).toEqual(["image-1.png"]);
	expect(second).toEqual(["image-2.jpg"]);

	let finishFirst: (() => void) | undefined;
	let finishSecond: (() => void) | undefined;
	const firstConversion = new Promise<void>((resolve) => (finishFirst = resolve));
	const secondConversion = new Promise<void>((resolve) => (finishSecond = resolve));
	const settled: string[] = [];
	void firstConversion.then(() => settled.push(first[0] ?? ""));
	void secondConversion.then(() => settled.push(second[0] ?? ""));
	finishSecond?.();
	await Promise.resolve();
	expect(settled).toEqual(["image-2.jpg"]);
	expect(secondDraft.value).toBe("[image-1.png] [image-2.jpg]");
	finishFirst?.();
	await Promise.resolve();
	expect(settled).toEqual(["image-2.jpg", "image-1.png"]);
});

test("failed clipboard conversions remove their tag from the latest intervening draft", () => {
	const tagged = insertImageTags("Review", 6, 6, ["image-1.png", "image-2.jpg"]);
	const edited = `${tagged.value} after typing`;
	const removal = removeImageTags(edited, edited.length, ["image-1.png"]);
	expect(removal.value).toBe("Review  [image-2.jpg] after typing");
	expect(removal.caret).toBe(edited.length - "[image-1.png]".length);
});

const composerRoot = new URL("../../../webui/src/chat/composer/", import.meta.url);
const composerComponents = [
	"composer.svelte",
	"file-chip.svelte",
	"image-chip.svelte",
	"slash-command-menu.svelte",
] as const;

test("every composer Svelte component parses without warnings or React imports", async () => {
	for (const relativePath of composerComponents) {
		const url = new URL(relativePath, composerRoot);
		const source = await Bun.file(url).text();
		expect(source.length).toBeGreaterThan(0);
		expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react|@radix-ui)/);
		const result = compile(source, { filename: url.pathname, generate: false });
		expect(result.warnings).toEqual([]);
	}
});

test("the Svelte composer retains completion, attachment, send, and focus contracts", async () => {
	const composerSource = await Bun.file(new URL("composer.svelte", composerRoot)).text();
	const slashSource = await Bun.file(new URL("slash-command-menu.svelte", composerRoot)).text();
	const imageSource = await Bun.file(new URL("image-chip.svelte", composerRoot)).text();

	for (const testId of [
		"chat-input",
		"mention-menu",
		"composer-attachments",
		"composer-image-error",
		"composer-image",
		"composer-text-attachment",
		"composer-attachment-pending",
		"file-attach",
		"history-open",
		"chat-abort",
		"send-menu",
		"chat-send",
	]) {
		expect(
			composerSource.includes(`data-testid="${testId}"`) ||
				composerSource.includes(`testid="${testId}"`),
		).toBeTrue();
	}
	expect(composerSource).toContain('role="combobox"');
	expect(composerSource).toContain('role="listbox"');
	expect(composerSource).toContain('role="option"');
	expect(composerSource).toContain('class="composer composer-shell"');
	expect(composerSource).toContain("mewa(dropdownBehavior)");
	expect(composerSource).toContain("event.isComposing || event.keyCode === 229");
	expect(composerSource).toContain("ACCEPTED_TEXT_ATTACHMENT_EXTENSIONS.map");
	expect(slashSource).toContain('data-testid="slash-command-name"');
	expect(slashSource).toContain("aria-selected={index === activeIndex}");
	expect(imageSource).toContain('testid="chat-attachment-chip"');
	expect(imageSource).toContain('testid="chat-attachment-dialog"');
	expect(imageSource).toContain("onClosedAutoFocus={() => trigger?.focus()}");
	expect(await Bun.file(new URL("composer.tsx", composerRoot)).exists()).toBeFalse();
});
