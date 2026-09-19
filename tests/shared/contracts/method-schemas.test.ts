import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { loadProtocolCatalog, SCHEMA_RELATIVE_PATH } from "../../../scripts/generate-contracts";
import {
	CONTROLLER_METHOD_SCHEMAS,
	HOST_METHOD_SCHEMAS,
	METHOD_FIELD_TYPES,
} from "../../../src/shared/generated/protocol-catalog";
import {
	isWsMethodResult,
	isWsRequest,
	validateWsErrorEnvelope,
	validateWsMethodParams,
	validateWsMethodResult,
} from "../../../src/shared/ws-runtime";

const packageDir = resolve(import.meta.dir, "..", "..", "..");

interface PlainMethodSchema {
	name: string;
	params: { name: string; type: string; optional?: boolean }[];
	result: { name: string; type: string; optional?: boolean }[];
}

interface LooseMethodField {
	name: string;
	type: string;
	optional?: boolean;
}

interface LooseMethodSchema {
	name: string;
	params: readonly LooseMethodField[];
	result: readonly LooseMethodField[];
}

function plainTable(schemas: readonly LooseMethodSchema[]): PlainMethodSchema[] {
	return schemas.map((entry) => ({
		name: entry.name,
		params: entry.params.map((field) =>
			field.optional
				? { name: field.name, type: field.type, optional: true }
				: { name: field.name, type: field.type },
		),
		result: entry.result.map((field) =>
			field.optional
				? { name: field.name, type: field.type, optional: true }
				: { name: field.name, type: field.type },
		),
	}));
}

test("method schemas declare allowed field types and catalogued methods", () => {
	const schema = loadProtocolCatalog(join(packageDir, SCHEMA_RELATIVE_PATH));
	expect(plainTable(HOST_METHOD_SCHEMAS)).toEqual(plainTable(schema.methodSchemas.host));
	expect(plainTable(CONTROLLER_METHOD_SCHEMAS)).toEqual(
		plainTable(schema.methodSchemas.controller),
	);
	for (const entry of [...HOST_METHOD_SCHEMAS, ...CONTROLLER_METHOD_SCHEMAS]) {
		for (const field of [...entry.params, ...entry.result]) {
			expect(METHOD_FIELD_TYPES as readonly string[]).toContain(field.type);
		}
	}
});

test("session.fork accepts an optional entry identity for edit-from-here", () => {
	// AUX-14: a new-file fork omits entryId; an in-file branch supplies the
	// native entry id. The parameter is optional on both the browser and host
	// direction so the existing fork contract stays compatible.
	expect(
		validateWsMethodParams("session.fork", { projectId: "p", sessionId: "s" }),
	).toBeUndefined();
	expect(
		validateWsMethodParams("session.fork", { projectId: "p", sessionId: "s", entryId: "entry-1" }),
	).toBeUndefined();
	expect(
		validateWsMethodParams("session.fork", { projectId: "p", sessionId: "s", entryId: 4 }),
	).toBe("session.fork params field entryId must be string");
	const hostFork = HOST_METHOD_SCHEMAS.find((entry) => entry.name === "session.fork");
	const hostEntry = hostFork?.params.find((field) => field.name === "entryId");
	expect(hostEntry).toMatchObject({ name: "entryId", type: "string", optional: true });
});

test("method parameter validation rejects malformed payloads and keeps additive fields", () => {
	expect(validateWsMethodParams("session.prompt", { sessionId: "s", text: "hi" })).toBeUndefined();
	expect(validateWsMethodParams("session.prompt", { sessionId: "s" })).toBe(
		"session.prompt params is missing text",
	);
	expect(validateWsMethodParams("session.prompt", { sessionId: 7, text: "hi" })).toBe(
		"session.prompt params field sessionId must be string",
	);
	expect(validateWsMethodParams("session.prompt", "not-an-object")).toBe(
		"session.prompt params must be an object",
	);
	expect(validateWsMethodParams("session.prompt", null)).toBe(
		"session.prompt params must be an object",
	);
	// Unknown fields are additive-compatible, and a method without a schema is
	// envelope-validated only.
	expect(
		validateWsMethodParams("session.prompt", {
			sessionId: "s",
			text: "hi",
			futureField: { nested: true },
		}),
	).toBeUndefined();
	expect(validateWsMethodParams("not.a.method", { anything: true })).toBeUndefined();
});

test("method result validation rejects invalid results and accepts additive fields", () => {
	expect(validateWsMethodResult("model.clampThinking", { level: "medium" })).toBeUndefined();
	expect(
		validateWsMethodResult("model.clampThinking", { level: "medium", additive: true }),
	).toBeUndefined();
	expect(validateWsMethodResult("model.clampThinking", {})).toBe(
		"model.clampThinking result is missing level",
	);
	expect(validateWsMethodResult("model.clampThinking", { level: 3 })).toBe(
		"model.clampThinking result field level must be string",
	);
	expect(validateWsMethodResult("model.thinkingLevels", { levels: ["low"] })).toBeUndefined();
	expect(validateWsMethodResult("model.thinkingLevels", { levels: "low" })).toBe(
		"model.thinkingLevels result field levels must be array",
	);
	expect(validateWsMethodResult("session.list", [])).toBeUndefined();
	expect(validateWsMethodResult("not.a.method", null)).toBeUndefined();
});

test("error envelope validation requires a message and typed code", () => {
	expect(validateWsErrorEnvelope({ id: "1", ok: false, error: "boom" })).toBeUndefined();
	expect(
		validateWsErrorEnvelope({ id: "1", ok: false, error: "boom", errorCode: "UNKNOWN_COMMIT" }),
	).toBeUndefined();
	expect(validateWsErrorEnvelope({ id: "1", ok: false })).toBe(
		"error envelope requires a non-empty message",
	);
	expect(validateWsErrorEnvelope({ id: "1", ok: false, error: "boom", errorCode: 4 })).toBe(
		"error envelope errorCode must be a string",
	);
});

test("isWsRequest applies the method schema and isWsMethodResult checks results", () => {
	expect(
		isWsRequest({ id: "1", method: "session.prompt", params: { sessionId: "s", text: "x" } }),
	).toBe(true);
	expect(isWsRequest({ id: "1", method: "session.prompt", params: { sessionId: "s" } })).toBe(
		false,
	);
	// A method without a generated schema still passes envelope validation.
	expect(isWsRequest({ id: "1", method: "not.a.method", params: {} })).toBe(true);
	expect(
		isWsMethodResult("model.clampThinking", { id: "1", ok: true, result: { level: "medium" } }),
	).toBe(true);
	expect(isWsMethodResult("model.clampThinking", { id: "1", ok: true, result: {} })).toBe(false);
	expect(isWsMethodResult("model.clampThinking", { id: "1", ok: false })).toBe(false);
});

// The Go adapter is generated from the same schema. This executable drift check
// compares the generated Go method-schema table with the TypeScript one so a
// generator change that only updates one adapter fails here.
test("Go and TypeScript method-schema tables carry the same methods and fields", () => {
	const go = readFileSync(join(packageDir, "piprotocol", "catalog_generated.go"), "utf8");
	for (const name of ["CatalogHostMethodSchemas", "CatalogControllerMethodSchemas"]) {
		expect(go).toContain(`var ${name} = []CatalogMethodSchema{`);
	}
	const goMethods = new Set(
		[...go.matchAll(/^\t\{Name: "([^"]+)", Params:/gm)].map((match) => match[1] ?? ""),
	);
	const tsMethods = new Set(
		[...HOST_METHOD_SCHEMAS, ...CONTROLLER_METHOD_SCHEMAS].map((entry) => entry.name),
	);
	for (const name of tsMethods) {
		expect(goMethods.has(name)).toBe(true);
	}
	expect(goMethods.size).toBeGreaterThanOrEqual(tsMethods.size);
	// Required fields are rendered identically on both sides.
	for (const entry of [...HOST_METHOD_SCHEMAS, ...CONTROLLER_METHOD_SCHEMAS]) {
		for (const field of entry.result) {
			const rendered = `{Name: ${JSON.stringify(field.name)}, Type: ${JSON.stringify(field.type)}}`;
			expect(go).toContain(rendered);
		}
	}
});
