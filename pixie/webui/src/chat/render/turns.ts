import type { ImageContent, UserMessage } from "@pixie/contracts";

export interface UserImageAttachment {
	key: string;
	label: string;
	image: ImageContent;
}

export interface UserResourceMarker {
	key: string;
	name: string;
	mimeType: string;
}

export function userImageAttachments(
	content: UserMessage["content"],
	names?: string[],
): UserImageAttachment[] {
	if (typeof content === "string") return [];
	const seen = new Map<string, number>();
	return content
		.filter((block): block is ImageContent => block.type === "image")
		.map((image, index) => {
			const tail = image.data.slice(-24);
			const occurrence = seen.get(tail) ?? 0;
			seen.set(tail, occurrence + 1);
			return { key: `${tail}-${occurrence}`, label: names?.[index] ?? image.mimeType, image };
		});
}

export function userResourceMarkers(content: UserMessage["content"]): UserResourceMarker[] {
	if (typeof content === "string") return [];
	const seen = new Map<string, number>();
	return content
		.filter((block) => block.type === "resource")
		.map((resource) => {
			const identity = `${resource.name}\0${resource.mimeType}`;
			const occurrence = seen.get(identity) ?? 0;
			seen.set(identity, occurrence + 1);
			return {
				key: `${identity}\0${occurrence}`,
				name: resource.name,
				mimeType: resource.mimeType,
			};
		});
}

export function formatElapsed(ms: number): string {
	const totalSeconds = Math.round(ms / 1000);
	const minutes = Math.floor(totalSeconds / 60);
	const seconds = totalSeconds % 60;
	return minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;
}
