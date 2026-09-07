export function activateCheckableMenuItem(event: KeyboardEvent): void {
	if (event.key !== "Enter" && event.key !== " ") return;
	event.preventDefault();
	event.stopPropagation();
	(event.currentTarget as HTMLElement).click();
}
