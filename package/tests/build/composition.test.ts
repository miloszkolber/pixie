import { expect, test } from "bun:test";
import {
	collectCompositionInput,
	formatCompositionReport,
	inspectComposition,
	type CompositionInput,
} from "../../scripts/check-composition.ts";

const passingDockerfile = `
FROM runtime AS pixie
COPY --from=build /out/pixie /app/pixie
ENTRYPOINT ["/app/pixie", "serve", "--mode", "controller"]
`;

function passingComposition(): CompositionInput {
	const assistantSources = {
		"assistant/host/host.go": "package host\n\ntype Handle struct{}\n",
		"assistant/internal/supervisor/supervisor.go": "package supervisor\n\ntype Supervisor struct{}\n",
		"assistant/cmd/pixie-assistant/main.go":
			'package main\n\nimport "example.test/assistant/host"\n\nfunc main() { _ = host.Handle{} }\n',
	};
	const packageCommandSources = {
		"package/cmd/main.go":
			'package main\n\nimport "example.test/assistant/host"\n\nfunc main() { _ = host.Handle{} }\n',
	};
	return {
		assistantSources,
		packageCommandSources,
		productionSources: { ...assistantSources, ...packageCommandSources },
		assistantGoModText: "module example.test/assistant\n\ngo 1.27\n",
		packageGoModText:
			"module example.test/pixie\n\nrequire example.test/assistant v0.0.0\n\nreplace example.test/assistant => ../assistant\n",
		dockerfileText: passingDockerfile,
	};
}

test("a single public assistant facade composes both host entrypoints", () => {
	const report = inspectComposition(passingComposition());

	expect(report.ok).toBe(true);
	expect(report.facts.publicFacadeImport).toBe("example.test/assistant/host");
	expect(report.facts.bunServeCount).toBe(0);
	expect(report.facts.supervisorOwners).toEqual(["assistant"]);
});

test(
	"composition rejects a missing facade, controller-internal imports and nonlocal replacement",
	() => {
		const input = passingComposition();
		const report = inspectComposition({
			...input,
		assistantSources: {
			...input.assistantSources,
			"assistant/src/bad.ts": 'import "../../package/internal/controller/runtime";\n',
			"assistant/internal/bad.go":
				"package bad\n\nimport \"example.test/pixie/internal/controller\"\n",
		},
			packageCommandSources: {
				"package/cmd/main.go": "package main\n",
			},
			productionSources: {
				...input.productionSources,
				"package/internal/supervisor/supervisor.go":
					"package supervisor\n\ntype Supervisor struct{}\n",
				"assistant/src/server.ts": "Bun.serve({});\n",
				"package/internal/server.ts": "Bun.serve({});\n",
			},
			packageGoModText: "module example.test/pixie\n\nrequire example.test/assistant v0.0.0\n",
			dockerfileText:
				"FROM runtime AS pixie\nCOPY assistant/ /app/assistant\nENTRYPOINT [\"/app/pixie\"]\n",
		});

		expect(report.ok).toBe(false);
		expect(report.violations.join("\n")).toMatch(/public assistant facade/);
		expect(report.violations.join("\n")).toMatch(/forbidden controller-internal import/);
		expect(report.violations.join("\n")).toMatch(/exact local replacement/);
		expect(report.violations.join("\n")).toMatch(/duplicate Bun\.serve/);
		expect(report.violations.join("\n")).toMatch(/duplicate supervisor/);
		expect(report.violations.join("\n")).toMatch(/must not copy assistant/);
		expect(report.violations.join("\n")).toMatch(/--mode controller/);
	},
);

test("the checked-in composition satisfies the BUILD-01 boundary", async () => {
	const report = inspectComposition(await collectCompositionInput());

	expect(formatCompositionReport(report)).toContain("check-composition: OK");
	expect(report.ok).toBe(true);
	expect(report.violations).toEqual([]);
	expect(report.facts.publicFacadeImport).toBe("github.com/miloszkolber/pixie/assistant/host");
	expect(report.facts.bunServeCount).toBeLessThanOrEqual(1);
});
