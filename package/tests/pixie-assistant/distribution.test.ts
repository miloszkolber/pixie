import { expect, test } from "bun:test";
import { mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

test("packed source starts with production dependencies and no optional packages", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-assistant-package-"));
	const run = async (args: string[], cwd: string) => {
		const process = Bun.spawn(args, {
			cwd,
			stdout: "pipe",
			stderr: "pipe",
			signal: AbortSignal.timeout(90000),
		});
		const [stdout, stderr, code] = await Promise.all([
			new Response(process.stdout).text(),
			new Response(process.stderr).text(),
			process.exited,
		]);
		if (code !== 0) throw new Error(`${args.join(" ")} failed (${code}): ${stderr}\n${stdout}`);
		return stdout;
	};
	try {
		await run(
			[process.execPath, "pm", "pack", "--destination", root],
			new URL("../../../assistant", import.meta.url).pathname,
		);
		const archive = (await readdir(root)).find((name) => name.endsWith(".tgz"))!;
		await writeFile(
			join(root, "package.json"),
			JSON.stringify({
				private: true,
				dependencies: { "@pixie_ai/pixie-assistant": `file:./${archive}` },
			}),
		);
		await run([process.execPath, "install", "--production", "--ignore-scripts"], root);
		const entry = join(root, "node_modules", "@pixie_ai", "pixie-assistant", "dist", "main.js");
		const manifest = JSON.parse(
			await Bun.file(new URL("../../../assistant/package.json", import.meta.url).pathname).text(),
		);
		const sdk = manifest.dependencies["@earendil-works/pi-coding-agent"];
		expect(await run([process.execPath, entry, "--version"], root)).toContain(
			`pixie-assistant ${manifest.version} (Pi SDK ${sdk})`,
		);
		const probe = `
			import { createRequire } from "node:module";
			import { startHost } from ${JSON.stringify(entry.replace("main.js", "server.js"))};
			const require = createRequire(${JSON.stringify(entry)});
			for (const name of ["pi-mcp-adapter", "@mjakl/pi-subagent", "@signetai/connector-pi", "@juicesharp/rpiv-todo", "@juicesharp/rpiv-web-tools", "@juicesharp/rpiv-ask-user-question"]) {
				let present = false;
				try { require.resolve(name); present = true; } catch {}
				if (present) throw new Error("Optional runtime dependency: " + name);
			}
			const agentDir = ${JSON.stringify(join(root, "state"))};
			process.env.PI_CODING_AGENT_DIR = agentDir;
			const host = await startHost({ agentDir, port: 0, secret: "isolated-package-secret" });
			try {
				if (host.capabilities.mcp || host.capabilities.signet) throw new Error("False optional capability");
				if (host.control.session.getActiveToolNames().join(",") !== "read,bash,edit,write") throw new Error("Native tools changed");
				console.log("production baseline ready");
			} finally { await host.close(); }
		`;
		expect(await run([process.execPath, "--eval", probe], root)).toContain(
			"production baseline ready",
		);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
}, 120000);
