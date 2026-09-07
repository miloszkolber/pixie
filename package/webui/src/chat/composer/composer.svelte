<script module lang="ts">
export type {
	ComposerHandle,
	ComposerProps,
	MentionCandidate,
	SubmitBehavior,
} from "./composer-state";
</script>

<script lang="ts">
	import {
		ACCEPTED_IMAGE_TYPES,
		ACCEPTED_TEXT_ATTACHMENT_EXTENSIONS,
		REQUEST_IMAGE_BASE64_BUDGET,
		utf8ByteLength,
		validateTextResourceAttachments,
	} from "@pixie/contracts";
	import { tick, untrack } from "svelte";
	import { mewa } from "../../../vendor/mewa-svelte/index.js";
	import { behavior as dropdownBehavior } from "../../../vendor/mewa-ui/components/dropdown-menu.js";
	import Button from "../../components/button.svelte";
	import Icon from "../../components/icon.svelte";
	import { randomId } from "../../lib";
	import type { ChatAttachment } from "../runtime/types";
	import {
		activeToken,
		agentMentionLabel,
		agentMentionSummary,
		clampedMentionActiveIndex,
		composerEnterBehavior,
		imageAttachmentTag,
		insertedMention,
		insertImageTags,
		mentionCompletionKeyAction,
		removeImageTags,
		reserveClipboardImageNames,
		streamingSendModes,
		streamingSubmitBehavior,
		type ComposerProps,
		type MentionCandidate,
		type SubmitBehavior,
	} from "./composer-state";
	import FileChip from "./file-chip.svelte";
	import { type AttachedImage, fileToAttachedImage } from "./image-attachment";
	import {
		clampedSlashActiveIndex,
		matchSlashCommands,
		selectedSlashCommandValue,
		slashCommandQuery,
		slashCommandResetSignal,
		slashCompletionKeyAction,
	} from "./slash-command-completion";
	import SlashCommandMenu from "./slash-command-menu.svelte";
	import { fileToTextResource } from "./text-attachment";

	interface PendingImage extends AttachedImage {
		id: string;
		name: string;
		tag?: string;
	}

	interface PendingText {
		id: string;
		name: string;
		content: Extract<ChatAttachment, { kind: "text" }>["content"];
	}

	interface AttachError {
		id: string;
		name: string;
		reason: string;
	}

	let {
		value,
		onChange,
		isStreaming,
		commands,
		mentionCandidates,
		recentPrompts,
		onMentionQuery,
		onSubmit,
		onAbort,
		onHistoryOpen,
		supportsImages = true,
		supportsTextResources = true,
		supportsSteer = true,
	}: ComposerProps = $props();

	let textarea = $state<HTMLTextAreaElement>();
	let fileInput = $state<HTMLInputElement>();
	let sendMenu = $state<HTMLElement>();
	let caret = $state(untrack(() => value.length));
	let images = $state<PendingImage[]>([]);
	let texts = $state<PendingText[]>([]);
	let pendingAttachments = $state(0);
	let attachErrors = $state<AttachError[]>([]);
	let mentionActiveIndex = $state(0);
	let mentionDismissed = $state(false);
	let slashActiveIndex = $state(0);
	let slashDismissed = $state(false);
	let recalledPromptIndex: number | null = null;
	let draftSnapshot = untrack(() => value);
	let reservedImageNames: string[] = [];
	let selectionRevision = 0;
	let previousMentionQuery: string | null | undefined;
	let previousSlashSignal = "";
	let previousSupportsImages = untrack(() => supportsImages);
	let previousSupportsTextResources = untrack(() => supportsTextResources);

	const componentId = $props.id();
	const mentionListboxId = `composer-mentions-${componentId}`;
	const slashListboxId = `composer-slash-${componentId}`;
	const sendMenuId = `composer-send-${componentId}`;
	const inputId = `composer-input-${componentId}`;

	let tokenState = $derived(activeToken(value, caret));
	let mentionQuery = $derived(tokenState.token.startsWith("@") ? tokenState.token.slice(1) : null);
	let mentionOpen = $derived(
		!mentionDismissed && mentionQuery !== null && mentionCandidates.length > 0,
	);
	let visibleMentionActiveIndex = $derived(
		clampedMentionActiveIndex(mentionActiveIndex, mentionCandidates.length),
	);
	let slashQuery = $derived(slashCommandQuery(value));
	let slashMatches = $derived(matchSlashCommands(value, commands));
	let slashSignal = $derived(slashCommandResetSignal(value, commands));
	let slashOpen = $derived(!slashDismissed && slashQuery !== null && slashMatches.length > 0);
	let visibleSlashActiveIndex = $derived(
		clampedSlashActiveIndex(slashActiveIndex, slashMatches.length),
	);
	let completionOpen = $derived(mentionOpen || slashOpen);
	let completionListboxId = $derived(mentionOpen ? mentionListboxId : slashListboxId);
	let activeCompletionId = $derived(
		mentionOpen
			? `${mentionListboxId}-option-${visibleMentionActiveIndex}`
			: slashOpen
				? `${slashListboxId}-option-${visibleSlashActiveIndex}`
				: undefined,
	);
	let imagePromptsEnabled = $derived(supportsImages !== false);
	let textResourcesEnabled = $derived(supportsTextResources !== false);
	let attachmentPromptsEnabled = $derived(imagePromptsEnabled || textResourcesEnabled);
	let acceptedAttachments = $derived(
		[
			...ACCEPTED_IMAGE_TYPES,
			...ACCEPTED_TEXT_ATTACHMENT_EXTENSIONS.map((extension) => `.${extension}`),
		].join(","),
	);
	let placeholder = $derived(
		isStreaming
			? supportsSteer
				? "Enter steers at the next step · Cmd/Ctrl+Enter queues for when it finishes"
				: "Enter queues a follow-up for when the agent finishes"
			: "Message the agent…  (@ files · / commands · Enter to send)",
	);

	$effect(() => {
		draftSnapshot = value;
	});

	$effect(() => {
		const query = mentionQuery;
		onMentionQuery(query);
		if (query !== previousMentionQuery) {
			previousMentionQuery = query;
			mentionActiveIndex = 0;
			mentionDismissed = false;
		}
	});

	$effect(() => {
		const signal = slashSignal;
		if (signal !== previousSlashSignal) {
			previousSlashSignal = signal;
			slashActiveIndex = 0;
			slashDismissed = false;
		}
	});

	$effect(() => {
		const support = supportsImages;
		if (support === false && previousSupportsImages !== false && images.length > 0) {
			images = [];
			attachErrors = [
				{
					id: randomId(),
					name: "images",
					reason: "connected agent does not support image prompts",
				},
			];
		}
		previousSupportsImages = support;
	});

	$effect(() => {
		const support = supportsTextResources;
		if (support === false && previousSupportsTextResources !== false && texts.length > 0) {
			texts = [];
			attachErrors = [
				{
					id: randomId(),
					name: "text files",
					reason: "connected agent does not support text resource prompts",
				},
			];
		}
		previousSupportsTextResources = support;
	});

	function commitImages(next: PendingImage[]): void {
		images = next;
	}

	function commitTexts(next: PendingText[]): void {
		texts = next;
	}

	function focusSelection(start: number, end = start): void {
		caret = start;
		const revision = ++selectionRevision;
		void tick().then(() => {
			if (revision !== selectionRevision || !textarea) return;
			textarea.focus();
			textarea.setSelectionRange(start, end);
			caret = textarea.selectionStart;
		});
	}

	function replaceDraft(text: string, nextCaret = text.length): void {
		recalledPromptIndex = null;
		draftSnapshot = text;
		onChange(text);
		focusSelection(nextCaret);
	}

	function canSubmit(raw: string): boolean {
		return (
			pendingAttachments === 0 &&
			(!!raw.trim() ||
				(imagePromptsEnabled && images.length > 0) ||
				(textResourcesEnabled && texts.length > 0))
		);
	}

	function submitText(raw: string, behavior: SubmitBehavior): void {
		if (!canSubmit(raw)) return;
		const accepted = onSubmit(
			raw.trim(),
			[
				...(imagePromptsEnabled
					? images.map(({ name, content }) => ({ kind: "image" as const, name, content }))
					: []),
				...(textResourcesEnabled
					? texts.map(({ name, content }) => ({ kind: "text" as const, name, content }))
					: []),
			],
			behavior,
		);
		if (accepted === false) return;
		draftSnapshot = "";
		caret = 0;
		onChange("");
		commitImages([]);
		commitTexts([]);
		attachErrors = [];
		recalledPromptIndex = null;
	}

	function pickMention(candidate: MentionCandidate): void {
		const before = value.slice(0, tokenState.start);
		const after = value.slice(caret);
		const insert = insertedMention(candidate);
		const suffix = candidate.kind === "dir" ? "" : " ";
		replaceDraft(
			`${before}${insert}${suffix}${after}`,
			before.length + insert.length + suffix.length,
		);
	}

	function dismissSlashCompletion(): void {
		slashDismissed = true;
	}

	function pickSlashCommand(command: Parameters<typeof selectedSlashCommandValue>[0]): void {
		replaceDraft(selectedSlashCommandValue(command));
		dismissSlashCompletion();
	}

	export function openHistory(): void {
		mentionDismissed = true;
		dismissSlashCompletion();
		onHistoryOpen?.();
	}

	export function insertText(text: string): void {
		replaceDraft(text);
	}

	export function insertAndSubmit(text: string, behavior: SubmitBehavior): void {
		if (canSubmit(text)) submitText(text, behavior);
		else replaceDraft(text);
	}

	export function refocus(): void {
		focusSelection(caret);
	}

	async function addFiles(files: File[], pastedImageNames?: readonly string[]): Promise<void> {
		const imageFiles = files.filter((file) => file.type.startsWith("image/"));
		const textFiles = files.filter(
			(file) => !file.type.startsWith("image/") && file.name.includes("."),
		);
		const unsupported = files.filter(
			(file) => !imageFiles.includes(file) && !textFiles.includes(file),
		);
		if (files.length === 0) return;

		pendingAttachments += files.length;
		try {
			const [imageResults, textResults] = await Promise.all([
				Promise.allSettled(imageFiles.map(fileToAttachedImage)),
				Promise.allSettled(textFiles.map(fileToTextResource)),
			]);
			let used = images.reduce((sum, pending) => sum + pending.content.data.length, 0);
			const imageAdditions: PendingImage[] = [];
			const textAdditions: PendingText[] = [];
			const errors: AttachError[] = [];
			const failedImageNames: string[] = [];

			imageResults.forEach((result, index) => {
				const file = imageFiles[index];
				const name = pastedImageNames?.[index] ?? file?.name ?? "image";
				if (supportsImages === false) {
					errors.push({
						id: randomId(),
						name,
						reason: "connected agent does not support image prompts",
					});
					if (pastedImageNames) failedImageNames.push(name);
					return;
				}
				if (result.status !== "fulfilled" || result.value === null) {
					errors.push({ id: randomId(), name, reason: "unsupported image format" });
					if (pastedImageNames) failedImageNames.push(name);
					return;
				}
				const size = result.value.content.data.length;
				if (used + size > REQUEST_IMAGE_BASE64_BUDGET) {
					errors.push({ id: randomId(), name, reason: "message image limit reached" });
					if (pastedImageNames) failedImageNames.push(name);
					return;
				}
				used += size;
				imageAdditions.push({
					id: randomId(),
					name,
					...(pastedImageNames ? { tag: imageAttachmentTag(name) } : {}),
					...result.value,
				});
			});

			textResults.forEach((result, index) => {
				const name = textFiles[index]?.name ?? "file";
				if (supportsTextResources === false) {
					errors.push({
						id: randomId(),
						name,
						reason: "connected agent does not support text resource prompts",
					});
					return;
				}
				if (result.status !== "fulfilled") {
					errors.push({
						id: randomId(),
						name,
						reason:
							result.reason instanceof Error
								? result.reason.message
								: "unsupported text file",
					});
					return;
				}
				try {
					validateTextResourceAttachments([
						...texts.map((attachment) => attachment.content),
						...textAdditions.map((attachment) => attachment.content),
						result.value,
					]);
				} catch (error) {
					errors.push({
						id: randomId(),
						name,
						reason:
							error instanceof Error
								? error.message
								: "message text attachment limit reached",
					});
					return;
				}
				textAdditions.push({ id: randomId(), name, content: result.value });
			});

			unsupported.forEach((file) => {
				errors.push({
					id: randomId(),
					name: file.name || "file",
					reason: "supported image or text file required",
				});
			});

			if (supportsImages !== false && imageAdditions.length > 0) {
				commitImages([...images, ...imageAdditions]);
			}
			if (failedImageNames.length > 0) {
				const removal = removeImageTags(draftSnapshot, caret, failedImageNames);
				replaceDraft(removal.value, removal.caret);
			}
			if (supportsTextResources !== false && textAdditions.length > 0) {
				commitTexts([...texts, ...textAdditions]);
			}
			if (errors.length > 0) attachErrors = [...attachErrors, ...errors];
		} finally {
			if (pastedImageNames) {
				reservedImageNames = reservedImageNames.filter(
					(name) => !pastedImageNames.includes(name),
				);
			}
			pendingAttachments -= files.length;
		}
	}

	function removeImage(image: PendingImage): void {
		commitImages(images.filter((current) => current.id !== image.id));
		if (!image.tag) return;
		const removal = removeImageTags(draftSnapshot, caret, [image.name]);
		if (removal.value !== draftSnapshot) replaceDraft(removal.value, removal.caret);
	}

	function handleSlashKeyDown(event: KeyboardEvent): boolean {
		const action = slashCompletionKeyAction(
			event.key,
			slashOpen,
			visibleSlashActiveIndex,
			slashMatches.length,
		);
		if (action.type === "none") return false;
		event.preventDefault();
		event.stopPropagation();
		if (action.type === "move") slashActiveIndex = action.index;
		if (action.type === "dismiss") dismissSlashCompletion();
		if (action.type === "select") {
			const command = slashMatches[action.index];
			if (command) pickSlashCommand(command);
		}
		return true;
	}

	function handleKeyDown(event: KeyboardEvent): void {
		if (event.isComposing || event.keyCode === 229) return;
		if (mentionOpen) {
			const action = mentionCompletionKeyAction(
				event.key,
				mentionOpen,
				visibleMentionActiveIndex,
				mentionCandidates.length,
			);
			if (action.type !== "none") {
				event.preventDefault();
				event.stopPropagation();
				if (action.type === "move") mentionActiveIndex = action.index;
				if (action.type === "dismiss") mentionDismissed = true;
				if (action.type === "select") {
					const candidate = mentionCandidates[action.index];
					if (candidate) pickMention(candidate);
				}
				return;
			}
		}
		if (handleSlashKeyDown(event)) return;

		const recallAt = recalledPromptIndex;
		if (event.key === "ArrowUp" && (value === "" || recallAt !== null) && recentPrompts.length > 0) {
			event.preventDefault();
			const next = recallAt === null ? 0 : Math.min(recallAt + 1, recentPrompts.length - 1);
			const text = recentPrompts[next] ?? "";
			recalledPromptIndex = next;
			draftSnapshot = text;
			onChange(text);
			focusSelection(text.length);
			return;
		}
		if (event.key === "ArrowDown" && recallAt !== null) {
			event.preventDefault();
			if (recallAt === 0) {
				recalledPromptIndex = null;
				draftSnapshot = "";
				onChange("");
				focusSelection(0);
			} else {
				const next = recallAt - 1;
				const text = recentPrompts[next] ?? "";
				recalledPromptIndex = next;
				draftSnapshot = text;
				onChange(text);
				focusSelection(text.length);
			}
			return;
		}

		const behavior = composerEnterBehavior(event, isStreaming, supportsSteer);
		if (behavior) {
			event.preventDefault();
			submitText(value, behavior);
		}
	}

	function updateCaret(event: Event): void {
		const target = event.currentTarget;
		if (target instanceof HTMLTextAreaElement) caret = target.selectionStart;
	}

	function handleInput(event: Event): void {
		const target = event.currentTarget;
		if (!(target instanceof HTMLTextAreaElement)) return;
		const next = target.value;
		if (recalledPromptIndex !== null && next !== recentPrompts[recalledPromptIndex]) {
			recalledPromptIndex = null;
		}
		draftSnapshot = next;
		onChange(next);
		caret = target.selectionStart;
	}

	function handlePaste(event: ClipboardEvent): void {
		const files = Array.from(event.clipboardData?.files ?? []);
		const imageFiles = files.filter((file) => file.type.startsWith("image/"));
		if (imageFiles.length === 0) return;
		event.preventDefault();
		if (!attachmentPromptsEnabled) {
			attachErrors = [
				{
					id: randomId(),
					name: "clipboard image",
					reason: "connected agent does not support file attachments",
				},
			];
			return;
		}

		const target = event.currentTarget;
		const eventValue = target instanceof HTMLTextAreaElement ? target.value : draftSnapshot;
		const selectionStart =
			target instanceof HTMLTextAreaElement && eventValue === draftSnapshot
				? target.selectionStart
				: caret;
		const selectionEnd =
			target instanceof HTMLTextAreaElement && eventValue === draftSnapshot
				? target.selectionEnd
				: caret;
		const names = reserveClipboardImageNames(
			imageFiles,
			[
				...images.map((image) => image.name),
				...texts.map((attachment) => attachment.name),
				...reservedImageNames,
			],
			draftSnapshot,
		);
		reservedImageNames = [...reservedImageNames, ...names];
		const insertion = insertImageTags(draftSnapshot, selectionStart, selectionEnd, names);
		replaceDraft(insertion.value, insertion.caret);
		void addFiles(files, names);
	}

	function handleDrop(event: DragEvent): void {
		const files = Array.from(event.dataTransfer?.files ?? []);
		if (files.length === 0) return;
		event.preventDefault();
		if (attachmentPromptsEnabled) {
			void addFiles(files);
			return;
		}
		attachErrors = [
			{
				id: randomId(),
				name: "dropped image",
				reason: "connected agent does not support file attachments",
			},
		];
	}
</script>

<div
	class="relative flex shrink-0 flex-col bg-container-project-bg"
	data-image-prompts={supportsImages === null ? "unknown" : supportsImages}
	data-text-resource-prompts={supportsTextResources === null ? "unknown" : supportsTextResources}
>
	{#if mentionOpen}
		<div
			id={mentionListboxId}
			role="listbox"
			data-testid="mention-menu"
			class="absolute bottom-full left-sm z-10 mb-xs max-h-[40vh] w-[min(28rem,90%)] overflow-y-auto rounded-[var(--radius-md)] border border-border-default bg-container-elevated-bg p-xs shadow-[var(--shadow-md)]"
		>
			{#each mentionCandidates as candidate, index (candidate.kind === "agent" ? candidate.mention : candidate.path)}
				<button
					id={`${mentionListboxId}-option-${index}`}
					role="option"
					aria-selected={index === visibleMentionActiveIndex}
					type="button"
					data-testid="mention-item"
					aria-label={candidate.kind === "agent" ? agentMentionLabel(candidate) : undefined}
					onclick={() => pickMention(candidate)}
					class={`flex w-full items-center gap-sm rounded-[var(--radius-sm)] px-sm py-xs text-left tr-text-ui ${
						index === visibleMentionActiveIndex
							? "bg-control-bg-selected text-text-default"
							: "text-text-muted"
					}`}
				>
					{#if candidate.kind === "agent"}
						<Icon
							name={candidate.sourceType === "skill" || candidate.sourceType === "builtinSkill"
								? "wrench"
								: candidate.sourceType === "project"
										? "puzzle"
										: "bot"}
							size={14}
							class="shrink-0"
						/>
					{:else}
						<Icon name={candidate.kind === "dir" ? "folder" : "file"} size={14} class="shrink-0" />
					{/if}
					{#if candidate.kind === "agent"}
						<span class="min-w-0">
							<span class="block truncate">{candidate.name}</span>
							<span class="block truncate text-text-muted tr-text-metadata">
								{agentMentionSummary(candidate)}
							</span>
						</span>
					{:else}
						<span class="truncate">{candidate.path}</span>
					{/if}
				</button>
			{/each}
		</div>
	{:else if slashOpen}
		<SlashCommandMenu
			commands={slashMatches}
			activeIndex={visibleSlashActiveIndex}
			onSelect={pickSlashCommand}
			class="absolute bottom-full left-sm z-10 mb-xs"
			listboxId={slashListboxId}
		/>
	{/if}

	{#if (imagePromptsEnabled && images.length > 0) || (textResourcesEnabled && texts.length > 0) || pendingAttachments > 0 || attachErrors.length > 0}
		<div class="flex flex-wrap gap-xs px-sm pt-sm" data-testid="composer-attachments">
			{#each attachErrors as error (error.id)}
				<FileChip
					testid="composer-image-error"
					tone="error"
					icon={false}
					title={`Couldn't attach ${error.name} — ${error.reason}`}
					label={`Couldn't attach ${error.name}`}
					meta={`— ${error.reason}`}
					removeLabel="Dismiss"
					onRemove={() => (attachErrors = attachErrors.filter((item) => item.id !== error.id))}
				/>
			{/each}
			{#each images as image (image.id)}
				<FileChip
					testid="composer-image"
					width={image.width}
					height={image.height}
					mime={image.content.mimeType}
					title={image.name}
					label={image.name}
					meta={image.width && image.height ? ` · ${image.width}×${image.height}` : undefined}
					removeLabel="Remove image"
					onRemove={() => removeImage(image)}
				/>
			{/each}
			{#each texts as attachment (attachment.id)}
				<FileChip
					testid="composer-text-attachment"
					title={attachment.name}
					label={attachment.name}
					meta={` · ${utf8ByteLength(attachment.content.text).toLocaleString()} bytes`}
					removeLabel="Remove file"
					onRemove={() =>
					commitTexts(texts.filter((item) => item.id !== attachment.id))}
				/>
			{/each}
			{#if pendingAttachments > 0}
				<FileChip
					testid="composer-attachment-pending"
					label={pendingAttachments === 1 ? "Attaching…" : `Attaching ${pendingAttachments}…`}
				/>
			{/if}
		</div>
	{/if}

	<form
		class="composer composer-shell"
		data-state={isStreaming ? "thinking" : undefined}
		onsubmit={(event) => {
			event.preventDefault();
			submitText(value, isStreaming ? streamingSubmitBehavior(supportsSteer) : "send");
		}}
	>
		<label class="composer-label" for={inputId}>Message</label>
		<textarea
			bind:this={textarea}
			id={inputId}
			data-testid="chat-input"
			aria-autocomplete="list"
			aria-controls={completionOpen ? completionListboxId : undefined}
			aria-activedescendant={activeCompletionId}
			{value}
			oninput={handleInput}
			onkeyup={updateCaret}
			onclick={updateCaret}
			onselect={updateCaret}
			onkeydown={handleKeyDown}
			onpaste={handlePaste}
			ondrop={handleDrop}
			rows={4}
			{placeholder}
			class="composer-input"
		></textarea>

		<div class="composer-actions">
			<div class="composer-actions-leading"></div>
			<div class="composer-actions-trailing">
				<input
					bind:this={fileInput}
					type="file"
					accept={acceptedAttachments}
					multiple
					tabindex="-1"
					aria-hidden="true"
					class="sr-only"
					onchange={(event) => {
						const files = Array.from(event.currentTarget.files ?? []);
						event.currentTarget.value = "";
						void addFiles(files);
					}}
				/>
				<Button
					variant="outline"
					size="icon-sm"
					data-testid="file-attach"
					aria-label="Attach files or images"
					title="Attach files or images"
					disabled={!attachmentPromptsEnabled}
					onclick={() => fileInput?.click()}
				>
					<Icon name="paperclip" size={14} />
				</Button>
				<Button
					variant="outline"
					size="icon-sm"
					data-testid="history-open"
					aria-label="Search history"
					onclick={openHistory}
				>
					<Icon name="clock-arrow-left" size={14} />
				</Button>
				{#if isStreaming}
					<Button
						variant="outline"
						size="icon-sm"
						data-testid="chat-abort"
						aria-label="Stop"
						onclick={onAbort}
					>
						<Icon name="square" size={14} />
					</Button>
					<span class="contents" {@attach mewa(dropdownBehavior)}>
						<Button
							variant="outline"
							size="icon-sm"
							data-testid="send-menu"
							data-dropdown-menu-trigger={sendMenuId}
							aria-haspopup="menu"
							aria-controls={sendMenuId}
							aria-expanded="false"
							aria-label="Send options"
						>
							<Icon name="chevron-up" size={14} />
						</Button>
						<div
							bind:this={sendMenu}
							id={sendMenuId}
							popover="auto"
							role="menu"
							class="dropdown-menu-content send-menu"
							data-align="end"
						>
							{#each streamingSendModes(supportsSteer) as mode (mode.behavior)}
								<button
									type="button"
									role="menuitem"
									data-testid={mode.testid}
									disabled={!canSubmit(value)}
									onclick={() => {
										sendMenu?.hidePopover();
										submitText(value, mode.behavior);
									}}
									class="dropdown-menu-item flex-col items-stretch gap-2xs"
								>
									<span class="flex w-full items-baseline justify-between gap-sm">
										<span class="text-text-default tr-text-ui">{mode.name}</span>
										<span class="shrink-0 text-text-muted tr-text-metadata">{mode.keys}</span>
									</span>
									<span class="text-text-muted tr-text-metadata">{mode.meaning}</span>
								</button>
							{/each}
						</div>
					</span>
				{/if}
				<Button
					size="icon-sm"
					data-testid="chat-send"
					aria-label={isStreaming ? (supportsSteer ? "Steer" : "Queue follow-up") : "Send"}
					disabled={!canSubmit(value)}
					onclick={() =>
						submitText(value, isStreaming ? streamingSubmitBehavior(supportsSteer) : "send")}
				>
					<Icon name="arrow-up" size={16} />
				</Button>
			</div>
		</div>
	</form>
</div>

<style>
    .composer-input { min-block-size: 108px; }
    @media (max-height: 600px) {
        .composer-shell { padding-block: 4px; gap: 4px; }
        .composer-input { min-block-size: 36px; max-block-size: 72px; }
        .composer-actions { margin-block-start: 0; gap: 8px; }
    }
    @media (pointer: coarse) {
        .composer-actions :global(button) { min-width: 44px; min-height: 44px; }
    }
	.composer-shell {
		border-inline: 0;
		border-block-end: 0;
	}

	.send-menu {
		left: auto;
		right: anchor(right);
		bottom: anchor(top);
		top: auto;
		width: min(20rem, calc(100vw - 2rem));
		margin-bottom: var(--space-100);
	}
</style>
