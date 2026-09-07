import type { StopReason } from "@pixie/contracts";

interface AssistantTerminal {
	stopReason?: StopReason;
	errorMessage?: string;
}

export function terminalOutcome(
	terminal: AssistantTerminal | null | undefined,
): { text: string; failed: boolean } | null {
	if (!terminal?.stopReason) return null;
	switch (terminal.stopReason) {
		case "end_turn":
		case "complete":
		case "completed":
		case "stop":
			return { text: "✓ Done", failed: false };
		case "cancelled":
		case "canceled":
		case "aborted":
			return { text: "Stopped", failed: false };
		case "length":
		case "max_tokens":
			return { text: "Response reached the token limit. Ask the agent to continue.", failed: true };
		case "max_turn_requests":
			return { text: "Run reached the request limit. Ask the agent to continue.", failed: true };
		case "refusal":
			return { text: "The agent declined this request.", failed: true };
		case "error":
			return { text: terminal.errorMessage || "The agent run ended in an error.", failed: true };
		default:
			return { text: `Run ended (${terminal.stopReason}).`, failed: false };
	}
}

export function assistantFailureText(
	terminal: AssistantTerminal | null | undefined,
): string | null {
	const outcome = terminalOutcome(terminal);
	return outcome?.failed ? outcome.text : null;
}
