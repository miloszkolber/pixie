import { randomUUID } from "node:crypto";
import { mkdir, open, readFile, rename, rm } from "node:fs/promises";
import { dirname, join } from "node:path";
import lockfile from "proper-lockfile";

export type RecordValue = Record<string, unknown>;
export function object(value: unknown): RecordValue {
	return value && typeof value === "object" && !Array.isArray(value) ? (value as RecordValue) : {};
}
export function text(value: unknown): string {
	return typeof value === "string" ? value : "";
}
export function required(value: unknown, name: string, max = 4096): string {
	const s = text(value);
	if (!s || s.length > max || s.includes("\0")) throw new Error(`Invalid ${name}`);
	return s;
}
export class HostError extends Error {
	constructor(
		message: string,
		readonly code = -32000,
	) {
		super(message);
	}
}
export async function atomicWrite(path: string, data: string): Promise<void> {
	await mkdir(dirname(path), { recursive: true, mode: 0o700 });
	const temporary = `${path}.${randomUUID()}.tmp`;
	try {
		const file = await open(temporary, "wx", 0o600);
		try {
			await file.writeFile(data);
			await file.sync();
		} finally {
			await file.close();
		}
		await rename(temporary, path);
		const directory = await open(dirname(path), "r");
		try {
			await directory.sync();
		} finally {
			await directory.close();
		}
	} finally {
		await rm(temporary, { force: true });
	}
}
const writes = new Map<string, Promise<unknown>>();
export class JsonStore<T> {
	constructor(
		readonly path: string,
		readonly initial: () => T,
	) {}
	async read(): Promise<T> {
		try {
			return JSON.parse(await readFile(this.path, "utf8")) as T;
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return this.initial();
			throw new Error(`Cannot read state ${this.path}`, { cause: error });
		}
	}
	update<R>(change: (state: T) => R | Promise<R>): Promise<R> {
		const operation = (writes.get(this.path) ?? Promise.resolve()).then(async () => {
			await mkdir(dirname(this.path), { recursive: true, mode: 0o700 });
			const release = await lockfile.lock(this.path, {
				realpath: false,
				retries: { retries: 20, minTimeout: 10, maxTimeout: 100 },
			});
			try {
				const state = await this.read();
				const result = await change(state);
				await atomicWrite(this.path, JSON.stringify(state));
				return result;
			} finally {
				await release();
			}
		});
		const tail = operation.catch(() => {});
		writes.set(this.path, tail);
		void tail.finally(() => {
			if (writes.get(this.path) === tail) writes.delete(this.path);
		});
		return operation;
	}
}
export function serviceStore<T>(agentDir: string, name: string, initial: () => T): JsonStore<T> {
	return new JsonStore(join(agentDir, "pixie", `${name}.json`), initial);
}
