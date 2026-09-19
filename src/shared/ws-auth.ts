export const CODE_TOKEN_MIN_LENGTH = 32;
export const CODE_TOKEN_MAX_LENGTH = 256;

/** Values intentionally shipped in documentation as setup sentinels. */
export const TOKEN_SENTINELS = [
	"INVALID_REPLACE_WITH_RANDOM_CONTROLLER_TOKEN",
	"INVALID_REPLACE_WITH_RANDOM_BROWSER_TOKEN",
	"replace-with-a-random-controller-token",
	"replace-with-a-random-browser-token",
	"replace-with-a-random-token",
] as const;

const CODE_TOKEN_PATTERN = /^[\x21-\x7e]+$/;

/** Validate a printable token suitable for controller and browser authentication. */
export function isStrongToken(value: unknown): value is string {
	return (
		typeof value === "string" &&
		value.length >= CODE_TOKEN_MIN_LENGTH &&
		value.length <= CODE_TOKEN_MAX_LENGTH &&
		CODE_TOKEN_PATTERN.test(value) &&
		!(TOKEN_SENTINELS as readonly string[]).includes(value)
	);
}

export function isCodeToken(value: unknown): value is string {
	return isStrongToken(value);
}
