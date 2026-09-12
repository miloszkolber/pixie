/** Bounds and keyboard steps for the chat/preview split handle. */

export const SPLIT_MIN = 20;
export const SPLIT_MAX = 80;
export const SPLIT_DEFAULT = 50;
export const SPLIT_STEP = 5;
export const SPLIT_LARGE_STEP = 10;

export function clampSplitPercent(value: number): number {
	if (!Number.isFinite(value)) return SPLIT_DEFAULT;
	return Math.min(SPLIT_MAX, Math.max(SPLIT_MIN, value));
}

export function stepSplitPercent(current: number, direction: -1 | 1, large = false): number {
	const step = large ? SPLIT_LARGE_STEP : SPLIT_STEP;
	return clampSplitPercent(clampSplitPercent(current) + direction * step);
}
