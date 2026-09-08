# Go assistant implementation plan

Owner B, with A/E/G. Replace the TypeScript service with one shared Go runtime supervising the selected host Pi. Ship it as pixie-assistant for Docker and inside the complete host pixie binary. Read [contracts](contracts.md), [compatibility](compatibility.md), [migration](migration.md) and [builds](builds-and-releases.md); their exact decisions govern this implementation.

The current assistant embeds a pinned SDK under Bun. Native subprocess RPC has different response semantics. Adapt observable behavior rather than translating TypeScript line by line. Source evidence is in [sources](sources.md#assistant-and-native-pi) and [second pass](second-pass-review.md).

## 1. Runtime ownership

```text
Docker: Browser → Docker controller → host pixie-assistant → selected host Pi
Host:   Browser → pixie [controller + same assistant engine + embedded UI] → selected host Pi
```

Pi owns native execution/configuration/resources/trust. The assistant owns executable discovery, native transport, supervision, catalog and projections. Controller owns workspace, file/Git inspection, schedules and pre-handoff durable outbox. Logical owners remain separate in the combined process.

Neither binary bundles another Pi SDK/runtime, agent loop, provider policy or Pi MCP client. Keep native tools, prompts and resource resolution unchanged. No forced extension bundle, silent install/upgrade or automatic project trust. Optional application-side workers are separate from Pi.

The combined binary does not launch a separate pixie-assistant executable. It initially connects to its shared engine through a private authenticated ephemeral loopback endpoint, reusing the tested host contract. The secret stays internal and is not inherited by Pi or exposed through the UI router. Docker explicitly uses external mode and never performs native discovery/startup itself.

## 2. Source structure and shared facade

Retain assistant/go.mod and package/go.mod; no root-module/directory migration.

```text
assistant/
  cmd/pixie-assistant/main.go
  host/                       public composition facade
  internal/config/
  internal/discovery/
  internal/pirpc/
  internal/supervisor/
  internal/catalog/
  internal/projection/
  internal/hostapi/
  internal/service/
  internal/wire/              generated shared-contract bindings
  testdata/
  systemd/pixie-assistant.service
  go.mod
  go.sum
package/cmd/pixie/             embedded or external-assistant composition
```

The facade exposes configuration, startup, readiness, transport and bounded shutdown without controller dependencies. Libraries return lifecycle intent/errors; entrypoints own flags, signals and exit. Never import assistant/internal from the application, copy internals or call os.Exit from the facade. Build both variants early using a fake facade while native features progress.

Resolve the assistant module from the exact checkout. Keep standalone dependency closure small and buildable without Svelte assets. One authored catalog under package/contracts produces checked bindings where appropriate. The baseline browser/host/native/MCP protocols and release ID are distinct. Capture actual callers before changing host v1; a completion-to-acceptance change needs a coordinated versioned contract.

Colocate new Go unit tests and retain existing package/tests integrations. Keep packages tied to real boundaries rather than inventing an extensible agent framework.

## 3. Discovery and configuration

Resolve explicit piExecutable, otherwise pi from service PATH, and pin the resolved absolute executable for the service lifetime. Record non-secret installation identity/version and detect changed executable distribution at an explicit refresh/restart boundary rather than silently mixing installations in one session. Support standalone/npm/symlink/custom-prefix paths and spaces; do not rely on interactive aliases.

Normalize startup configuration once: explicit CLI, config, supported environment, then defaults. Use Pi's native default agent directory and PI_CODING_AGENT_DIR. Configure native argv as an array, never shell text. Reserve mode, session and cwd selection for the supervisor and reject conflicting argv. Optional native flags such as local-model bootstrap remain opt-in and version-tested.

Preserve intended HOME/cwd/agent directory and operator-supplied provider/tool environment. Browser's stripped environment is not appropriate for native Pi. Remove assistant/controller auth credentials from children; explicitly scoped native integrations receive only their own required credentials. Do not place secrets in argv, logs, snapshots, URLs or diagnostics.

Standalone serve and doctor, and full-host composition/config/service templates, are defined in builds-and-releases.md. Doctor is read-only by default: executable/config checks, no model call, installation, native writes or project extension loading. An active compatibility probe is separately explicit about effects. Distinguish missing executable/interpreter, unsupported interface, unreadable state, no provider and unavailable optional features.

Invalid service configuration/auth or conflicting ownership fails safely. Missing Pi/provider does not destroy the combined shell or cause an uncontrolled restart loop: expose accurate agent readiness and disable dispatch. Do not silently attach to another daemon or change the selected cwd/executable. Keep host API loopback-only and reject browser-origin connections.

## 4. Native transport, settlement and Stop

One reader and serialized writer per child own JSONL framing and request correlation. Bound pending maps, request/output bytes, deadlines, diagnostic buffers and waiting consumers. Drain stdout independently; stderr is bounded redacted diagnostics, not another protocol stream. Child exit fails outstanding calls.

Split only on LF, strip optional trailing CR and preserve Unicode separators in JSON strings. Explicitly configure a bounded reader beyond Go Scanner's default 64 KiB limit. Never truncate JSON or split a record into invented messages. Test fragmented/coalesced lines, Unicode/CRLF, malformed/oversized output, large images, stdout pollution, slow/broken pipes and cancellation.

Type strict envelopes while retaining bounded unknown native payload fields as raw JSON. Unknown events neither crash the adapter nor acquire guessed semantics. Separate native request/session/entry/tool IDs from Pixie delivery/transport IDs. Observe compatibility rules for duplicate outstanding IDs.

Native prompt acknowledges accepted/queued/handled work. It is not session completion. The reviewed native agent_settled boundary differs from agent_end; preserve a completion-oriented legacy call until actual settlement or explicitly version the new behavior. Test ordinary prompts, no-LLM commands, extension-triggered deferred work, retry, manual/automatic compaction, queue continuations, errors, cancellation and child death. No generic quiet timer is sufficient for every command.

Pi owns accepted native queues; controller outbox owns work before handoff. Persist dispatch claims, represent lost acknowledgment as delivery-uncertain and never resend automatically on reconnect/restart. One item cannot be runnable in both owners. Do not claim exactly-once tool effects.

Default Stop follows contracts.md: pause the Pixie dispatch lane, settle pending UI, clear native continuation queues, then abort. Keep unsent outbox work paused and recovered native queue text as an explicit draft proposal. Resume/discard is a user decision. Do not let queues continue after a control described as stopping all session work.

## 5. Native catalog and persistence

Index native JSONL read-only with bounded enumeration/concurrency, metadata caching, incremental scanning and revalidation after external edits/replacement. Preserve native ID/path/cwd/parent/active leaf and unknown/custom/summary entries. An incomplete final record is a recoverable tail, not permission to truncate history. Distinguish corruption from an empty session and show bounded diagnostics.

Duplicate native IDs are ambiguous; do not pick the first file. Project membership/archive are Pixie metadata and do not change native cwd. Discovering a session does not admit files or mount a Docker path. Ungrouped create needs a validated user-selected cwd; never fall back to service HOME after failure. Schedules remain project/root-scoped.

| Operation | Direction |
| --- | --- |
| Create/reopen | Native startup/new_session/switch_session; respect cancellation |
| Model/thinking | Native available-model/current-state/supported-level queries and setters, independent of optional provider admin |
| Rename | Native set_session_name |
| Clone/fork | Native clone at current position, or get_fork_messages/fork earlier prompt; distinct UI and preserved cancellation/text |
| History/tree | Native entries/active leaf for residents; bounded read-only discovery index; inspection does not imply unsupported tree-navigation control |
| Compact/stats | Native compact/get_session_stats with unknown values preserved |
| Archive/grouping | Pixie store, imported from the existing sidecar where needed |
| Delete | Explicit confirmed action after stopping/releasing work and exact owner/path/file identity revalidation |

Do not synthesize headers or rebuild branch JSONL in Go. Unpersisted empty conversations remain drafts until native durable identity is available; reconcile once. Fork/clone may change the child session identity: transfer ownership, keep original independent, and reopen source only under correct identity.

Preserve existing pixie-input and plan presentation entries when reading history. New UI-only attachment metadata belongs in Pixie state and uses stable native/delivery associations, not matching identical prompt text heuristically or modifying old transcripts. Test old sessions with images/resources and repeated same-text prompts.

Use shared stable installation/agent-directory and per-managed-session ownership in both variants. This coordinates Pixie with itself, not an independent vanilla TUI. Explicit idle handoff releases the managed writer before native resume. The transition from the old proper-lockfile service needs tested coordination; a differently implemented lock on a similar path is insufficient.

## 6. Supervision and shutdown

Serialize per-session state changes. Track process generation, pending requests, agent activity, UI, supported background pins, residency and termination. Spawn on demand, not per catalog row. Bound concurrent starts and idle residency; measure total Pi-child cost before copying old SDK-host limits.

Active work and pending UI cannot be evicted. Unknown detached-work liveness is conservative: do not assume prompt idle means no extension work. The generic optional bridge can report versioned pins without affecting native run settlement. Explicit Stop/release/service teardown still has defined authority.

Browser disconnect removes subscriptions, not execution. Controller loss preserves accepted runs and bounded projections while the child lives. Child death invalidates callbacks and leaves interrupted/uncertain work explicit; no automatic generation restart with the original prompt.

On service shutdown stop new dispatch, cancel/settle UI as defined, request native abort, wait within a deadline, terminate remaining managed process groups, finish owned metadata and exit. systemd provides final descendant cleanup; foreground behavior still needs managed-group tests. Never signal unrelated user TUI processes. Errors in extension shutdown cannot wedge the service indefinitely.

Restart follows the same path and returns lifecycle intent to the command/composition owner. Combined shutdown first quiesces schedules/outbox and coordinates modules/controller/children. Repeated restart is idempotent and the real executable must obtain a new PID/boot epoch. Injected callback-only tests are not sufficient. Docker's separate Browser descendants use the final-image lifecycle tests, not assumptions about host systemd.

## 7. Projection and reconnect

Assemble partial content by native content index/tool-call ID, with final native messages authoritative. Preserve thinking/text/images/tool outputs/errors/usage and visible custom/summary messages. Hidden native context remains hidden; unknown usage is not zero. Compare native final content rather than appending a duplicated unstreamed tail.

Stable installation ID, assistant boot and child generation are distinct. Snapshot/checkpoint and buffered subsequent events share one owner. Multiple get_state/get_entries calls are not inherently atomic; reconcile native leaf/events or remain visibly restoring. Subscribe/bootstrap before admitting prompts so startup events are not lost.

Keep valid pending UI requests and original deadlines while the native child lives. Reconnect does not reset timeouts. Old-generation answers fail. Passive statuses/widgets are bounded presentation, not run ownership; preserve/replay supported state deliberately, without treating history recaps as live dialogs. Draft conflicts follow compatibility.md.

Page and encode histories incrementally; avoid serializing all messages merely to decide chunking. Bound aggregate output and a single huge record/image. A limit returns an explicit partial/limit result consistent with protocol, not corrupt JSON or invented transcript entries. Use authenticated image/artifact references for browser display where appropriate without changing native content.

## 8. Retained capabilities and bridge

Implement every CP row in compatibility.md. Native RPC is sufficient for much core work but not automatically for provider authentication/defaults/preferences, resource administration, all MCP management or liveness. API-03 proves the optional generic native bridge early against public APIs from the selected Pi distribution. Do not assume its ExtensionContext exposes the legacy AgentSession/ModelRuntime or import Pixie's dev SDK as fallback.

No reductions are preapproved. A missing managed feature blocks supported-profile cutover; TUI instructions are diagnostic fallback, not a completed replacement. Unsupported installations keep Vanilla features and clear capability diagnostics. Do not make the bridge a mandatory prerequisite for basic chat.

Preserve existing scoped agent Markdown authoring, including no-clobber/CAS and unknown fields; this can be ported to host Go without becoming a delegation engine. Native optional subagents/todo/web/Signet/local models still execute under Pi. Verify real results, native child startup and hidden recall rather than a model's claim or a package marker.

Reassess Bun child-launch and SDK llama-export patches against the selected executable. Keep required legacy support until tested retirement; no automatic patching of independent user packages. Nondisruptive per-session reload remains outside the current requirement unless a verified retained behavior needs it.

## 9. Builds and units

Both binaries use the facade and exact source commit. Unit templates, exit status 75 for requested restart, readiness, private config, architecture matrix, archive contents and publication are defined only in builds-and-releases.md. Do not maintain another unit or release-version recipe here.

Assistant-only needs Go, not UI compilation. Full-host embeds verified real assets and works without a separate assistant or frontend runtime. Pi's own runtime remains a separate prerequisite. Optional worker runtimes do not enter the standalone dependency closure. Build success alone is not native architecture compatibility.

## 10. Migration and acceptance

Order: capture legacy/native conformance; facade/discovery/JSONL plus API-03; one Vanilla session through both commands; catalog/history/reconnect/clone/fork/images; settlement/queues/UI and full managed CP coverage; state migration and actual artifacts. The UI slice can progress against agreed fixtures independently.

Follow migration.md for all Pixie sidecars, controller stores, locks, state versions and topology changes. Native transcripts/configuration are not conversion targets. Do not rewind execution or deletion ledgers during rollback. Legacy host may remain explicitly selectable until cutover but cannot be secretly packaged into a Go release as parity.

Accept only with CP-01–17, AC-01–14 and both release topologies/architectures as specified in acceptance.md. Measure controller/shared engine/all Pi children and optional workers: startup, catalog, cold/warm large history, concurrent sessions, idle/active/peak RSS, shutdown and repeated p50/p95. Ownership and distribution motivate this rewrite; a performance improvement requires actual measurement.
