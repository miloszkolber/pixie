export type FilePreviewKind = "image" | "binary" | "markdown" | "source";

export const BINARY_FILE_NOTICE = "Binary file — text preview is unavailable.";

export function filePreviewKind(path: string, content: string): FilePreviewKind {
	if (isImagePath(path)) return "image";
	if (content.includes("\0")) return "binary";
	if (isMarkdownPath(path)) return "markdown";
	return "source";
}

import { isMarkdownPath } from "../../lib/utils";
import { isImagePath } from "../tree/file-kind";
