# Session reload upstream proposal

Prepared upstream contribution for safe in-process extension reload, following the [local patches](../agent/extensions/local-patches/README.md) workflow. No submission has been made; the [publication signoff gate](roadmap.md) applies.

## Current behavior

Saving native extension configuration reports `saved: true, loaded: false, reload: deferred`, and "check session reload" reports `session-busy`, `session-not-resident` or `sdk-loader-install-policy` instead of applying ([extensions](pi-extensions.md)). Configured changes reach sessions only when they are closed and reopened; idle resident sessions keep their previous load set.

## Upstream blocker

Two pinned-SDK mechanics force the deferral:

- `AgentSession.reload()` calls the process-global `resetApiProviders()`, which cannot reset one session's providers safely while other sessions hold theirs.
- `DefaultResourceLoader.reload()` resolves packages without the install-policy callback the initial load accepts, so a preflight check cannot exclude a concurrent configuration or filesystem change that starts an installation during reopening.

The assistant does not patch private loader state, mutate process-wide environment, or run a second loading engine to bypass these boundaries.

## Proposed upstream change

1. Scope provider reset to the reloading session, or provide a re-entrant per-session variant of `resetApiProviders()`, so `AgentSession.reload()` becomes safe beside resident sibling sessions.
2. Accept an install-policy callback on `DefaultResourceLoader.reload()` with the same shape the initial load takes, so reload preflight can forbid mid-reload package installation.

Both changes are small, additive and match the SDK's existing loading model; neither changes default behavior.

## Verification plan

Local today: repeated deferred-reload checks neither add listeners nor replace capabilities or MCP runtimes (`tests/pixie-assistant/server.test.ts`), and idle sessions stay unchanged. After an upstream release: `bun run check:parity` passes, then a reload integration test flips an idle session's `reload: deferred` to an applied reload with preserved MCP membership and capability set.
