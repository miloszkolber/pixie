import {
	TEXT_ATTACHMENT_MAX_BYTES,
	type TextResourceAttachment,
	textAttachmentMediaType,
	validateTextResourceAttachments,
} from "@pixie/contracts";

export async function fileToTextResource(file: File): Promise<TextResourceAttachment> {
	const mimeType = textAttachmentMediaType(file.name);
	if (!mimeType) throw new Error("unsupported text file type");
	if (file.size > TEXT_ATTACHMENT_MAX_BYTES) throw new Error("file exceeds the 1 MiB size limit");
	let text: string;
	try {
		text = new TextDecoder("utf-8", { fatal: true }).decode(await file.arrayBuffer());
	} catch {
		throw new Error("file is not valid UTF-8 text");
	}
	const resource: TextResourceAttachment = { type: "text", name: file.name, mimeType, text };
	validateTextResourceAttachments([resource]);
	return resource;
}
