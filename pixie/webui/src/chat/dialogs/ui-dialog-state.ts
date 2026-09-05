import type { UiDialogRequest } from "@pixie/contracts";

/** The pending generic extension dialog for one session, if any. */
export function uiDialogForSession(
	dialogs: Record<string, UiDialogRequest>,
	sessionId: string,
): UiDialogRequest | undefined {
	return Object.values(dialogs).find((dialog) => dialog.sessionId === sessionId);
}
