const defaultAssistantHost = "127.0.0.1";
const defaultAssistantPort = 3284;

export function validateAssistantHost(value: string | undefined): string {
	const host = value ?? defaultAssistantHost;
	if (host.trim() !== host || host === "")
		throw new Error("Assistant host must be a literal loopback host");
	const normalized = host.toLowerCase();
	if (normalized === "localhost" || normalized === "127.0.0.1" || normalized === "::1")
		return normalized;
	throw new Error("Assistant host must be a literal loopback host (localhost, 127.0.0.1, or ::1)");
}

export function parseAssistantPort(value: string | undefined): number {
	if (value === undefined) return defaultAssistantPort;
	if (!/^\d+$/.test(value)) throw new Error("Assistant port must be an integer from 1 to 65535");
	const port = Number(value);
	if (!Number.isSafeInteger(port) || port < 1 || port > 65535)
		throw new Error("Assistant port must be an integer from 1 to 65535");
	return port;
}

export function validateAssistantRuntimePort(value: number | undefined): number {
	const port = value ?? defaultAssistantPort;
	if (!Number.isSafeInteger(port) || port < 0 || port > 65535)
		throw new Error("Assistant runtime port must be an integer from 0 to 65535");
	return port;
}

export function validateAssistantSecret(value: string | undefined): string {
	if (!value || value.length < 16)
		throw new Error("Pi host secret must contain at least 16 characters");
	return value;
}
