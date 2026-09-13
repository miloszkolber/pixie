export function shouldLoadArchivedChats(
	open: boolean,
	showArchived: boolean,
	status: string,
	canArchive: boolean,
): boolean {
	return open && showArchived && status === "connected" && canArchive;
}
