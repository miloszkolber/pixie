import type { RuntimeAvailability } from "@pixie/contracts";

export const STATE_LABEL: Record<RuntimeAvailability, string> = {
	ready: "Ready",
	degraded: "Degraded",
	unavailable: "Unavailable",
};

export const STATE_CLASS: Record<RuntimeAvailability, string> = {
	ready: "text-feedback-success",
	degraded: "text-feedback-warning",
	unavailable: "text-feedback-error",
};

function finiteNonnegative(value: number): number | null {
	return Number.isFinite(value) && value >= 0 ? value : null;
}

export function formatCount(value: number): string {
	const safe = finiteNonnegative(value);
	return safe === null ? "—" : Math.round(safe).toLocaleString();
}

export function formatMilliseconds(value: number): string {
	const safe = finiteNonnegative(value);
	if (safe === null) return "—";
	if (safe < 0.1) return "<0.1 ms";
	if (safe < 1_000) return `${safe < 10 ? safe.toFixed(1) : Math.round(safe)} ms`;
	return `${(safe / 1_000).toFixed(safe < 10_000 ? 1 : 0)} s`;
}

export function formatBytes(value: number): string {
	const safe = finiteNonnegative(value);
	if (safe === null) return "—";
	if (safe < 1_024) return `${Math.round(safe)} B`;
	if (safe < 1_024 * 1_024) return `${Math.round(safe / 1_024)} KiB`;
	return `${(safe / (1_024 * 1_024)).toFixed(safe < 10 * 1_024 * 1_024 ? 1 : 0)} MiB`;
}

export function formatUptime(value: number): string {
	const safe = finiteNonnegative(value);
	if (safe === null) return "—";
	const seconds = Math.floor(safe);
	const days = Math.floor(seconds / 86_400);
	const hours = Math.floor((seconds % 86_400) / 3_600);
	const minutes = Math.floor((seconds % 3_600) / 60);
	if (days > 0) return `${days}d ${hours}h`;
	if (hours > 0) return `${hours}h ${minutes}m`;
	if (minutes > 0) return `${minutes}m`;
	return `${seconds}s`;
}
