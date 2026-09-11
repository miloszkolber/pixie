<script lang="ts">
import type { DesignState } from "./design-model";
import { designInspectorModel, designSharedFocusPublish } from "./design-inspector";
import { buildDesignDraftReference, designDraftReferenceAction } from "./design-draft-reference";
import {
	EMPTY_DESIGN_UPLOAD,
	designDocumentMetadata,
	designRemovalConfirmation,
	designUploadStatusLabel,
	formatDesignFileSize,
	type DesignUploadProgress,
} from "./design-upload";

type Props = {
	state: DesignState;
	upload?: DesignUploadProgress;
	selectedPageId?: string | null;
	uploadError?: string | null;
	inspectorError?: string | null;
	onpublish?: (pageId: string | null, nodeId: string | null) => void;
	oninsertreference?: (reference: string) => void;
	onrefresh?: () => void;
	onuploadcancel?: () => void;
	onremove?: () => void;
	class?: string;
};

let {
	state,
	upload = EMPTY_DESIGN_UPLOAD,
	selectedPageId = null,
	uploadError = null,
	inspectorError = null,
	onpublish,
	oninsertreference,
	onrefresh,
	onuploadcancel,
	onremove,
	class: className = "",
}: Props = $props();

let inspector = $derived(designInspectorModel(state, selectedPageId));
let publish = $derived(
	designSharedFocusPublish(state, {
		pageId: inspector.selectedPageId,
		nodeId: state.focus.privateFocus?.nodeId ?? null,
	}),
);
let metadata = $derived(designDocumentMetadata(state.document));
let removal = $derived(designRemovalConfirmation(state));
let draftTarget = $derived(
	state.document
		? {
				documentId: state.document.id,
				documentName: state.document.name,
				...(inspector.selectedPageId ? { pageId: inspector.selectedPageId } : {}),
				...(state.focus.privateFocus?.nodeId
					? { nodeId: state.focus.privateFocus.nodeId }
					: {}),
			}
		: null,
);
let draftAction = $derived(designDraftReferenceAction(state, draftTarget));
let draftReference = $derived(draftTarget ? buildDesignDraftReference(draftTarget) : null);
</script>

<aside
	class={`flex min-w-0 flex-col gap-sm ${className}`}
	data-testid="design-sidebar"
	data-scope="instance"
	aria-label="Design document and focus controls"
>
	<p class="tr-text-eyebrow text-text-muted">Design</p>
	<p class="tr-text-metadata text-text-muted" data-testid="design-scope-notice">
		Instance-wide · every authorized Design reader can inspect this document.
	</p>

	<section aria-label="Source and upload status" class="flex min-w-0 flex-col gap-xs">
		<h3 class="tr-text-metadata text-text-muted">Source</h3>
		{#if metadata}
			<p class="tr-text-metadata text-text-default" data-testid="design-document-metadata">
				{metadata.name} · {metadata.sizeLabel} · {metadata.dateLabel} · {metadata.countsLabel}
			</p>
			<p class="tr-text-metadata text-text-muted">{metadata.scopeNotice}</p>
		{:else}
			<p class="tr-text-metadata text-text-muted" data-testid="design-document-empty">
				No Design document is loaded. Upload a local .fig file; the single instance-wide slot stays
				explained here and in module settings.
			</p>
		{/if}
		<p class="tr-text-metadata text-text-muted" data-testid="design-upload-status">
			{designUploadStatusLabel(upload)}
		</p>
		{#if upload.phase === "uploading"}
			<progress
				class="progress"
				data-testid="design-upload-progress"
				max="100"
				value={upload.percent}
				aria-label={`Uploading ${upload.fileName ?? "design file"} ${upload.percent}%`}
			>
				{upload.percent}%
			</progress>
			<p class="tr-text-metadata text-text-muted" data-testid="design-upload-metadata">
				{upload.fileName ?? "Unknown file"} · {formatDesignFileSize(upload.fileSize)}
			</p>
			<button
				type="button"
				class="btn"
				data-variant="outline"
				data-size="sm"
				data-testid="design-upload-cancel"
				disabled={!upload.cancellable}
				onclick={() => onuploadcancel?.()}
			>
				Cancel upload
			</button>
		{/if}
		{#if uploadError}
			<p role="alert" class="tr-text-metadata text-feedback-error" data-testid="design-upload-error">
				{uploadError}
			</p>
		{/if}
		{#if state.document}
			<p class="tr-text-metadata text-text-muted" data-testid="design-removal-metadata">
				{state.document.name} · {formatDesignFileSize(state.document.sourceBytes)}
			</p>
			<button
				type="button"
				class="btn"
				data-variant="destructive-outline"
				data-size="sm"
				data-testid="design-remove-button"
				disabled={removal.request === null}
				onclick={() => onremove?.()}
			>
				Remove document
			</button>
			<p class="tr-text-metadata text-text-muted" data-testid="design-removal-confirmation">
				{removal.title} {removal.message} {removal.copiesNotice}
			</p>
		{/if}
	</section>

	<section aria-label="Pages, layers, and frame candidates" class="flex min-w-0 flex-col gap-xs">
		<h3 class="tr-text-metadata text-text-muted">Pages · layers · frames</h3>
		{#if inspector.availability.available}
			<ul class="flex min-w-0 flex-col gap-0.5" aria-label="Design pages">
				{#each inspector.pages as page}
					<li
						class="truncate tr-text-metadata text-text-default"
						data-testid={`design-page-row-${page.id}`}
						data-selected={page.selected ? "true" : undefined}
					>
						{page.name} · {page.nodeCount} nodes{page.internal ? " · internal" : ""}
					</li>
				{/each}
			</ul>
			<ul class="flex min-w-0 flex-col gap-0.5" aria-label="Design layers">
				{#each inspector.layers as layer}
					<li
						class="truncate tr-text-metadata text-text-muted"
						data-testid={`design-layer-row-${layer.id}`}
					>
						{layer.name} · {layer.type}{layer.frameCandidate ? " · frame candidate" : ""}{layer.hasText
							? " · text"
							: ""}
					</li>
				{/each}
			</ul>
			<ul class="flex min-w-0 flex-col gap-0.5" aria-label="Design frame candidates">
				{#each inspector.frameCandidates as candidate}
					<li
						class="truncate tr-text-metadata text-text-muted"
						data-testid={`design-frame-candidate-row-${candidate.id}`}
					>
						{candidate.name} · frame candidate
					</li>
				{/each}
			</ul>
			<p class="tr-text-metadata text-text-muted" data-testid="design-inspector-counts">
				{inspector.pageCount} pages · {inspector.nodeCount} nodes · shared focus revision {inspector.sharedRevision}
			</p>
		{:else}
			<p
				role="status"
				class="tr-text-metadata text-text-muted"
				data-testid="design-inspector-unavailable"
			>
				{inspector.availability.reason ?? "Design structure unavailable."}
				{#each inspector.availability.diagnostics as diagnostic}
					<span class="block">{diagnostic}</span>
				{/each}
			</p>
		{/if}
		{#if inspectorError}
			<p role="alert" class="tr-text-metadata text-feedback-error" data-testid="design-inspector-error">
				{inspectorError}
			</p>
		{/if}
	</section>

	<section aria-label="Shared focus" class="flex min-w-0 flex-col gap-xs">
		<h3 class="tr-text-metadata text-text-muted">Shared focus</h3>
		<p class="tr-text-metadata text-text-muted">Private browsing never changes shared focus.</p>
		<button
			type="button"
			class="btn"
			data-variant="outline"
			data-size="sm"
			data-testid="design-shared-focus-publish"
			disabled={!publish.enabled}
			title={publish.reason ?? "Publish the selected page and node as shared focus"}
			onclick={() => onpublish?.(inspector.selectedPageId, state.focus.privateFocus?.nodeId ?? null)}
		>
			{publish.label}
		</button>
		{#if !publish.enabled && publish.reason}
			<p class="tr-text-metadata text-text-muted" data-testid="design-shared-focus-reason">
				{publish.reason}
			</p>
		{/if}
		{#if publish.optimisticRevision !== null}
			<p class="tr-text-metadata text-text-muted" data-testid="design-shared-focus-revision">
				Optimistic revision {publish.optimisticRevision}
			</p>
		{/if}
	</section>

	<section aria-label="Chat reference" class="flex min-w-0 flex-col gap-xs">
		<h3 class="tr-text-metadata text-text-muted">Chat reference</h3>
		{#if draftReference}
			<p class="truncate tr-text-metadata text-text-muted" data-testid="design-draft-reference-preview">
				{draftReference}
			</p>
		{/if}
		<button
			type="button"
			class="btn"
			data-variant="outline"
			data-size="sm"
			data-testid="design-draft-reference-insert"
			disabled={!draftAction.enabled || draftReference === null}
			title={draftAction.hint}
			onclick={() => {
				if (draftReference) oninsertreference?.(draftReference);
			}}
		>
			{draftAction.label}
		</button>
		<p class="tr-text-metadata text-text-muted">{draftAction.hint}</p>
		{#if !draftAction.enabled && draftAction.reason}
			<p class="tr-text-metadata text-text-muted" data-testid="design-draft-reference-reason">
				{draftAction.reason}
			</p>
		{/if}
	</section>

	<button
		type="button"
		class="btn"
		data-variant="outline"
		data-size="sm"
		data-testid="design-refresh-button"
		onclick={() => onrefresh?.()}
	>
		Refresh status
	</button>
</aside>
