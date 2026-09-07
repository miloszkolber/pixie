# Session reload upstream proposal

A prepared upstream contribution for safe in-process extension reload, following the [local patches](../agent/extensions/local-patches/README.md) workflow. Status: **shelved** — the whole-host reload covers the current need ([deployment](deployment.md)), and the per-session reload flow was retired from the assistant. Revisit only if non-disruptive per-session reload becomes a requirement; submission still requires the [publication signoff](roadmap.md).

## How reload works today

Saving native extension configuration reports `saved: true, loaded: false, reload: deferred`. Configured changes apply when a session reopens, or everywhere at once through the whole-host reload ([extensions](pi-extensions.md)). In-flight runs interrupt on a host reload; session transcripts stay durable on disk.

## Why in-process reload was deferred

Two pinned-SDK mechanics made a safe per-session reload impossible:

- `AgentSession.reload()` calls the process-global `resetApiProviders()`, which cannot reset one session's providers safely while other sessions hold theirs.
- `DefaultResourceLoader.reload()` resolves packages without the install-policy callback the initial load accepts, so a preflight check cannot exclude a concurrent configuration or filesystem change that starts an installation during reopening.

The assistant does not patch private loader state, mutate process-wide environment, or run a second loading engine to bypass these boundaries.

## Proposed upstream change

1. Scope provider reset to the reloading session, or provide a re-entrant per-session variant of `resetApiProviders()`, so `AgentSession.reload()` becomes safe beside resident sibling sessions.
2. Accept an install-policy callback on `DefaultResourceLoader.reload()` with the same shape the initial load takes, so reload preflight can forbid mid-reload package installation.

Both changes are small, additive and match the SDK's existing loading model; neither changes default behavior.

## Verification plan

If the proposal is ever revived: `bun run check:parity` passes on an upstream release carrying both changes, then a reload integration test applies a saved configuration to one idle session without disturbing a sibling session's run, listeners or MCP membership.
