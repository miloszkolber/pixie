export const DIRECTORY_PAGE_SIZE = 100;

export function parentPath(path: string): string | null {
	const end = path.length > 1 && path.endsWith("/") ? path.length - 1 : path.length;
	const index = path.lastIndexOf("/", end - 1);
	return index > 0 ? path.slice(0, index) : null;
}
