/** Short muted timestamp for session rows: now, 3h, 4h, 1d, 3d, 1w, 2w. */
export function shortSessionAge(updatedAt: number, now: number = Date.now()): string {
	if (!Number.isFinite(updatedAt) || !Number.isFinite(now) || updatedAt <= 0 || updatedAt > now)
		return "now";
	const seconds = Math.floor((now - updatedAt) / 1000);
	if (seconds < 60) return "now";
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h`;
	const days = Math.floor(hours / 24);
	if (days < 7) return `${days}d`;
	return `${Math.floor(days / 7)}w`;
}
