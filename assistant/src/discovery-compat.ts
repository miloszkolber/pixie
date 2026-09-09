/**
 * Legacy import path retained during the Go/host migration.
 *
 * Compatibility decisions and rollback planning have one implementation under
 * `compatibility/`; keeping this shim prevents old callers from silently
 * receiving a weaker schema/monotonic-state policy.
 */
export * from "./compatibility/index.ts";
