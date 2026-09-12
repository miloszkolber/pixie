import { Buffer } from "node:buffer";
import { constants } from "node:fs";
import { lstat, open, writeFile } from "node:fs/promises";
import { deflateRawSync } from "node:zlib";

const TAR_BLOCK_SIZE = 512;
const TAR_END_BLOCKS = 2;
const TAR_NAME_OFFSET = 0;
const TAR_NAME_LENGTH = 100;
const TAR_MODE_OFFSET = 100;
const TAR_UID_OFFSET = 108;
const TAR_GID_OFFSET = 116;
const TAR_SIZE_OFFSET = 124;
const TAR_MTIME_OFFSET = 136;
const TAR_CHECKSUM_OFFSET = 148;
const TAR_TYPE_OFFSET = 156;
const TAR_MAGIC_OFFSET = 257;
const TAR_VERSION_OFFSET = 263;
const GZIP_HEADER = Buffer.from([0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff]);
const UTF8 = new TextEncoder();

export interface DeterministicTarEntry {
	name: string;
	path: string;
	mode: number;
}

function compareBytes(left: Uint8Array, right: Uint8Array): number {
	const length = Math.min(left.byteLength, right.byteLength);
	for (let index = 0; index < length; index += 1) {
		const difference = (left[index] ?? 0) - (right[index] ?? 0);
		if (difference !== 0) return difference;
	}
	return left.byteLength - right.byteLength;
}

function archiveNameBytes(name: string): Uint8Array {
	if (
		name.length === 0 ||
		name.startsWith("/") ||
		name.includes("\0") ||
		name.split("/").some((part) => part.length === 0 || part === "." || part === "..")
	) {
		throw new Error(`release archive entry has an unsafe name: ${JSON.stringify(name)}`);
	}
	const bytes = UTF8.encode(name);
	if (bytes.byteLength > TAR_NAME_LENGTH)
		throw new Error(`release archive entry name is too long for ustar: ${JSON.stringify(name)}`);
	return bytes;
}

function writeOctal(
	header: Uint8Array,
	offset: number,
	width: number,
	value: number,
	label: string,
): void {
	const maximum = 8 ** (width - 1) - 1;
	if (!Number.isSafeInteger(value) || value < 0 || value > maximum)
		throw new Error(`release archive ${label} is outside the portable ustar range`);
	const text = value.toString(8);
	const field = header.subarray(offset, offset + width);
	field.fill(0x30);
	field.set(UTF8.encode(text), width - 1 - text.length);
	field[width - 1] = 0;
}

function crc32(bytes: Uint8Array): number {
	let crc = 0xffffffff;
	for (const byte of bytes) {
		crc ^= byte;
		for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
	}
	return (crc ^ 0xffffffff) >>> 0;
}

async function readRegularFile(path: string): Promise<Buffer> {
	const initialStats = await lstat(path);
	if (!initialStats.isFile())
		throw new Error(`release archive input is not a regular file: ${path}`);
	let handle: Awaited<ReturnType<typeof open>> | undefined;
	try {
		handle = await open(path, constants.O_RDONLY | constants.O_NONBLOCK | constants.O_NOFOLLOW);
		const openedStats = await handle.stat();
		if (!openedStats.isFile())
			throw new Error(`release archive input is not a regular file: ${path}`);
		return await handle.readFile();
	} finally {
		await handle?.close();
	}
}

function tarHeader(name: Uint8Array, mode: number, size: number, mtime: number): Buffer {
	if (!Number.isSafeInteger(mode) || mode < 0 || mode > 0o7777)
		throw new Error("release archive mode must be a portable file mode");
	const header = Buffer.alloc(TAR_BLOCK_SIZE);
	header.set(name, TAR_NAME_OFFSET);
	writeOctal(header, TAR_MODE_OFFSET, 8, mode, "mode");
	writeOctal(header, TAR_UID_OFFSET, 8, 0, "owner");
	writeOctal(header, TAR_GID_OFFSET, 8, 0, "group");
	writeOctal(header, TAR_SIZE_OFFSET, 12, size, "file size");
	writeOctal(header, TAR_MTIME_OFFSET, 12, mtime, "mtime");
	header.fill(0x20, TAR_CHECKSUM_OFFSET, TAR_CHECKSUM_OFFSET + 8);
	header[TAR_TYPE_OFFSET] = "0".charCodeAt(0);
	header.set(UTF8.encode("ustar\0"), TAR_MAGIC_OFFSET);
	header.set(UTF8.encode("00"), TAR_VERSION_OFFSET);
	let checksum = 0;
	for (const byte of header) checksum += byte;
	const checksumText = checksum.toString(8).padStart(6, "0");
	header.set(UTF8.encode(checksumText), TAR_CHECKSUM_OFFSET);
	header[TAR_CHECKSUM_OFFSET + 6] = 0;
	header[TAR_CHECKSUM_OFFSET + 7] = 0x20;
	return header;
}

function gzip(tar: Buffer): Buffer {
	const trailer = Buffer.alloc(8);
	trailer.writeUInt32LE(crc32(tar), 0);
	trailer.writeUInt32LE(tar.byteLength >>> 0, 4);
	return Buffer.concat([GZIP_HEADER, deflateRawSync(tar, { level: 9 }), trailer]);
}

/**
 * Write a regular-file-only ustar archive with a manually fixed gzip header.
 * It deliberately avoids host tar implementations so BusyBox and GNU hosts
 * produce the same staged release bytes from the same inputs.
 */
export async function writeDeterministicTarGz(
	archivePath: string,
	entries: readonly DeterministicTarEntry[],
	mtime: number,
): Promise<void> {
	if (!Number.isSafeInteger(mtime) || mtime < 0)
		throw new Error("release archive mtime must be a non-negative integer");
	const ordered = entries
		.map((entry) => ({ ...entry, nameBytes: archiveNameBytes(entry.name) }))
		.sort((left, right) => compareBytes(left.nameBytes, right.nameBytes));
	for (let index = 1; index < ordered.length; index += 1) {
		const previous = ordered[index - 1];
		const current = ordered[index];
		if (previous === undefined || current === undefined)
			throw new Error("release archive ordering changed unexpectedly");
		if (compareBytes(previous.nameBytes, current.nameBytes) === 0)
			throw new Error(`release archive has duplicate entry: ${JSON.stringify(current.name)}`);
	}

	const blocks: Buffer[] = [];
	for (const entry of ordered) {
		const content = await readRegularFile(entry.path);
		blocks.push(tarHeader(entry.nameBytes, entry.mode, content.byteLength, mtime), content);
		const padding = (TAR_BLOCK_SIZE - (content.byteLength % TAR_BLOCK_SIZE)) % TAR_BLOCK_SIZE;
		if (padding !== 0) blocks.push(Buffer.alloc(padding));
	}
	blocks.push(Buffer.alloc(TAR_BLOCK_SIZE * TAR_END_BLOCKS));
	await writeFile(archivePath, gzip(Buffer.concat(blocks)));
}
