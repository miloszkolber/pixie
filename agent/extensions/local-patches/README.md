# Local upstream patches

`@mjakl%2Fpi-subagent@3.0.1.patch` adds explicit public Pi RPC entry resolution for Bun child launches. See [subagent child verification](../../../docs/subagent-child-verification.md) for the upstream proposal, real-child tests and remaining verification constraints.

`@earendil-works%2Fpi-coding-agent@0.85.1.patch` adds the `./extensions` subpath to the SDK's public export map so embedded hosts can load `builtInExtensions` (the built-in llama.cpp factory) through the public API instead of a deep file import. See the [upstream roadmap](../../../docs/roadmap.md) for the proposal state.

Bun applies both patches through the root `patchedDependencies` declaration and lockfile. Reproduce with `bun install --frozen-lockfile` using the repository's pinned Bun. To revise a patch, use `bun patch <package>@<version>`, edit the prepared package, then `bun patch --commit node_modules/<package> --patches-dir agent/extensions/local-patches`. Here `--commit` is Bun's patch-generation command, not a Git commit.
