import { expect, test } from "bun:test";
import {
	type CompositionInput,
	collectCompositionInput,
	formatCompositionReport,
	inspectComposition,
} from "../../scripts/check-composition.ts";

const controllerDockerfile = `
FROM golang:1.27 AS go-source
RUN go mod edit -droprequire=github.com/miloszkolber/pixie/assistant -dropreplace=github.com/miloszkolber/pixie/assistant
RUN go build -tags=controller ./cmd
FROM runtime AS pixie
COPY --from=go-source /out/pixie /app/pixie
COPY --from=web-build /work/package/webui/dist /app/web
USER 1000:1000
HEALTHCHECK CMD ["/app/pixie", "healthcheck"]
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/pixie", "serve", "--mode", "controller"]
`;

function composition(dockerfile: string): CompositionInput {
	const assistantSources = {
		"assistant/go.mod": "module example.test/assistant\n",
		"assistant/host/host.go": "package host\ntype Handle struct{}\n",
		"assistant/cmd/pixie-assistant/main.go": 'package main\nimport "example.test/assistant/host"\n',
	};
	const packageCommandSources = {
		"package/cmd/main.go":
			'package main\nimport assistantHost "example.test/assistant/host"\nfunc run(ctx context.Context) { assistant, _ := assistantHost.Start(ctx, assistantHost.Config{Host: "127.0.0.1", Port: 0}); defer assistant.Close(context.Background()); controller.NewRuntime(controller.RuntimeConfig{PiURL: assistant.Endpoint()}) }\n',
		"package/cmd/runtime.go":
			'package main\nconst ( modeFullHost = "full-host"; modeController = "controller" )\nfunc parseMode() {}\nfunc serveController() { context.WithTimeout(context.Background(), time.Second); runtime.Shutdown(ctx) }\n',
	};
	const packageWebuiSources = {
		"package/webui/webui.go": "package webui\n//go:embed all:dist\n",
	};
	return {
		assistantSources,
		packageCommandSources,
		productionSources: {
			...assistantSources,
			...packageCommandSources,
			"package/internal/controller/runtime.go":
				"package controller\nfunc (r *Runtime) Shutdown(ctx context.Context) error { r.sessions.shutdown(ctx); r.client.Close(); r.work.Wait(); return r.server.Shutdown(ctx) }\n",
			"package/internal/controller/pairing_authority.go":
				"package controller\n// authorityBindingId persists paired ownership.\n",
		},
		packageWebuiSources,
		embeddedUiFiles: ["package/webui/dist/index.html"],
		assistantGoModText: "module example.test/assistant\n",
		packageGoModText:
			"module example.test/pixie\nrequire example.test/assistant v0.0.0\nreplace example.test/assistant => ../assistant\n",
		dockerfileText: dockerfile,
	};
}

test("Docker composition is controller-only and keeps effective PID 1 init", () => {
	const report = inspectComposition(composition(controllerDockerfile));

	expect(report.ok).toBe(true);
	expect(report.facts.docker.controllerOnlyBuild).toBe(true);
	expect(report.facts.docker.explicitControllerEntrypoint).toBe(true);
	expect(report.facts.docker.effectiveInit).toBe(true);
	expect(report.facts.docker.noAssistantRuntime).toBe(true);
	expect(report.facts.fullHost.facadeStart).toBe(true);
	expect(report.facts.fullHost.controllerUsesFacadeEndpoint).toBe(true);
	expect(report.facts.fullHost.uiEmbed).toBe(true);
	expect(report.facts.fullHost.modeSwitch).toBe(true);
	expect(report.facts.fullHost.drain).toBe(true);
});

test("Docker composition rejects assistant/Pi startup and a non-reaping final command", () => {
	const report = inspectComposition(
		composition(`
FROM runtime AS pixie
COPY assistant/src/ /app/assistant
RUN go build ./cmd
ENTRYPOINT ["/app/pixie", "serve", "--mode", "full-host", "pi serve"]
`),
	);

	expect(report.ok).toBe(false);
	const output = report.violations.join("\n");
	expect(output).toMatch(/must not copy assistant/);
	expect(output).toMatch(/controller build tag/);
	expect(output).toMatch(/--mode controller/);
	expect(output).toMatch(/tini/);
	expect(output).toMatch(/must not start an assistant or local Pi/);
});

test("checked-in Docker and full-host composition report static facts without live-artifact claims", async () => {
	const report = inspectComposition(await collectCompositionInput());
	const output = formatCompositionReport(report);

	expect(report.ok).toBe(true);
	expect(report.facts.docker.controllerOnlyBuild).toBe(true);
	expect(report.facts.docker.explicitControllerEntrypoint).toBe(true);
	expect(report.facts.docker.effectiveInit).toBe(true);
	expect(report.facts.fullHost.uiEmbed).toBe(true);
	expect(output).toMatch(/missing live evidence/);
	expect(output).not.toMatch(/ErrUnavailable/);
});
