# 02 — Go assistant

Owner: B; API with A, native UI with E, delivery with G. Replace the TypeScript assistant with a reusable Go engine that supervises the selected host Pi. Ship it both as pixie-assistant for the Docker interface and inside the complete host pixie binary. Do not port Pi or embed another JavaScript runtime. [Reviewed source and native RPC](sources.md#assistant-and-pi).

## Runtime ownership and the two compositions

```text
Docker installation:
  Browser UI → Docker Pixie controller → host pixie-assistant → installed Pi

Direct-host installation:
  Browser UI → pixie [controller + same assistant engine + embedded UI] → installed Pi
```

In both: the controller owns workspace, files/Git, schedules and durable outbox; the assistant engine owns discovery, native RPC, supervision and projections; installed Pi owns native sessions/configuration, agent execution, tools/extensions and its optional MCP client.

The standalone binary contains no controller/UI. The combined binary starts both engines in one process and one service without installing or launching a separate pixie-assistant executable. Native Pi processes and the runtime required by the user's Pi are still separate dependencies. Do not require Bun merely to run either core build. Do not change the frontend build toolchain as part of the assistant port.

Keep model-routing policy, tool interception, replacement system prompts, schedule timing, provider databases and a second MCP client for Pi outside the assistant. Preserve the distinction between native configuration and Pixie presentation metadata. [Build/composition contract](07-build-release.md).

## Source layout

Keep assistant/go.mod and package/go.mod. No root-module migration or broad source renaming is needed.

```text
assistant/
  cmd/pixie-assistant/main.go
  host/                       public service facade used by both commands
  internal/config/
  internal/discovery/
  internal/pirpc/
  internal/supervisor/
  internal/catalog/
  internal/projection/
  internal/hostapi/
  internal/service/
  internal/wire/
  testdata/
  systemd/pixie-assistant.service
  go.mod
  go.sum
package/
  cmd/pixie/                  combined or explicit external-assistant composition
  internal/controller/
  webui/                     compiled assets embedded into the combined binary
```

The host facade exposes construction, readiness, endpoint and bounded shutdown. It must not call os.Exit, parse CLI flags or import controller internals. Commands own process signals/exit; the embedded composition coordinates shutdown with the controller and schedules. Return restart intent to that owner rather than terminating from a library callback. Both commands must exercise the actual production restart path in tests.

Resolve the assistant dependency from the exact repository checkout. Do not copy its internals into package/ or import through Go's internal boundary. Keep standalone dependencies small. Initially combined mode uses the same host protocol over an authenticated private ephemeral loopback endpoint owned by the one pixie process; standalone mode uses the operator-configured authenticated endpoint. The combined boot credential stays internal. Do not create another wire catalog or skip semantic checks in embedded mode.

Keep packages tied to real boundaries. Colocate Go unit tests with new code and preserve controller/deployment integration tests under package/tests. Shared schema ownership is in [01](01-contracts.md). Implement the facade and its fake in the first vertical slice so G can build both variants while B completes native features.

## Discovery and configuration

Resolve explicit piExecutable first, otherwise pi on the service PATH. Pin the resolved absolute path for the service lifetime. Support npm-installed, standalone and symlinked executables, custom prefixes and paths with spaces. A systemd service does not run login-shell aliases or initialization scripts.

Proposed standalone CLI:

```text
pixie-assistant serve --config /absolute/path/assistant.json
pixie-assistant doctor --config /absolute/path/assistant.json
pixie-assistant --version
```

Combined pixie embeds the same assistant configuration under its own config and exposes the same diagnostics without a second service; see 07. Configuration covers piExecutable, agentDir, listen/secretFile where external, explicit piArgs and limits. Use Pi's native default directory and PI_CODING_AGENT_DIR override. Define precedence once: explicit CLI, configuration, supported environment, defaults. Report resolved non-secret values, not credentials.

piArgs is argv, never a shell string. Reserve mode/session/working-directory selection for the supervisor and reject conflicts. Version-test optional native CLI features such as local-model flags; never inject a mandatory extension bundle.

Keep HOME, selected agent directory, native cwd and operator-provided provider/tool environment consistent with Pi. Do not apply Browser's stripped environment to the host agent. Remove assistant/controller service credentials from children unless a particular native integration explicitly needs its own scoped credential. No secrets in command lines, snapshots, logs or controller state.

Doctor defaults to read-only configuration/executable checks, without installing packages, prompting a model or accidentally loading project extensions. An active compatibility probe is explicit about its effects. Distinguish missing executable, interpreter failure, invalid directory, unsupported behavior and missing provider configuration. Do not auto-install/upgrade Pi. External-assistant Docker mode must not attempt discovery or spawn a local Pi.

## Native transport

Use one reader and one serialized writer per child, correlated request IDs, bounded pending calls, deadlines and explicit child-exit failures. Drain stdout independently of slow consumers. stderr is bounded redacted diagnostics, not a second protocol stream.

Parse LF-delimited JSON; strip optional trailing CR and preserve Unicode separators in strings. Do not inherit bufio.Scanner's default 64 KiB token ceiling. Set explicit byte limits including a single huge image/custom entry. Never truncate JSON silently. Test fragmented/coalesced records, CRLF, Unicode, malformed/oversized output, slow peers and extension stdout pollution.

Type the envelope while preserving bounded unknown native fields as raw JSON. Unsupported events must not crash the adapter or acquire invented semantics. Distinguish transport IDs, native session/entry/tool IDs and Pixie delivery IDs.

### Acceptance versus settlement

Pi prompt replies acknowledge acceptance, queuing or immediate handling. Do not complete a legacy completion-oriented call there. Native agent_end may precede retry/continuation; use the tested settled boundary for session-level completion. Never resend accepted-or-possibly-accepted work because a reply was lost.

Test ordinary turns, commands without an LLM turn, extension-triggered deferred work, manual/automatic compaction, retries, queued continuations, tool failures, cancellation and child death. Command-specific completion comes from supported semantics, not a guessed idle timer. Protocol semantic changes follow the versioning path in 01. Run the same recordings/scenarios in both binary compositions.

### Queues and Stop

Native Pi owns accepted queues. The controller may retain an explicitly named durable outbox for not-yet-handed-off work with pending, dispatching, accepted and delivery-uncertain states. Preserve ambiguous-delivery safeguards. Never enqueue one item in both stores or claim exactly-once execution across crashes.

Specify whether Stop clears native queued work, only aborts current execution, or restores queued text. Native clear_queue before abort matters for TUI-style Esc behavior. Do not silently discard the Pixie outbox or let queued work resume after an action presented as stopping all work.

## Native sessions and catalog

Build a bounded read-only index of native JSONL files with cached metadata, incremental reads and rescans for external edits/replacements. An incomplete final record is a recoverable tail, not a reason to discard history. Preserve unknown entries and never rewrite transcripts to simplify projection.

Track native ID/path/cwd, parents, active leaf, names, custom entries, compaction/branch summaries and model metadata. Duplicate native IDs are ambiguous: fail with an actionable diagnostic instead of choosing the first file. Make index limits and partial availability visible.

Project membership and archive are Pixie metadata. Discovering cwd does not admit it for file access or mount it into Docker. A session outside admitted roots may remain listed and inspectable under the trusted-user session policy while Files/Git explain their unavailability. Keep execution authorization explicit rather than treating absence of a display project as a changed native cwd.

| Operation | Native direction |
| --- | --- |
| Create/reopen | Startup or new_session/switch_session; respect cancellation |
| Model/thinking | Supported native queries and setters |
| Rename | set_session_name |
| Clone current position | clone, not an earlier-message fork |
| Fork earlier user message | get_fork_messages and fork; preserve text/cancellation |
| History | Native entries and active leaf for residents; read-only index for discovery |
| Compact/stats | compact and get_session_stats |
| Archive/grouping | Pixie metadata only |
| Delete | Explicit confirmed operation after stop/release, with exact identity/path revalidation |

Do not synthesize session headers or reconstruct branches with a Go writer. Keep an unpersisted empty conversation as a draft until Pi supplies durable identity; reconcile it once. Clone/fork can change the child process's active identity: update ownership and reopen the independent source as needed without corrupting either session or claiming both IDs remain active writers in the same child.

Use a service/agent-directory lock plus per-managed-session ownership in both variants. These protect Pixie against itself, not independent vanilla TUI writes. Idle handoff releases the managed writer and explains native resume. Arbitrary live TUI process attachment remains outside this release absent supported upstream machinery.

## Supervision and recovery

Use one serialized state owner per managed session, tracking child generation, requests, agent state, dialogs, supported background-work pins, last use and termination.

Start children on demand, not per catalog row/project. Bound residents and concurrent starts. Never evict active runs or pending interactions. Unknown extension liveness needs a documented conservative policy; an optional versioned native bridge can report detached work, but its absence cannot disable core chat. Measure before copying SDK-host idle limits because child processes have different costs.

Browser disconnect removes a subscription, not Pi. Controller disconnect preserves accepted work and bounded projection. Child exit fails outstanding requests and marks unsettled work interrupted/uncertain. Do not recreate an in-flight run invisibly.

Use managed process groups with systemd's final cgroup boundary. On SIGTERM: stop new work, signal shutdown, cancel interactions according to policy, request native abort, wait within grace, terminate remaining managed descendants, finish Pixie metadata and exit. Escalate stubborn descendants, never independent user TUI processes.

Whole-service restart follows the same path. Standalone restarts the assistant; combined restarts the whole pixie process after quiescing controller schedules/outbox/modules. Communicate the wider impact in the UI. Repeated requests are idempotent and actually change PID/boot identity. A library callback-only test is insufficient. Unit/artifact requirements are in 07.

## Projection and reconnect

Assemble partial output by content index/tool-call ID; final native messages are authoritative. Preserve text, thinking, images, usage, errors, visible custom messages and partial tools. Hidden entries remain native state rather than displayed instructions.

Distinguish stable installation identity, assistant boot epoch and child generation. Snapshot state/checkpoint under the session owner, buffer and replay only subsequent events. A new generation invalidates old answers and expected-run commands. Bootstrap before dispatch, including initialization/history events. Several get_state/get_entries calls are not automatically atomic.

Page history and encode incrementally. Avoid serializing it all to decide whether to chunk. Bound one huge message as well as total history; never split a JSON object into fake messages. Use safe image/artifact references for UI transport where possible without rewriting native content. Embedded composition uses the same projection semantics and limits as the external transport.

## Optional capabilities and cutover

Finish the disposition matrix in 01 before deleting legacy behavior. Native RPC may lack current provider/resource/authoring operations. Use public native APIs, explicit optional bridges, useful native-TUI fallbacks or approved deferrals. Do not recreate provider policy in Go or drop functionality merely because porting is awkward.

Test unfamiliar native extensions, images, delegation, plans, memory and local models as optional lanes. Core needs no assistant-owned subagent dependency or Pi source patch. Reassess Bun-specific subagent and SDK llama-export patches against selected Pi; retire only after retained scenarios pass.

Migration converts Pixie metadata, not Pi JSONL/auth/settings. Back up, make conversion repeatable, declare rollback readability and stop active work before changing owner. Preserve archive/project identity. Test switching Docker+assistant and combined host as well as old/new engines; never run conflicting writers.

## Delivery and acceptance

Order: current conformance recordings; reusable engine/facade and discovery/transport; one vanilla conversation through both commands; catalog/history/reconnect; clone/fork/images/settlement; native UI and optional dispositions; migration and artifacts. Integrate the new UI after independent vertical slices work.

Acceptance: independently installed fresh/existing Pi; no native writes during discovery; no duplicate prompt after reconnect; correct clone/fork IDs; no orphan descendants; no cross-session/generation answers; documented controller compatibility; optional-package absence leaves chat usable; rollback needs no transcript repair. The combined binary works with pixie-assistant absent; Docker external mode never starts Pi itself.

Measure both full topologies, including controller and Pi children: idle/active RSS, startup, catalog, cold/warm history attach, simultaneous sessions, output and repeated p50/p95. Distribution and ownership justify the rewrite; performance improvements require measurements.
