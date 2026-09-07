<script lang="ts">
import SourcePreview from "../tabs/source-preview.svelte";
import { simpleUnifiedDiff } from "./line-diff";

interface Props {
	path: string;
	originalPath?: string | undefined;
	original: string;
	modified: string;
	ignoreWhitespace: boolean;
}

let { path, originalPath, original, modified, ignoreWhitespace }: Props = $props();
let content = $derived(simpleUnifiedDiff(path, original, modified, ignoreWhitespace, originalPath));
</script>

<SourcePreview {path} {content} language="diff" testid="source-diff" />
