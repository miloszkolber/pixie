<script lang="ts">
import Icon from "../../components/icon.svelte";
import ImageChip from "../composer/image-chip.svelte";
import type { ChatRow } from "../runtime/rows";
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
</script>

{#if row.kind === "user"}
	<UserTurn id={row.id} message={row.message} imageAttachmentNames={row.imageAttachmentNames} />
{:else if row.kind === "system"}
	<div data-testid="chat-message" data-role="system" class="text-center text-text-muted tr-text-metadata">{row.text}</div>
{:else if row.kind === "error"}
	<div data-testid="chat-message" data-role="error" class="flex items-start gap-sm rounded-[var(--radius-sm)] border border-feedback-error-muted bg-clip-padding bg-feedback-error-subtle px-md py-sm text-feedback-error tr-text-ui">
		<Icon name="triangle-alert" size={16} class="mt-0.5 shrink-0" /><span class="min-w-0 whitespace-pre-wrap break-words">{row.text}</span>
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
	<div data-testid="chat-message" data-role="assistant" class="tr-text-reading text-text-default"><Markdown text={row.text} /></div>
{:else if row.kind === "image"}
	<div data-testid="chat-message" data-role="assistant" class="tr-text-reading text-text-default"><ImageChip label={row.image.mimeType} image={row.image} /></div>
{:else if row.kind === "tool"}
	<ToolRow {row} {projectAreaRoot} />
{:else if row.kind === "activity"}
	<ActivityGroup id={row.id} steps={row.steps} live={row.live} {projectAreaRoot} />
{:else if row.kind === "divider"}
	<TurnDivider id={row.id} data={row.data} {projectAreaRoot} {onOpenChange} />
{/if}
