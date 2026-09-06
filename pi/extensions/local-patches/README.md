# Local upstream patches

`@mjakl%2Fpi-subagent@3.0.1.patch` adds explicit public Pi RPC entry resolution for Bun child launches. See [subagent child verification](../../../docs/subagent-child-verification.md) for the upstream proposal, real-child tests and remaining verification constraints.

Bun applies this patch through the root `patchedDependencies` declaration and lockfile. Reproduce with `bun install --frozen-lockfile` using the repository's pinned Bun. To revise it, use `bun patch @mjakl/pi-subagent@3.0.1`, edit the prepared package, then `bun patch --commit node_modules/@mjakl/pi-subagent --patches-dir pi/extensions/local-patches`. Here `--commit` is Bun's patch-generation command, not a Git commit.
