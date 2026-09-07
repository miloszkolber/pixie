import { expect, test } from "bun:test";
import {
	ACCEPTED_IMAGE_TYPES,
	IMAGE_MAX_BASE64_BYTES,
	modelReferenceKey,
	normalizeModelReferences,
	REQUEST_IMAGE_BASE64_BUDGET,
	REQUEST_TEXT_ATTACHMENT_MAX_BYTES,
	REQUEST_TEXT_ATTACHMENT_MAX_COUNT,
	TEXT_ATTACHMENT_MAX_BYTES,
	textAttachmentMediaType,
	validateRequestImages,
	validateTextResourceAttachments,
} from "../../contracts/src/domain";

test("normalizes model visibility references without provider-specific assumptions", () => {
	const references = normalizeModelReferences([
		{ provider: "openai", id: "gpt" },
		{ provider: "openai", id: "gpt" },
		{ provider: "custom/provider", id: "model:latest" },
		{ provider: "", id: "missing" },
		{ provider: "broken", id: "bad\0id" },
		{ provider: 4, id: "wrong" },
	]);

	expect(references).toEqual([
		{ provider: "openai", id: "gpt" },
		{ provider: "custom/provider", id: "model:latest" },
	]);
	const [first, second] = references;
	if (!first || !second) throw new Error("expected normalized references");
	expect(modelReferenceKey(first)).not.toBe(modelReferenceKey(second));
});

test("validates bounded typed text resource attachments", () => {
	expect(textAttachmentMediaType("review.ts")).toBe("text/x-typescript");
	expect(textAttachmentMediaType("archive.zip")).toBeNull();
	expect(() =>
		validateTextResourceAttachments([
			{ type: "text", name: "review.ts", mimeType: "text/x-typescript", text: "export {}\n" },
		]),
	).not.toThrow();
	expect(() =>
		validateTextResourceAttachments([
			{ type: "text", name: "../secret.ts", mimeType: "text/x-typescript", text: "export {}" },
		]),
	).toThrow("filename");
	expect(() =>
		validateTextResourceAttachments([
			{ type: "text", name: "binary.ts", mimeType: "text/x-typescript", text: "ok\0bad" },
		]),
	).toThrow("valid text");
	expect(() =>
		validateTextResourceAttachments([
			{
				type: "text",
				name: "large.txt",
				mimeType: "text/plain",
				text: "x".repeat(TEXT_ATTACHMENT_MAX_BYTES + 1),
			},
		]),
	).toThrow("1 MiB");
	expect(() =>
		validateTextResourceAttachments(
			Array.from({ length: REQUEST_TEXT_ATTACHMENT_MAX_COUNT + 1 }, (_, index) => ({
				type: "text" as const,
				name: `file-${index}.txt`,
				mimeType: "text/plain" as const,
				text: "x",
			})),
		),
	).toThrow("4 files");
	expect(() =>
		validateTextResourceAttachments([
			{
				type: "text",
				name: "one.txt",
				mimeType: "text/plain",
				text: "x".repeat(Math.floor(REQUEST_TEXT_ATTACHMENT_MAX_BYTES / 3) + 1),
			},
			{
				type: "text",
				name: "two.txt",
				mimeType: "text/plain",
				text: "x".repeat(Math.floor(REQUEST_TEXT_ATTACHMENT_MAX_BYTES / 3) + 1),
			},
			{
				type: "text",
				name: "three.txt",
				mimeType: "text/plain",
				text: "x".repeat(Math.floor(REQUEST_TEXT_ATTACHMENT_MAX_BYTES / 3) + 1),
			},
		]),
	).toThrow("aggregate");
});

test("validates canonical image request blocks and their encoded budgets", () => {
	expect(() =>
		validateRequestImages([{ type: "image", mimeType: "image/png", data: "AA==" }]),
	).not.toThrow();
	expect(() =>
		validateRequestImages([{ type: "image", mimeType: "image/png", data: "AB==" }]),
	).toThrow("canonical base64");
	expect(() =>
		validateRequestImages([{ type: "image", mimeType: "text/plain", data: "AA==" }]),
	).toThrow("Unsupported image media type");
	expect(() =>
		validateRequestImages([
			{
				type: "image",
				mimeType: ACCEPTED_IMAGE_TYPES[0],
				data: "A".repeat(IMAGE_MAX_BASE64_BYTES + 4),
			},
		]),
	).toThrow("4.5 MiB");
	const atLimit = "A".repeat(IMAGE_MAX_BASE64_BYTES);
	expect(() =>
		validateRequestImages(
			Array.from(
				{ length: Math.floor(REQUEST_IMAGE_BASE64_BUDGET / IMAGE_MAX_BASE64_BYTES) + 1 },
				() => ({
					type: "image" as const,
					mimeType: "image/png",
					data: atLimit,
				}),
			),
		),
	).toThrow("24 MiB");
});
