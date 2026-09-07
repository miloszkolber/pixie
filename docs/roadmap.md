# Roadmap

## Current state

Pixie is a web interface and thin application layer around vanilla Pi. Pi owns execution, sessions, providers, credentials, models and extensions; Pixie owns projects, presentation, queues, schedules, goals and Browser publication. The bridge is invisible to the model: optional packages load through Pi's native mechanisms, and basic chat needs none of them. Spot-checked live 2026-09-07 against an isolated agent dir: four path-listed upstream packages surfaced all nine native tools with zero extension errors, and a local-model turn ran the discovered `todo` tool end to end.

- One assistant service: `assistant/` runs on the host (`127.0.0.1:3284`, `pixie-assistant.service`) and ships as the published npm package `@pixie_ai/pixie-assistant` (`0.1.1` released 2026-09-07 from tag `pixie-assistant-v0.1.1` with signed provenance; `0.1.0` stays immutable).
- One container: the merged `pixie` image (`package/`, Go plus embedded web UI) serves the app on `:7312` with the in-process Browser module. Docker Compose is the primary deployment; a self-contained binary plus the published assistant package is the supported non-Docker path.
- Behavior lives with its owners: [architecture](architecture.md), [Pi integration](pi.md), [extensions](pi-extensions.md), [security](security.md), [deployment](deployment.md), [development](development.md). This file records remaining work and approval gates, not completed migrations or test counts.

## Approval boundaries

All remote publication requires explicit user signoff. Preparing code, workflow changes, packages and local dry runs does not authorize pushing branches or tags, opening an upstream pull request, publishing packages or images, or dispatching a release workflow. An approved branch push can itself trigger publication in the current container workflow, so its effects must be included in the approval request.

The first automated release (`0.1.1`, tag `pixie-assistant-v0.1.1`) completed 2026-09-07 after explicit approval: OIDC trusted publishing with signed provenance, strict pack checks, tag-stamped version. Every later release repeats the same gate: approval for the exact commit, package version, tag, destinations and workflow before triggering, then verification of tag/version agreement, package contents, provenance, version output and a fresh install. Never republish `0.1.0` or assume the next version; a manual dispatch must not publish a fallback `0.0.0-dev`.

Host state relocation is complete on this host (`PI_CODING_AGENT_DIR=/home/core/.pi`, unit `pixie-assistant.service`); the history squash to a single root is done and was revalidated before release. Both remain governed operations: do not repeat them without their own confirmation.

## Remaining work

### 1. Standalone npm subagents (next release gate)

`0.1.1` workspace installs are complete. Its tarball deliberately carries no subagent production dependency (the distribution test enforces a production baseline of native tools only), so standalone npm installs offer no subagent extension until the operator adds the pinned package; the bundled portable patch and postinstall then apply to it, guarded by the patch-freshness tests. Next: verify a real standalone install with the documented opt-in runs a child successfully, then approve version, tag and destinations for the follow-up release.

### 2. arm64 measurements

x86-64 numbers are recorded ([security](security.md) for Browser open/snapshot/close, [development](development.md) for host session-creation fixtures). Repeat the same fixtures on arm64 and record machine, runtime and version details alongside each number. arm64 stays explicitly unverified until then; publish measurements only for configurations actually exercised.

### 3. Upstream patch contribution

Propose the Bun-specific public RPC-entry resolution to [mjakl/pi-subagent](https://github.com/mjakl/pi-subagent) with the known-agent child regression from [subagent child verification](subagent-child-verification.md). No submission has been made. Do not publish any contribution without signoff. Once an upstream release passes the child tests, drop the local patch in `agent/extensions/local-patches/`.

### 4. SDK built-in extension export proposal

The pinned Pi SDK loads its built-in llama.cpp extension only inside the CLI (`--llama`) and does not export the factory from its package index. The local additive export patch (`agent/extensions/local-patches/`) already publishes the `./extensions` subpath, and the assistant loads the factory through that public path ([extensions](pi-extensions.md)). Propose the same export upstream. No submission has been made; the publication signoff gate applies. Once an upstream release exports the barrel, drop the local patch.

### Host state (complete)

This host runs `/home/core/.pi` directly via `PI_CODING_AGENT_DIR=/home/core/.pi` for interactive Pi, pixie-assistant and native children, with unit `pixie-assistant.service`. Universal documentation keeps Pi's default plus an optional override example.

## Completed milestones

- One `pixie-assistant` identity (`assistant/`, `@pixie_ai/pixie-assistant`, `pixie-assistant.service`): one MCP runtime, native baseline without optional packages, no remnants of the old host identity.
- Generic UI bridge for questions, dialogs, status and widgets; history cards are read-only recaps; unsupported TUI components are reported honestly ([extensions](pi-extensions.md)).
- Project-scoped schedules with CRUD, run-now/stop and a durable execution ledger on the existing APIs ([deployment](deployment.md), [architecture](architecture.md)).
- Read-only native Extensions inventory, distinct from MCP connections; configuration saves are explicit and deferred, and in-process reload stays gated on a supported no-install loading path ([extensions](pi-extensions.md)).
- Lifecycle and trust parity (compaction, forks, retries, default-deny project trust) covered by native-SDK tests and fixtures ([development](development.md)).
- Documented packaging and first automated release: `0.1.1` published from tag `pixie-assistant-v0.1.1` via OIDC with signed provenance and strict pack checks; Docker-primary deployment with a supported self-contained binary plus published package path and systemd units ([deployment](deployment.md)).
- Browser security review and x86-64 task-latency measurement recorded ([security](security.md)).
- History before `669955a` squashed to a single root with the tree verified byte-identical.

## Final architecture review gate

Before publication, verify from current source and fresh tests:

- A clean or existing Pi installation can use pixie-assistant with minimal setup through supported APIs, whether the chosen bridge is a service, extension or thin launcher. Its packaging and process model are explicit.
- TUI, web and child sessions use the same native resources and settings without package-specific factories changing their core capabilities.
- Pi's session files and lifecycle remain authoritative. Forks remain independent sessions, while in-session branches retain native semantics and unsupported navigation is labelled honestly.
- Generic UI works for unfamiliar extensions, reconnect restores pending work, and Stop unwinds both dialogs and generation without orphan state.
- Native extension inventory is distinct from MCP connections and Pixie's published Browser module catalog.
- Pixie authoring, schedules and read-only project presentation remain application features, without becoming an orchestration framework.
- No interactive MCP Apps, speculative compatibility aliases, duplicate state machines, hidden provider configuration or forced overlay setup return.
- Security claims match the deployed process model. In particular, shared-UID Chromium with `--no-sandbox` is not isolated from controller files merely because child environment variables are sanitized.
- Documentation and repository guidance agree with actual architecture (single merged container image, `assistant/` + `package/` layout, `pixie-assistant.service`).
