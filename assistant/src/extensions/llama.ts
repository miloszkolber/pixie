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
// Loading: the SDK publishes the factory as `builtInExtensions` in its
// extensions barrel, but the pinned 0.85.1 package does not export that
// barrel from its public index. An additive upstreamable export patch
// (`assistant/patches/`, applied by Bun patchedDependencies and
// by the assistant postinstall for standalone installs) publishes the
// `./extensions` subpath, and this bridge imports the factory through it.
// The import degrades to "unavailable" when the patch is absent so installs
// never break, while a requested `--llama` profile fails loudly at startup
// instead of silently dropping the provider. Exporting the barrel from the
// SDK index itself is the upstream contribution tracked in roadmap/compatibility.md;
// once upstream exports it, this file needs no local patch.
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

const upstreamLlama: ExtensionFactory | undefined = await import(
	"@earendil-works/pi-coding-agent/extensions"
)
	.then(({ builtInExtensions }) => {
		const entry = builtInExtensions.find(
			(extension) => "name" in extension && extension.name === "llama.cpp",
		);
		return entry && "factory" in entry ? entry.factory : undefined;
	})
	.catch(() => undefined);

// Loud failure at host startup when --llama is requested but the pinned SDK
// does not expose its built-in llama.cpp extension through the public export.
export function llamaFactory(): ExtensionFactory {
	if (!upstreamLlama)
		throw new Error(
			"--llama requested, but the pinned Pi SDK does not expose its built-in llama.cpp extension. Reinstall with the SDK export patch applied (bun install re-applies it through patchedDependencies).",
		);
	return upstreamLlama;
}

export default function llamaExtensionBridge(pi: ExtensionAPI): void {
	llamaFactory()(pi);
	registerCapability(pi, { id: "llama", version: 1, operations: {} });
}
