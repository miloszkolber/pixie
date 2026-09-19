<script lang="ts">
import type { ToolRenderProps } from "../render/tool-registry";
import ResolvedRecord from "./ask-user-question-record.svelte";
import { parseQuestions, readAskResult } from "./ask-user-question-state";
import { resultText } from "./tool-helpers";

let { args, result, status }: ToolRenderProps = $props();
let questions = $derived(parseQuestions(args));
let resolved = $derived(readAskResult(result));
</script>

<!-- Transcript results are recaps, never an authority for pending interaction. -->
<ResolvedRecord {questions} result={resolved} rawText={resultText(result) || (status === "running" ? "Question in progress. Any active request appears in the session dialog." : "Question closed.")} />
