# 01 — Shared contracts

Owner: A, with B/C/E. Starts immediately alongside FIX-01–04. This is the common boundary for parallel implementation, not a new orchestration framework.

## Deliverables

Inventory actual methods and callers at browser → controller, controller → assistant, assistant → native Pi, and native Pi → configured MCP services. Record request/result/event shapes, cancellation, error codes, idempotency, authorization, persistence owner and completion semantics. Use dispatchers as evidence; the current prose method list is not authoritative. [Baseline sources](sources.md#assistant-and-pi).

Keep one wire catalog under package/contracts. Generate bindings for the Go assistant and application where useful and check generated drift in CI. Existing handwritten interfaces can migrate method by method. Do not import package/internal/controller into the independent assistant module or create a third service for shared types.

## Identity and envelopes

Distinguish assistant version, installed Pi version, wire protocol version, stable installation identity, assistant boot epoch and per-session child generation. Native session IDs, entry IDs, tool-call IDs, user-delivery IDs and transport request IDs have different lifetimes.

Requests require correctly typed IDs, method names and parameter objects; do not coerce strings, booleans or null. Preserve bounded unknown native payload fields without relaxing envelope validation. Specify malformed-frame, unsupported-operation, incompatible-version, stale-context, oversized-output, busy and delivery-uncertain errors.

Sequence numbers are monotonic within their documented epoch. Snapshots include a checkpoint; replay contains only later events. Define reconnect to the same live child separately from recovery after assistant/child restart. No old dialog answer or expected-run command can cross a generation boundary.

Keep protocol v1 semantics while compatible. Changing a completion-oriented call into an acceptance-oriented call is breaking even if its JSON keys stay the same. Add an explicit protocol version and coordinated support if semantics cannot be preserved.

## Capability disposition

A capability means the complete operation set works in the relevant context, not that a package is installed. Distinguish configured, supported, enabled, connected and ready. Separate mandatory session capability from optional provider administration or MCP.

| Feature | Required disposition before cutover |
| --- | --- |
| Chat, images, history, model/thinking, Stop | Works through a compatible vanilla Pi installation |
| Retry, compaction, clone/fork, native queues | Native semantics and explicit settlement/recovery fixtures |
| Dialogs, notifications, string widgets/status/title | Supported native RPC primitives with scoped projection/replay |
| Editor-text requests | Supported with draft conflict handling, not silent overwrite |
| Terminal-only component factories/chrome | Explicit unsupported result with available text fallback |
| Provider login, credentials/defaults/preferences | Public native interface, optional bridge, native-TUI fallback, or explicitly approved reduction |
| Native inventory/configuration and agent authoring | Feature-by-feature public API/bridge/fallback; no hidden Go replacement policy |
| MCP, local models, delegation, plans, Signet | Optional native integrations; absence does not block chat |
| Goals/questions/schedules | Existing Pixie-owned service and state; no second scheduler or agent loop |
| Files/Git | Existing admitted-root read-only service, independent of native session discovery |

Fill this matrix with exact operations, supported Pi versions, verification evidence and fallback UX during implementation. Do not silently drop retained functionality. The legacy host may remain selectable until coverage is sufficient; never run both against one session.

Use Pi 0.85.1 as the first candidate baseline because its published RPC interface was reviewed, not as a promise that every version works. Add independently installed supported versions only after conformance tests. Keep an upstream smoke lane distinct from release-blocking tested versions.

## Delivery and settlement

Native prompt acknowledgment means accepted/queued/handled, not completed. A low-level agent_end can precede retry or continuation. Fully settled work uses the tested native settlement boundary. Extension commands can complete without an LLM turn or schedule later work. Specify each case; do not assume one event sequence for all commands.

Native Pi owns accepted steering/follow-up work. Pixie may own a durable outbox before handoff. A message cannot be runnable in both. Lost acknowledgment after dispatch becomes delivery-uncertain, never automatic retry. Define Stop current turn, Stop queued continuation and restore queued draft as distinct behaviors.

Snapshot consistency must account for events during startup, initialization and history reads. Multiple unrelated native queries are not automatically an atomic snapshot.

## Workspace selection

Agree domain types for primary area/selection, secondary area/selection, explicit context and layout preferences. Project membership is optional for native sessions; execution cwd is not optional. Preserve server-side admitted roots for file/Git authorization.

Primary selections are sessions, schedules and settings sections. Secondary selections are files, diffs and registered module resources. A selection resolves to content, loading, missing, unavailable or error. Visibility never determines session existence or dispatch ownership. Define old generic-tab migration and stale response rejection before changing the shell.

One URL driver owns browser history, restoration and validated resource identities. One reducer owns layout/selection invariants. Keep secrets, transcripts and native answer credentials out of URLs/local layout storage. [UI contract](03-workspace-ui.md).

## Module contract

Agree separate backend module definitions and frontend contributions, joined by stable module ID/version and declared context. Backend owns lifecycle, status, MCP routes and bounded services. Frontend contributes rail/sidebar/view/settings through registered trusted adapters. Context can be session, project or explicitly instance-wide.

Browser is the first implementation; Canvas and Design consume the same interface later. A caller-provided project/session ID is not authority. Authenticate context before dispatch. Canvas gets same-session tools; Design deliberately exposes one instance-wide read slot and separate user-management authority.

Persist all module settings transactionally. Unchanged enablement is a no-op. Runtime failure changes readiness, not the saved user preference. One module failure/toggle cannot restart another. Use additive catalog fields when possible and deterministic ordering. [Module contract](04-extensions.md).

## Fixtures and acceptance

Capture representative current host frames and native Pi recordings with secrets removed. Test strict envelopes, compatible/incompatible peers, acceptance/settlement, unknown events, stale generations, snapshot checkpoints and bounded output. Add pure selection reducers and fixture module contributions before implementations consume them.

K1 passes when the actual catalog, schema, capability dispositions, route/selection invariants and module/context contracts are agreed and exercised. Open gaps include exact upstream evidence and a safe fallback; they are not concealed as empty success. Any accepted change names all callers and replacement assertions.
