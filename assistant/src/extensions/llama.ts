import type { ExtensionAPI, ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";

// Load Pi's built-in provider through the current SDK export patch.
// The native CLI migration and patch-retirement gate are FC27 in
// roadmap/feature-coverage.md. The /llama management command needs a TUI;
// provider registration, model discovery and inference remain headless.
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

// A requested profile must not silently drop its provider.
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
