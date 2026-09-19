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
RUN go build -tags=controller -o /out/pixie_web ./cmd
RUN go build -o /out/pixie ./cmd/pixie
COPY web/scripts/release-runtime.ts web/scripts/release-runtime.ts
RUN bun build --target=bun --outfile /out/pixie_assistant.js assistant/src/serve.ts
RUN bun -e 'stageBundledPiRuntime(); verifyBundledPiRuntime();'
RUN test -x /out/runtime/bin/bun
RUN test -f /out/runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js
FROM runtime AS pixie_web
COPY --from=go-source /out/pixie_web /app/pixie_web
COPY --from=web-build /work/web/webui/dist /app/web
USER 1000:1000
HEALTHCHECK CMD ["/app/pixie_web", "healthcheck"]
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/pixie_web", "serve", "--mode", "controller"]
`;

function composition(dockerfile: string): CompositionInput {
	const assistantSources = {
		"assistant/src/serve.ts":
			'import { startBunHostFromVerifiedPi } from "./host.ts";\nimport { verifyPiPackage } from "./probe.ts";\n',
		"assistant/src/host.ts":
			"export async function startBunHostFromVerifiedPi() { return Bun.serve({}); }\n",
		"assistant/src/probe.ts": "export async function verifyPiPackage() {}\n",
	};
	const packageCommandSources = {
		"web/cmd/main.go":
			'package main\nconst modeController = "controller"\nfunc parseMode() { mode := modeController; if mode != modeController { panic(mode) } }\nfunc rejectControllerAssistantSettings() {}\nfunc rejectControllerConfigAssistantSettings() {}\n',
		"web/cmd/runtime.go":
			"package main\nfunc serveController() { context.WithTimeout(context.Background(), time.Second); runtime.Shutdown(ctx) }\n",
	};
	const packageWebuiSources = {
		"web/webui/webui.go": "package webui\n//go:embed all:dist\n",
	};
	return {
		assistantSources,
		packageCommandSources,
		productionSources: {
			...assistantSources,
			...packageCommandSources,
		},
		packageWebuiSources,
		embeddedUiFiles: ["web/webui/dist/index.html"],
		packageGoModText: "module example.test/pixie\n",
		dockerfileText: dockerfile,
	};
}

test("Docker composition keeps the controller-only image with no combined image", () => {
	const report = inspectComposition(composition(controllerDockerfile));

	expect(report.ok).toBe(true);
	expect(report.facts.docker.controllerOnlyBuild).toBe(true);
	expect(report.facts.docker.explicitControllerEntrypoint).toBe(true);
	expect(report.facts.docker.effectiveInit).toBe(true);
	expect(report.facts.docker.noAssistantRuntime).toBe(true);
	expect(report.facts.docker.noCombinedImage).toBe(true);
	expect(report.facts.docker.pinnedBunRuntime).toBe(true);
	expect(report.facts.docker.noNodeRuntime).toBe(true);
	expect(report.facts.controller.controllerDefault).toBe(true);
	expect(report.facts.controller.rejectsLocalAssistant).toBe(true);
	expect(report.facts.controller.uiEmbed).toBe(true);
	expect(report.facts.controller.drain).toBe(true);
});

test("Docker composition rejects Pi in pixie_web and a non-reaping controller command", () => {
	const report = inspectComposition(
		composition(`
FROM runtime AS pixie_web
COPY assistant/src/ /app/assistant
COPY --from=pi-build /out/runtime /app/runtime
RUN go build ./cmd
ENTRYPOINT ["/app/pixie_web", "serve", "--mode", "full-host", "pi serve"]
`),
	);

	expect(report.ok).toBe(false);
	const output = report.violations.join("\n");
	expect(output).toMatch(/must not copy assistant/);
	expect(output).toMatch(/controller build tag/);
	expect(output).toMatch(/--mode controller/);
	expect(output).toMatch(/tini/);
	expect(output).toMatch(/must contain no Bun, Node, Pi package, assistant, or Pi launcher/);
});

test("Docker composition rejects a reintroduced combined pixie stage", () => {
	const report = inspectComposition(
		composition(
			`${controllerDockerfile}
FROM runtime AS pixie
COPY --from=go-build /out/pixie /app/pixie
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/pixie", "serve"]
`,
		),
	);

	expect(report.ok).toBe(false);
	expect(report.facts.docker.noCombinedImage).toBe(false);
	expect(report.violations.join("\n")).toMatch(/combined pixie image stage was removed/);
});

test("checked-in Docker and controller composition report static facts without live-artifact claims", async () => {
	const input = await collectCompositionInput();
	const report = inspectComposition(input);
	const output = formatCompositionReport(report);

	expect(report.ok).toBe(true);
	expect(input.dockerfileText).toContain("-o /out/pixie_web ./cmd");
	expect(input.dockerfileText).toContain("FROM controller-runtime AS pixie_web");
	expect(input.dockerfileText).not.toContain("FROM controller-runtime AS pixie\n");
	expect(input.dockerfileText).toContain("/app/pixie_web");
	expect(report.facts.docker.controllerOnlyBuild).toBe(true);
	expect(report.facts.docker.explicitControllerEntrypoint).toBe(true);
	expect(report.facts.docker.effectiveInit).toBe(true);
	expect(report.facts.docker.noCombinedImage).toBe(true);
	expect(report.facts.docker.pinnedBunRuntime).toBe(true);
	expect(report.facts.docker.noNodeRuntime).toBe(true);
	expect(report.facts.controller.uiEmbed).toBe(true);
	expect(report.facts.deployment).toEqual({
		composeUsesWebImage: true,
		composeHasNoCombinedImage: true,
		composeWebServiceIsNonRoot: true,
		composeWebUsesDataMount: true,
		composeHasNoFixedContainerNames: true,
		archiveServiceUsesHostCommand: true,
		noLegacyCliUnit: true,
		noPublicAssistantUnit: true,
		ownerLockDoesNotRestart: true,
		secretsInherited: true,
		absoluteConfigPaths: true,
		noShellInterpolation: true,
	});
	expect(output).toMatch(/missing live evidence/);
});

test("deployment composition rejects a reintroduced full service or Pi-state volume", async () => {
	const input = await collectCompositionInput();
	const unsafeCompose = `${input.composeFileText ?? ""}
    pixie:
        profiles: ["full"]
        image: \${PIXIE_FULL_IMAGE:-ghcr.io/miloszkolber/pixie:sha-0123456789ab}
        volumes:
            - pixie-pi-state:/var/lib/pixie/pi
`;
	const report = inspectComposition({ ...input, composeFileText: unsafeCompose });

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toMatch(/combined pixie topology was removed/);
});

test("deployment composition rejects unguarded images, fixed names, root services, old units, and lock restart loops", () => {
	const input = composition(controllerDockerfile);
	const report = inspectComposition({
		...input,
		composeFileText: `services:
    pixie_web:
        profiles: ["web"]
        image: ghcr.io/example/pixie_web:latest
        container_name: fixed-pixie
        network_mode: host
        environment:
            PIXIE_CONTROLLER_PORT: "${"${PIXIE_CONTROLLER_PORT:-7312}"}"
        volumes:
            - ${"${PIXIE_DATA_PATH}"}:/var/lib/pixie/data
`,
		systemdUnitSources: {
			"web/systemd/pixie.service": `[Service]
EnvironmentFile=%h/.config/pixie/pixie.env
Environment=PI_CODING_AGENT_DIR=%h/.local/share/pixie/pi
ExecStart=/bin/sh -c '%h/.local/bin/pixie serve --config ~/assistant.json'
Restart=on-failure
RestartForceExitStatus=75
`,
			"web/systemd/pixie_cli.service": `[Service]
	EnvironmentFile=%h/.config/pixie/pixie.env
	Environment=PI_CODING_AGENT_DIR=%h/.local/share/pixie/pi
	ExecStart=%h/.local/bin/pixie_cli serve --config %h/.config/pixie/assistant.json
	Restart=always
	RestartForceExitStatus=75
`,
			"web/systemd/pixie_assistant.service": `[Service]
ExecStart=%h/.local/bin/pixie_assistant serve --config %h/.config/pixie/assistant.json
`,
		},
		systemdConfigSources: {
			"web/systemd/assistant.json": JSON.stringify({
				schemaVersion: 1,
				host: "127.0.0.1",
				port: 3284,
				agentDir: "~/.pi/agent",
				piPackage: "/outside/pi",
			}),
			"web/systemd/pixie.json": JSON.stringify({
				host: "127.0.0.1",
				port: 7312,
				dataDir: "relative/data",
				agentDir: "/wrong-owner",
			}),
		},
	});

	expect(report.ok).toBe(false);
	const output = report.violations.join("\n");
	expect(output).toMatch(/pixie_web must default to an immutable GHCR/);
	expect(output).toMatch(/fixed container_name/);
	expect(output).toMatch(/host archive must run/);
	expect(output).toMatch(/single pixie host binary owns serve/);
	expect(output).toMatch(/public pixie_assistant service unit is removed/);
	expect(output).toMatch(/must not restart owner-lock exit 73/);
	expect(output).toMatch(/shell or shell interpolation/);
	expect(output).toMatch(/schemaVersion 2.*no public piPackage/);
	expect(output).toMatch(/absolute dataDir.*assistant settings are not allowed/);
});

test("checked-in release runtime is pinned to the glibc Bun 1.4.0 archives", async () => {
	const input = await collectCompositionInput();
	expect(input.releaseRuntimeText).toBeDefined();
	expect(input.releaseRuntimeText).toContain('BUNDLED_BUN_VERSION = "1.4.0"');
	expect(input.releaseRuntimeText).toContain("bun-linux-x64.zip");
	expect(input.releaseRuntimeText).toContain(
		"2d03fb5fb83ac8b567aca0a281b2ce1a1a19d488f56c2968d88c3f25e92fe452",
	);
	expect(input.releaseRuntimeText).toContain("bun-linux-aarch64.zip");
	expect(input.releaseRuntimeText).toContain(
		"4b1a332ee861983eb93bcfe6f770fff94e3e31b2c388bdaea3c8ed35e58eed0e",
	);

	const drifted = inspectComposition({
		...input,
		releaseRuntimeText: (input.releaseRuntimeText ?? "").replace("1.4.0", "1.3.14"),
	});
	expect(drifted.facts.docker.pinnedBunRuntime).toBe(false);
	expect(drifted.violations.join("\n")).toMatch(/stage and verify pinned Bun 1\.4\.0/);
});
