import { expect, test } from "bun:test";
import {
	type CompositionInput,
	collectCompositionInput,
	formatCompositionReport,
	inspectComposition,
} from "../../scripts/check-composition.ts";

const passingDockerfile = `
FROM runtime AS pixie_web
COPY --from=build /out/pixie_web /app/pixie_web
ENTRYPOINT ["/app/pixie_web", "serve", "--mode", "controller"]
`;

function passingComposition(): CompositionInput {
	const assistantSources = {
		"assistant/src/serve.ts":
			'import { startBunHostFromVerifiedPi } from "./host.ts";\nimport { verifyPiPackage } from "./probe.ts";\n',
		"assistant/src/host.ts":
			"export async function startBunHostFromVerifiedPi() { return Bun.serve({}); }\n",
		"assistant/src/probe.ts": "export async function verifyPiPackage() {}\n",
	};
	const packageCommandSources = {
		"web/cmd/main.go": "package main\n\nfunc main() {}\n",
	};
	return {
		assistantSources,
		packageCommandSources,
		productionSources: { ...assistantSources, ...packageCommandSources },
		packageGoModText: "module example.test/pixie\n\ngo 1.27\n",
		dockerfileText: passingDockerfile,
	};
}

test("a Bun-only assistant composes with the controller entrypoint", () => {
	const report = inspectComposition(passingComposition());

	expect(report.ok).toBe(true);
	expect(report.facts.bunServeCount).toBe(1);
	expect(report.facts.supervisorOwners).toEqual([]);
	expect(report.facts.controller.controllerDefault).toBe(true);
	expect(report.facts.controller.rejectsLocalAssistant).toBe(true);
	expect(report.facts.controller.uiEmbed).toBe(true);
	expect(report.facts.controller.drain).toBe(true);
});

test("composition rejects reintroduced Go sources, the bridge, and assistant module wiring", () => {
	const input = passingComposition();
	const report = inspectComposition({
		...input,
		assistantSources: {
			...input.assistantSources,
			"assistant/host/host.go": "package host\n",
			"assistant/cmd/main.go": "package main\n",
			"assistant/bridge/bad.ts": 'import "../../web/internal/controller/runtime";\n',
		},
		assistantGoModText: "module example.test/assistant\n\ngo 1.27\n",
		packageCommandSources: {
			"web/cmd/main.go": 'package main\n\nimport "github.com/miloszkolber/pixie/assistant/host"\n',
		},
		productionSources: {
			...input.productionSources,
			"assistant/src/supervisor.ts": "export class AssistantSupervisor {}\n",
			"web/internal/supervisor/supervisor.go": "package supervisor\n\ntype Supervisor struct{}\n",
			"assistant/src/extra.ts": "Bun.serve({});\n",
			"web/internal/server.ts": "Bun.serve({});\n",
		},
		packageGoModText:
			"module example.test/pixie\n\nrequire github.com/miloszkolber/pixie/assistant v0.0.0\n\nreplace github.com/miloszkolber/pixie/assistant => ../assistant\n",
		dockerfileText:
			'FROM runtime AS pixie_web\nCOPY assistant/ /app/assistant\nENTRYPOINT ["/app/pixie_web"]\n',
	});

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toMatch(/assistant Go module was removed/);
	expect(report.violations.join("\n")).toMatch(/must not require the removed assistant/);
	expect(report.violations.join("\n")).toMatch(/must not replace/);
	expect(report.violations.join("\n")).toMatch(/Go sources were removed/);
	expect(report.violations.join("\n")).toMatch(/assistant\/host\//);
	expect(report.violations.join("\n")).toMatch(/assistant\/bridge\//);
	expect(report.violations.join("\n")).toMatch(/must not import the removed assistant/);
	expect(report.violations.join("\n")).toMatch(/forbidden controller-internal import/);
	expect(report.violations.join("\n")).toMatch(/duplicate Bun\.serve/);
	expect(report.violations.join("\n")).toMatch(/duplicate supervisor/);
	expect(report.violations.join("\n")).toMatch(/must not copy assistant/);
	expect(report.violations.join("\n")).toMatch(/--mode controller/);
});

test("the checked-in composition satisfies the Bun-only boundary", async () => {
	const report = inspectComposition(await collectCompositionInput());

	expect(formatCompositionReport(report)).toContain("check-composition: OK");
	expect(report.ok).toBe(true);
	expect(report.violations).toEqual([]);
	expect(report.facts.bunServeCount).toBe(1);
});
