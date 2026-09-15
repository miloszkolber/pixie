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
RUN go build -o /out/pixie_full ./cmd/pixie-full
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
FROM runtime AS pixie
COPY --from=pi-build /out/pixie_assistant.js /app/libexec/pixie_assistant.js
COPY --from=pi-build /out/runtime /app/runtime
COPY --from=go-build /out/pixie /app/pixie
COPY --from=go-build /out/pixie_full /app/libexec/pixie_full
COPY --from=go-build /out/pixie_web /app/libexec/pixie_web
RUN test -x /app/runtime/bin/bun \\
    && test -f /app/runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js \\
    && test -f /app/libexec/pixie_assistant.js \\
    && test ! -e /app/runtime/node_modules/.bin \\
    && ! find /app/runtime -iname '*rpc-entry*'
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/libexec/pixie_full"]
CMD ["serve", "--assistant-config", "/etc/pixie/assistant.json", "--web-config", "/etc/pixie/pixie.json"]
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

test("Docker composition separates the controller-only and full-suite images", () => {
	const report = inspectComposition(composition(controllerDockerfile));

	expect(report.ok).toBe(true);
	expect(report.facts.docker.controllerOnlyBuild).toBe(true);
	expect(report.facts.docker.explicitControllerEntrypoint).toBe(true);
	expect(report.facts.docker.effectiveInit).toBe(true);
	expect(report.facts.docker.noAssistantRuntime).toBe(true);
	expect(report.facts.docker.fullSuiteBuild).toBe(true);
	expect(report.facts.docker.fullServiceEntrypoint).toBe(true);
	expect(report.facts.docker.fullRuntimeClosure).toBe(true);
	expect(report.facts.docker.pinnedBunRuntime).toBe(true);
	expect(report.facts.docker.noNodeRuntime).toBe(true);
	expect(report.facts.docker.noRootPixieService).toBe(true);
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

test("checked-in Docker and controller composition report static facts without live-artifact claims", async () => {
	const input = await collectCompositionInput();
	const report = inspectComposition(input);
	const output = formatCompositionReport(report);

	expect(report.ok).toBe(true);
	expect(input.dockerfileText).toContain("-o /out/pixie_web ./cmd");
	expect(input.dockerfileText).toContain("FROM controller-runtime AS pixie_web");
	expect(input.dockerfileText).toContain("FROM controller-runtime AS pixie");
	expect(input.dockerfileText).toContain("/app/pixie_web");
	expect(report.facts.docker.controllerOnlyBuild).toBe(true);
	expect(report.facts.docker.explicitControllerEntrypoint).toBe(true);
	expect(report.facts.docker.effectiveInit).toBe(true);
	expect(report.facts.docker.fullSuiteBuild).toBe(true);
	expect(report.facts.docker.fullServiceEntrypoint).toBe(true);
	expect(report.facts.docker.fullRuntimeClosure).toBe(true);
	expect(report.facts.docker.pinnedBunRuntime).toBe(true);
	expect(report.facts.docker.noNodeRuntime).toBe(true);
	expect(report.facts.docker.noRootPixieService).toBe(true);
	expect(report.facts.controller.uiEmbed).toBe(true);
	expect(report.facts.deployment).toEqual({
		composeUsesWebImage: true,
		composeUsesFullImage: true,
		composeTopologiesAreExclusive: true,
		composeHasSeparatePiState: true,
		composePiStateInitIsBounded: true,
		composeFullServiceIsNonRoot: true,
		composeFullWaitsForPiStateInit: true,
		composeHasNoFixedContainerNames: true,
		archiveServiceUsesInternalFullCommand: true,
		cliServiceUsesStandaloneCommand: true,
		noPublicAssistantUnit: true,
		ownerLockDoesNotRestart: true,
		secretsInherited: true,
		absoluteConfigPaths: true,
		noShellInterpolation: true,
	});
	expect(output).toMatch(/missing live evidence/);
});

test("deployment composition rejects a bypassable or over-privileged Pi-state initializer", async () => {
	const input = await collectCompositionInput();
	const unsafeCompose = input.composeFileText
		?.replace("network_mode: none", "network_mode: host")
		.replace(
			'entrypoint: ["/usr/bin/tini", "-s", "--", "/usr/bin/install"]',
			'entrypoint: ["/bin/sh", "-c"]',
		)
		.replace(
			'command: ["-d", "-m", "0700", "-o", "1000", "-g", "1000", "/var/lib/pixie/pi"]',
			'command: ["chown -R 1000:1000 /var/lib/pixie"]',
		)
		.replace("            - FOWNER", "            - SYS_ADMIN")
		.replace(
			"                condition: service_completed_successfully",
			"                condition: service_started",
		)
		.replace("                required: true", "                required: false");
	if (unsafeCompose === undefined) {
		throw new Error("checked-in docker-compose.yaml is required for this test");
	}

	const report = inspectComposition({ ...input, composeFileText: unsafeCompose });

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toMatch(
		/bounded root-only, no-network Pi-state initializer/,
	);
	expect(report.violations.join("\n")).toMatch(/require successful pixie_pi_state_init completion/);
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
    pixie:
        profiles: ["full"]
        image: ghcr.io/example/pixie:latest
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
ExecStart=/bin/sh -c '%h/.local/bin/pixie serve --assistant-config ~/assistant.json --web-config $HOME/pixie.json'
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
	expect(output).toMatch(/pixie must default to an immutable GHCR/);
	expect(output).toMatch(/only the two immutable GHCR Pixie product images/);
	expect(output).toMatch(/fixed container_name/);
	expect(output).toMatch(/full archive must run/);
	expect(output).toMatch(/public pixie_assistant service unit is removed/);
	expect(output).toMatch(/must not restart owner-lock exit 73/);
	expect(output).toMatch(/shell or shell interpolation/);
	expect(output).toMatch(/schemaVersion 2.*no public piPackage/);
	expect(output).toMatch(/absolute dataDir.*assistant settings are not allowed/);
});

test("full image rejects a missing verified runtime or a root pixie service command", () => {
	const missingRuntime = controllerDockerfile
		.replace("COPY --from=pi-build /out/runtime /app/runtime\n", "")
		.replace(
			'ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/libexec/pixie_full"]',
			'ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/pixie", "serve"]',
		);
	const report = inspectComposition(composition(missingRuntime));

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toMatch(/stage the verified release runtime/);
	expect(report.violations.join("\n")).toMatch(/must never run root `pixie serve`/);
});

test("full image rejects a Node runtime, an unverified Bun, and a missing Pi closure or assistant", () => {
	const nodeRuntime = controllerDockerfile
		.replace(
			"COPY --from=pi-build /out/runtime /app/runtime",
			"COPY --from=pi-build /out/runtime /app/runtime\nRUN test -x /app/runtime/node/bin/node",
		)
		.replace(
			"RUN bun -e 'stageBundledPiRuntime(); verifyBundledPiRuntime();'",
			"RUN bun -e 'stageBundledNodeRuntime(); verifyBundledPiRuntime();'",
		);
	const nodeReport = inspectComposition(composition(nodeRuntime));
	expect(nodeReport.facts.docker.noNodeRuntime).toBe(false);
	expect(nodeReport.violations.join("\n")).toMatch(/must not reintroduce a bundled Node runtime/);

	const unverifiedBun = controllerDockerfile.replace("RUN test -x /out/runtime/bin/bun\n", "");
	const unverifiedReport = inspectComposition(composition(unverifiedBun));
	expect(unverifiedReport.facts.docker.pinnedBunRuntime).toBe(false);
	expect(unverifiedReport.violations.join("\n")).toMatch(/stage and verify pinned Bun 1\.4\.0/);

	const missingClosure = controllerDockerfile
		.replace(
			"&& test -f /app/runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js \\\n",
			"",
		)
		.replace("&& test -f /app/libexec/pixie_assistant.js \\\n", "");
	const missingReport = inspectComposition(composition(missingClosure));
	expect(missingReport.facts.docker.fullRuntimeClosure).toBe(false);
	expect(missingReport.violations.join("\n")).toMatch(/Pi closure, the portable assistant bundle/);
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
