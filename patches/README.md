# Workspace patches

`@mjakl%2Fpi-subagent@3.0.1.patch` adds explicit public Pi RPC entry resolution for Bun child launches. It is needed only by the retained legacy runtime and real-child parity fixture; the Go assistant does not use it. Without it, a Bun parent launches the child without a usable script and the child fails with `Script not found "rpc"`; the patch resolves the SDK's public `rpc-entry` export and passes its path as the child script, leaving upstream's Node branch, arguments, environment and cancellation unchanged. Remove the patch with the legacy Bun path, not before its fallback/parity role ends.

The SDK-surface patch (`pi-coding-agent-0.85.1-extensions-export.patch`, the built-in extension barrel export) remains under `assistant/patches/` and is applied through root `patchedDependencies` for legacy compatibility tests.

Bun applies the patch above through the root `patchedDependencies` declaration and lockfile. Reproduce with `bun install --frozen-lockfile` using the repository's pinned Bun. To revise a patch, use `bun patch <package>@<version>`, edit the prepared package, then `bun patch --commit node_modules/<package> --patches-dir patches`. Here `--commit` is Bun's patch-generation command, not a Git commit.
