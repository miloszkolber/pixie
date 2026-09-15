<script lang="ts">
import type { UserMessage } from "@pixie/shared";
import ChatTurnView from "@/chat/render/turns.svelte";
import type { ChatRow } from "@/chat/runtime/rows";
import { setSessionBranchContext } from "@/chat/session/session-branch-context";

interface Props {
	entryId?: string | undefined;
}
let { entryId }: Props = $props();
setSessionBranchContext({ editFromHere: () => {} });
const message: UserMessage & { entryId?: string } = {
	role: "user",
	content: "hello",
	...(entryId ? { entryId } : {}),
};
const row: ChatRow = { kind: "user", id: "probe-turn", message };
</script>

<ChatTurnView {row} onOpenChange={() => {}} />
