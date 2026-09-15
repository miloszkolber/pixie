<script lang="ts">
import Icon from "../../components/icon.svelte";
import ImageChip from "../composer/image-chip.svelte";
import type { ChatRow } from "../runtime/rows";
import { getSessionBranchContext } from "../session/session-branch-context";
import { messageEntryId } from "../session/session-lifecycle";
import "../tools/register";
import ActivityGroup from "./activity-group.svelte";
import CompactionNotice from "./compaction-notice.svelte";
import CompactionTurn from "./compaction-turn.svelte";
import Markdown from "./markdown.svelte";
import RetryIndicator from "./retry-indicator.svelte";
import ToolRow from "./tool-row.svelte";
import TurnDivider from "./turn-divider.svelte";
import UserTurn from "./user-turn.svelte";

interface Props {
	row: ChatRow;
	projectAreaRoot?: string | undefined;
	onOpenChange: (path: string) => void;
}
let { row, projectAreaRoot, onOpenChange }: Props = $props();
// AUX-14: the work area owns the branch action; the transcript supplies the
// selected native entry so "Edit from here" branches the session in-file.
const branch = getSessionBranchContext();
</script>

{#if row.kind === "user"}
	{@const entryId = messageEntryId(row.message)}
	<UserTurn id={row.id} message={row.message} imageAttachmentNames={row.imageAttachmentNames} />
	{#if branch && entryId}
		<div class="turn-edit-row">
			<button
				type="button"
				data-testid="turn-edit-from-here"
				title="Edit from here — branches this chat"
				onclick={() => branch.editFromHere(entryId)}
				class="turn-edit-from-here u-flex u-items-center u-gap-2xs u-rounded u-px-xs"
			>
				<Icon name="git-branch" size={12} /> Edit from here
			</button>
		</div>
	{/if}
{:else if row.kind === "system"}
	<div data-testid="chat-message" data-role="system" class="u-text-center u-text-text-muted tr-text-metadata">{row.text}</div>
{:else if row.kind === "error"}
	<div data-testid="chat-message" data-role="error" class="u-flex u-items-start u-gap-sm chat-error tr-text-ui">
		<Icon name="triangle-alert" size={16} class="chat-error-icon" /><span class="u-min-w-0 chat-pre-wrap u-break-words">{row.text}</span>
	</div>
{:else if row.kind === "compaction"}
	{#if row.summary !== undefined && row.tokensBefore !== undefined}
		<CompactionTurn id={row.id} summary={row.summary} tokensBefore={row.tokensBefore} />
	{:else}
		<CompactionNotice {...row} />
	{/if}
{:else if row.kind === "retry"}
	<RetryIndicator source={row.source} attempt={row.attempt} maxAttempts={row.maxAttempts} delayMs={row.delayMs} />
{:else if row.kind === "markdown"}
	<div data-testid="chat-message" data-role="assistant" class="tr-text-reading u-text-text-default"><Markdown text={row.text} /></div>
{:else if row.kind === "image"}
	<div data-testid="chat-message" data-role="assistant" class="tr-text-reading u-text-text-default"><ImageChip label={row.image.mimeType} image={row.image} /></div>
{:else if row.kind === "tool"}
	<ToolRow {row} {projectAreaRoot} />
{:else if row.kind === "activity"}
	<ActivityGroup id={row.id} steps={row.steps} live={row.live} {projectAreaRoot} />
{:else if row.kind === "divider"}
	<TurnDivider id={row.id} data={row.data} {projectAreaRoot} {onOpenChange} />
{/if}

<style>
	.chat-error { border: 1px solid var(--feedback-error-muted); border-radius: var(--radius-sm); background: var(--feedback-error-subtle); background-clip: padding-box; padding: var(--space-sm) var(--space-md); color: var(--feedback-error); }
	:global(.chat-error-icon) { margin-top: var(--space-2xs); flex-shrink: 0; }
	.chat-pre-wrap { white-space: pre-wrap; }
	.turn-edit-from-here { border: 0; background: transparent; color: var(--primary); }
	.turn-edit-from-here:hover { background: var(--control-bg-hovered); }
	.turn-edit-row { display: flex; justify-content: flex-end; }
</style>
