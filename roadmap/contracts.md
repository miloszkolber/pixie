# Shared implementation contracts

Owner A, with B/C/E/G. Read before parallel implementation. [Compatibility](compatibility.md) enumerates retained behavior; [acceptance](acceptance.md) defines tests. This is the canonical cross-stream contract, not another runtime service.

## Schemas and versions

Keep one authored wire catalog under `package/contracts`. Inventory actual dispatchers, callers and non-WebSocket surfaces before generating bindings. The baseline browser protocol is 88 in `package/contracts/src/ws-protocol.ts`; the assistant protocol is 1. Native Pi JSONL and MCP have separate version/support contracts. Do not label all four as one protocol.

Generate/check assistant and controller Go bindings where useful without importing another module's internal packages. The independent assistant module must remain buildable without the frontend. Include methods present in `WsMethodMap` or dispatchers but absent from `WS_METHODS`, including schedules; an enum alone is not the inventory.

For every method record boundary, inputs/results/events, caller, capability/version, authorization, state owner, mutation identity, timeout/cancellation, completion point and regression. An unclassified legacy method blocks API-01 completion. Preserve bounded unknown native fields independently of strict envelope validation.

Changing completion semantics is breaking even when JSON keys remain unchanged. Preserve host v1 where possible; otherwise introduce an explicit new negotiated host version and update all callers together. Old/new combinations outside the tested compatibility matrix fail with an actionable error. Release IDs are commit identities, not protocol or database schema versions.

Requests require correctly typed positive integer IDs within the documented safe range, nonempty method strings and object parameters. Do not coerce strings, booleans or null. Type-check results before publishing state. Specify malformed JSON, oversized frames, unsupported method, incompatible version, busy, cancelled, stale context and delivery-uncertain separately.

The existing duplicate-in-flight-ID behavior is a compatibility case: its error uses the same ID as an outstanding request. Capture that behavior and test both clients before changing it. Do not introduce two indistinguishable completions in a new protocol. Prefer closing an invalid new-protocol connection on duplicate outstanding IDs; do not silently change old-protocol semantics.

## Identity and authority

Keep these identities distinct: installed Pi executable/version; persistent assistant installation ID; assistant boot epoch; per-child generation; native session and entry IDs; tool-call and UI-request IDs; Pixie delivery/mutation IDs; workspace/resource IDs; module lifecycle generation; database schema version; release ID/full source commit.

A durable session key includes the selected Pi installation/agent-directory namespace, not only a display title or cwd. Canonicalize configured roots and reject ambiguous duplicate native IDs. A changed executable/agent directory is an explicit owner switch, not a reconnect to the previous service.

Sequences are monotonic within a stated epoch. Snapshot checkpoint and subsequent replay belong to the same owner/generation. New processes invalidate old dialog answers and expected-run operations. Stable installation identity must not make post-restart sequence zero appear to be an old duplicate event stream.

Authenticate before resolving expensive resources. A transport MCP session ID, tool argument, project label, file path, native session ID or module resource ID is not authorization. Scope Canvas to a server-issued native-session binding; scope Design deliberately to the instance read slot. Human management authority remains separate from model read authority.

## Native capability versus presentation

A capability means a complete usable operation set in the relevant installation/session, not dependency presence. Expose configured, supported, enabled, connected and ready independently. Do not require provider administration, agents or MCP in the core session handshake.

The basic model picker uses native available-model/current-model operations. It must not call a legacy provider-administration endpoint as a hidden prerequisite. Unknown credential status/cost/context metadata remains unknown, not false/zero or an empty successful catalog. `max` and future model-supported thinking values must not be filtered by the current assistant's shorter global preference enum.

Record capabilities at service and session scope; project-loaded tools/providers may differ. Configuration changes invalidate relevant catalogs and preserve saved-versus-loaded distinction. Compatibility evidence and reductions are governed by compatibility.md; a TUI workaround alone does not pass a retained Web UI feature.

## Delivery and Stop

The controller owns schedules and the durable outbox before native handoff. Pi owns accepted native steering/follow-up queues and execution. One item can be runnable in only one of those places.

Use delivery states `pending`, `dispatching`, `accepted`, `settled`, `cancelled`, and `delivery-uncertain`, with failure details separate from identity. Persist a dispatch claim before sending. If acknowledgment is lost, reconcile using supported native identities/evidence; otherwise leave uncertain and request an explicit user decision. Do not resend on reconnect, restart, timer expiry or failed status polling.

A native prompt response means accepted/queued/handled. Session completion uses the tested settled boundary, not the first agent_end. Extension commands without an LLM turn and commands that start deferred work require their own supported completion/liveness semantics; never infer completion from an arbitrary quiet interval.

Default UI Stop means stop this session's current work and automatic continuation: pause its Pixie dispatch lane, cancel pending blocking UI, clear native queued continuations, then abort. Preserve unsent outbox items in paused state and present recovered native queue text as a draft proposal, not an automatically sent prompt. Resuming dispatch or discarding drafts is explicit. A separately labelled Stop current turn action may be added only with equally explicit native queue behavior.

Do not cancel accepted work on a browser disconnect, navigation, Hide, view unmount or layout change. Service restart quiesces dispatch before child/module shutdown. Schedule pause prevents future occurrences; schedule stop terminates the current run. Neither silently deletes the schedule or an accepted run record.

## Workspace selection and scopes

One primary selection and one secondary selection have independent visibility. Primary areas: Chats, Archive, Schedules, Settings. Secondary areas: Details, Files, Git and registered modules. One reducer owns invariants and one route driver owns URL/back/forward/restoration.

Primary selection can be a native session with optional project membership, a project-owned schedule, a settings section or no selection. Secondary selection carries its resource kind and validated session/project/instance context. A session without a named project still has native cwd; no hidden project creation or transcript move is needed to list it.

Creating an ungrouped session requires an explicit validated existing cwd, defaulted from the last user-selected valid cwd. Never silently use service HOME or a different project after a failed path lookup. Resuming an existing native session uses its native cwd. Execution authority follows the trusted-user Pi connection and native project trust; file/Git admission is a separate explicit root boundary. Schedules retain project/root scope and are not available without it.

Opening Files or a module does not replace the primary conversation. Project-scoped previews survive same-project session switches; session-scoped previews do not switch their owner invisibly. Settings hides incompatible context and restores it on return only after revalidation. Design's instance-wide scope is labelled even when viewed beside a private session.

Close clears a secondary selection. Hide retains it. Focus temporarily hides the other content surface. Archive changes metadata; deletion requires confirmation; Stop changes execution. These are not aliases. Every selected item resolves to content/loading/stale/unavailable/missing/error rather than unrelated project-empty UI.

A navigation generation guards every asynchronous create/load/read. A late create may add its legitimate session to the catalog but cannot steal focus after newer navigation. Keep drafts, transcript runtime, pending dialogs and accepted runs outside component lifetimes. URL/layout storage contains bounded validated IDs/preferences, not secrets, transcripts or provider credentials.

## Module and persistence boundary

One trusted compile-time backend definition and one frontend contribution descriptor share a stable module ID/version. Frontend contributions declare rail/sidebar/view/settings and session/project/instance scope. Sidebar-only and viewer-only modules are valid. No arbitrary remote scripts, marketplace or implicit MCP App renderer.

Persist a complete candidate module map before updating committed memory. Unchanged enablement is a no-op; Restart is separate. Failed start changes readiness, not saved intent. Preserve unknown saved entries across known-module toggles without routing or executing them. Lifecycle generation prevents completion after disable/delete from restoring access.

Malformed operator configuration makes the affected module unavailable. Do not silently discard validated restrictive settings and start with defaults. Missing worker isolation similarly fails closed for untrusted processing, while authenticated retained-document status/removal remains available.

Use versioned Pixie schemas, compare-and-set for user edits and durable mutation identity for retryable writes. Atomic rename alone does not provide compare-and-set, no-clobber create or power-loss durability. Read/validate under the same ownership boundary as commit; test file and parent-directory synchronization where durability is promised. Native writers' locks must not be treated as coordinated merely because Pixie takes a different lock.

## Initial acceptance

API-01/API-02 require real legacy/native recordings, all caller classifications, strict envelope fixtures, explicit unsupported behavior, a model picker with administration absent, and correct epoch/checkpoint semantics. STATE-01 requires the scope/selection reducer and saved-state migration. MODULE-01 requires sidebar-only/viewer-only/failed-module fixtures and authority tests. Shared module composition and publication are defined in builds-and-releases.md, not reimplemented here.

Sources: [browser contracts](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/contracts/src/ws-protocol.ts), [assistant dispatcher](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/server.ts), [native RPC](https://github.com/earendil-works/pi/blob/v0.85.1/packages/coding-agent/docs/rpc.md).
