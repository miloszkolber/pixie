import { expect, test } from "bun:test";
import { TEXT_ATTACHMENT_MAX_BYTES } from "@pixie/contracts";
import { fileToTextResource } from "@/chat/composer/text-attachment";

test("reads supported browser text files as typed resources", async () => {
	await expect(
		fileToTextResource(new File(["export const review = true;\n"], "review.ts")),
	).resolves.toEqual({
		type: "text",
		name: "review.ts",
		mimeType: "text/x-typescript",
		text: "export const review = true;\n",
	});
});

test("rejects binary, unknown, and oversized browser files", async () => {
	await expect(
		fileToTextResource(new File([new Uint8Array([0xff, 0xfe])], "binary.ts")),
	).rejects.toThrow("valid UTF-8");
	await expect(fileToTextResource(new File(["data"], "archive.zip"))).rejects.toThrow(
		"unsupported text file type",
	);
	await expect(
		fileToTextResource(new File([new Uint8Array(TEXT_ATTACHMENT_MAX_BYTES + 1)], "large.txt")),
	).rejects.toThrow("1 MiB");
});
