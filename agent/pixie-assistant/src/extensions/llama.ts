import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import type { ExtensionAPI, ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";

// Optional local provider bootstrap (`--llama`).
//
// Uses the Pi SDK's own built-in llama.cpp extension unchanged: it registers
// the `llama.cpp` provider (OpenAI-completions against `LLAMA_BASE_URL`,
// default `http://127.0.0.1:8080`) plus the `/llama` model-management
// command. This bridge only loads that factory and advertises an additive
// capability marker so operators can verify the profile loaded. It adds no
// tools, prompts, or interception; model selection keeps flowing through the
// existing `pi.providers.*` and `session.configure` operations.
//
// Loading detour: the SDK publishes the factory as `builtInExtensions` inside
// `dist/extensions/index.js`, but that module is not re-exported from the
// package index, so embedded hosts cannot import it through the public API.
// This bridge therefore resolves the installed package directory (via the
// always-resolvable `package.json`, independent of install layout or working
// directory) and loads the file directly by URL. If the SDK ever moves the
// file, the profile fails loudly at startup instead of silently dropping the
// provider. Exporting `builtInExtensions` from the SDK index would remove
// this detour and is a candidate upstream contribution.
//
// Headless note: the extension's `/llama` management command renders through
// Pi TUI components and needs an interactive terminal. Provider registration,
// model catalog refresh, and inference are UI-free and work headless; the
// `/llama` command itself is unsupported in Pixie until Pi offers a generic
// host UI for it (the generic `notify` path already works through the Pixie
// UI bridge).
//
// Operator setup: give the host `LLAMA_BASE_URL` (e.g. in the service
// `Environment=` or `.pixie`), then pick the provider per session with
// `session.configure` (`provider` → `llama.cpp`, `model` → model id). Auth
// uses the stored `llama.cpp` credential when present and falls back to the
// `LLAMA_API_KEY` environment or the dummy key `local`, which llama.cpp
// ignores.

const packageDir = dirname(
	createRequire(import.meta.url).resolve("@earendil-works/pi-coding-agent/package.json"),
);
const factoryUrl = pathToFileURL(join(packageDir, "dist", "extensions", "llama", "index.js")).href;
const { default: upstreamLlama }: { default: ExtensionFactory } = await import(factoryUrl);

export default function llamaExtensionBridge(pi: ExtensionAPI): void {
	upstreamLlama(pi);
	registerCapability(pi, { id: "llama", version: 1, operations: {} });
}
