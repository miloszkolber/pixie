import type { DiffTab } from "../../store";

export function diffIsUnavailable(tab: DiffTab): boolean {
	return Boolean(tab.unavailable || tab.binary || tab.tooLarge);
}

export function diffUnavailableNotice(tab: DiffTab): string {
	return (
		tab.message ||
		(tab.binary
			? "Binary files cannot be previewed"
			: tab.tooLarge
				? "File is too large to preview"
				: "File is unavailable for preview")
	);
}
