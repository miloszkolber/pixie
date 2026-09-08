# Go assistant implementation plan

Replace the TypeScript assistant with one shared Go runtime that launches the selected host Pi. Distribute it both as `pixie-assistant` for a Docker controller and inside the full-host `pixie` binary. Logical assistant ownership remains separate even when both services share one process. See [builds-and-releases.md](builds-and-releases.md); track GO and BUILD tasks in [execution.md](execution.md).

The current assistant embeds a pinned SDK under Bun. Native subprocess RPC has different prompt-response semantics. Adapt observable behavior rather than translating TypeScript line by line. [Source baseline](sources.md#assistant-and-native-pi).

## 1. Runtime ownership

```text
Browser -> Pixie controller -> shared Go assistant runtime -> selected host pi
           workspace/data      discovery/supervision         native execution/state
           schedules           RPC/projection                providers/tools/extensions
```

In Docker mode, controller and assistant communicate across authenticated loopback. In full-host mode, one binary composes both using the same tested host API initially through an internal-only loopback listener. Do not start an external assistant executable, duplicate its implementation, or expose its listener through the public web interface.

Pi remains a separate child executable with its own installation/runtime requirements. Neither build ships a hidden replacement SDK. Core assistant/full-host execution does not require Node/Bun beyond what the chosen Pi installation itself needs. Optional application-side module workers are a separate concern.

The assistant does not own model routing policy, tool interception, replacement prompts, schedules, a provider database, or a second MCP client for Pi. Preserve native trust/resource loading; never auto-trust a project to pass a test.

## 2. Source structure and contracts

Keep `assistant/go.mod` separate from existing `package/go.mod`; no root-module/directory migration. Use a narrow public `assistant/host` facade so the full-host executable can compose assistant internals without importing another module's `internal` packages.

```text
assistant/
  cmd/pixie-assistant/main.go
  host/                         public composition facade
  internal/config/
  internal/discovery/
  internal/pirpc/
  internal/supervisor/
  internal/catalog/
  internal/projection/
  internal/hostapi/
  internal/service/
  internal/wire/                generated shared-contract bindings
  testdata/
  systemd/pixie-assistant.service
  go.mod
  go.sum

package/cmd/pixie/             full-host and explicit controller-only composition
```

The facade exposes configuration, startup, readiness, host transport and bounded shutdown without controller dependencies. Both entrypoints use it. Libraries return lifecycle signals/errors; only entrypoints decide exit. Use repository-local module replacement for exact-source builds, not a moving remote assistant dependency.

Keep small packages around real boundaries, not a generic agent framework. Colocate Go unit tests; retain current controller/integration test areas. Update root guidance to permit that placement.

One wire schema owner lives in `package/contracts`; generate/check bindings in both consumers where useful. Assistant compilation must not depend on building frontend assets. Inventory methods first; preserve controller-facing semantics until an explicitly versioned change is integrated. Do not retain a protocol number while replacing completion replies with acceptance replies.

The identity report distinguishes assistant build/version, native Pi version, protocol/capabilities, stable installation identity and fresh boot epoch. Full-host release metadata includes the embedded UI revision too.

## 3. Discovery and configuration

Resolve configured executable first, otherwise `pi` on service PATH; pin the absolute path for the service lifetime. Diagnose missing executable, interpreter, protocol support, state directory and provider configuration separately.

Use the CLI/configuration contract in [builds-and-releases.md](builds-and-releases.md#4-cli-configuration-and-service-lifetime). Shared settings include Pi executable, agentDir, listen/secret inputs, optional native argv and resource bounds. In full-host mode the composition root owns the internal listener/credential; user-facing settings must not accidentally expose it.

`piArgs` is an argv array, not shell text. Reserve mode/session/cwd controls for the supervisor; reject conflicting arguments. Native optional CLI features, including local-model flags, remain opt-in and version-tested. No silent Pi install/upgrade or native package installation.

Preserve intended HOME, `PI_CODING_AGENT_DIR`, cwd and operator-supplied provider/tool environment. Browser's stripped environment is inappropriate for native Pi. Remove service-auth secrets unless a specific native integration explicitly needs its own credential. Never return credentials in diagnostics, replay, snapshots or controller state.

systemd does not inherit interactive shell aliases/initialization. Test independently installed npm/standalone Pi, symlinks, custom prefixes/HOME/PATH/state directories and spaces. `doctor` is read-only by default: no model prompt, installation or project-extension loading merely for version detection. Any active compatibility probe is explicit and describes its effects.

The baseline host API is loopback-only. Do not preserve the legacy unrestricted host bind escape accidentally. Explain unsupported Pi releases through a tested range, not arbitrary-version claims.

## 4. Native RPC transport

Use one reader and serialized writer per child, correlated IDs, bounded pending maps, deadlines and child-exit handling. Stdout is protocol; stderr is bounded/redacted diagnostics. Drain independently so a slow UI does not deadlock the agent.

Pi uses LF JSONL. Strip optional trailing CR; preserve Unicode separators in strings. Do not use Go's default 64 KiB scanner ceiling without adjustment. Bound bytes per record and reject oversize without truncating JSON. Test fragments, multiple records, CRLF, Unicode separators, invalid JSON, large images, stdout pollution, backpressure and cancellation.

Type strict envelopes and preserve unknown native payload fields as raw JSON. Unsupported events do not crash the service or acquire invented meaning. Keep native/Pixie request IDs, sessions, entries and delivery IDs distinct.

### Acceptance and settlement

Native prompt replies mean acceptance/queueing/immediate handling, not turn completion. `agent_end` can precede retry/continuation; inspected Pi 0.85.1 documents `agent_settled`. A completion-oriented host method waits for the appropriate native settlement. A new acceptance-oriented method returns a delivery identity with separate progress. [Native reference](sources.md#assistant-and-native-pi).

Never resend after losing an acknowledgment when Pi may have accepted the prompt. Test commands with no LLM turn, extension-triggered work, manual/automatic compaction, retries, queued continuations, errors, cancellation and child death. Accepted commands need not all emit ordinary chat sequences.

Detached extension work may use a small versioned optional liveness bridge. Its absence cannot disable basic sessions or justify fabricated liveness. Decide safe residence/limitations explicitly.

### Queues and Stop

Pi owns native steering/follow-up queues. Pixie's existing durable outbox owns work not yet handed off. Track pending, dispatching, accepted and uncertain outcomes; one item cannot be runnable in both.

A dispatch/acknowledgment crash is uncertainty, not permission to retry. Preserve current fail-closed behavior; do not promise exactly-once execution across process failure or mode switching.

Stopping a turn, clearing native continuation and restoring text to the composer differ. Native `clear_queue` before `abort` is relevant to TUI-style Esc; do not silently erase the durable outbox or let queued work resume after an all-work Stop.

## 5. Catalog and native persistence

Index sessions read-only with bounded scanning, file-metadata caching and rescan after replacement/external edits. An incomplete trailing JSONL record is a recoverable tail. Preserve identity/path/cwd/parent/active leaf, custom entries and summaries in native storage even if the UI cannot display them.

Duplicate native IDs require disambiguation, not first-match selection. Projects are optional metadata. Cwd does not automatically authorize file browsing or create a Docker mount; retain native sessions outside admitted roots with explicit unavailable file access.

| Operation | Direction |
| --- | --- |
| Create/reopen | Native startup or new_session/switch_session; respect cancellation |
| Model/thinking | Native setters and supported-level queries |
| Rename | set_session_name |
| Clone current position | Native clone, not fork-at-message |
| Fork earlier prompt | get_fork_messages/fork; preserve returned draft/cancellation |
| History | Native entries/active leaf for residence; read-only discovery index |
| Compact/statistics | Native compact/get_session_stats |
| Archive/grouping | Pixie metadata only |
| Delete | Explicit destructive action after closing managed work and revalidating file identity/path |

Do not synthesize headers or rebuild branches with a Go writer. An unpersisted conversation remains a Pixie draft until Pi assigns/persists identity; reconcile once without duplicate rows.

Use one stable assistant lock/identity scheme for both binary variants and per-managed-session ownership. This does not coordinate an independent vanilla TUI. Retain separate-session simultaneous use and implement an idle handoff that releases Pixie ownership before native resume.

Fork/clone can change the child process's native session ID. Transfer ownership explicitly; never leave the original ID pointing at the new fork. Preserve independent reopen behavior for the source.

## 6. Supervision and recovery

Serialize session state mutation. Track process generation, pending requests, agent activity, dialogs, supported background pins, last use and shutdown. Launch on demand, not per catalog entry/project. Measure before copying old idle-process limits.

Active work/dialogs/background pins are not idle. Browser disconnect removes subscriptions, not accepted runs. Controller disconnect preserves bounded projection and active work. Child exit fails calls, invalidates callbacks and marks unsettled delivery interrupted/uncertain while retaining history.

Use process groups plus the systemd cgroup as the final cleanup boundary. Stop admission, report shutdown, cancel/settle interactions, request native abort, wait within a deadline, terminate stubborn managed descendants, persist Pixie metadata and exit. Never signal an independent TUI.

Restart shares bounded shutdown. Repeated restart is idempotent. In the standalone binary it restarts the assistant service; in full-host mode it signals the parent composition root to drain scheduler/controller/modules and restart the whole service. No library-level `os.Exit`, half-dead controller or orphan children.

## 7. Projection and reconnect

Final native messages are authoritative. Assemble partial blocks by content index/tool-call ID; retain text/thinking/images, visible custom messages, errors and tool state without exposing hidden records.

Stable installation identity and fresh boot/process epochs are distinct. Scope sequences/checkpoints/replies to the right epoch. Snapshot construction and concurrent event buffering share one state owner. Old dialogs/continuation commands cannot cross child generations.

Bootstrap before admitting prompts. Independent native queries are not automatically atomic; reconcile history/active leaf with events or stay visibly restoring until consistent. Test initialization events and concurrent attach.

Keep valid pending UI requests and original deadlines while the child lives, invalidate after restart, and retain bounded latest widget/status state. User drafts remain UI-owned; supported editor text cannot silently overwrite newer input.

Page history through controller/UI. Avoid eager full-history serialization just to decide chunking. Bound individual large records/images as well as aggregate output. Never split arbitrary JSON into fabricated messages; reuse safe mediated attachment references where appropriate.

## 8. Capability disposition and compatibility

Pi 0.85.1 is the first candidate conformance baseline, not universal support. Add releases only after fixtures pass; keep upstream smoke separate from supported-release CI.

| Family | Cutover requirement |
| --- | --- |
| Chat/history/model/thinking/images/abort | Works with optional packages absent |
| Retry/compaction/continuation | Correct settlement, Stop and recovery |
| Dialogs/notifications/text widgets/status/title | Native mapping, ownership and replay |
| Editor text | Session-specific semantics and draft-conflict protection |
| TUI factories/terminal chrome | Explicit unsupported state |
| Provider login/default administration | Supported public native API or approved explicit fallback |
| Native resource inventory/configuration | Public versioned interface or optional bridge, not hidden Go config rewriting |
| MCP tools | Native client owns execution; no duplicate assistant MCP client |
| Delegation/plans/Signet/local models | Optional tested integrations, not a required bundle |

Record every retained legacy method's disposition, callers and tests. Missing native administration is not permission for silent feature loss. Approve any cutover reduction explicitly; independent work can proceed while a decision is pending. A temporary legacy host can remain selectable, but two runtimes never own the same managed session.

## 9. systemd and artifact delivery

Both required build variants use the [release/service contract](builds-and-releases.md). Proposed standalone unit:

```ini
[Unit]
Description=Pixie assistant
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=exec
ExecStart=%h/.local/bin/pixie-assistant serve --config %h/.config/pixie/assistant.json
Restart=on-failure
RestartSec=2
RestartForceExitStatus=75
TimeoutStopSec=30
KillMode=mixed
UMask=0077
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

The full-host unit uses `pixie serve` and its combined config, with no dependency on a separate assistant unit. Reserve exit 75 for requested restart; ordinary stop succeeds. Test the executable and unit together. Type=exec does not assert application readiness; expose truthful readiness or implement notify fully. Verify supported target-systemd directives. [References](sources.md#external-contracts).

Avoid default ProtectHome/DynamicUser/blanket write restrictions that break user-owned Pi. Test optional NoNewPrivileges/hardening against actual native tools. Explain user-service lifetime/optional lingering without changing host policy automatically.

Build/check both variants on amd64/arm64 from one revision, with pinned Go, trimpath/version metadata, checksums/provenance/notices and real archive tests. CGO-free release builds are preferred when dependencies permit; race tests run separately. No assistant runtime toolchain, hidden assets or secrets. The full binary embeds verified UI and assistant code; assistant-only contains no UI/worker dependencies.

## 10. Migration and acceptance

Capture legacy fixtures; implement discovery/transport, one vanilla session, history/reconnect and optional dispositions. Add the public composition facade early, then test full-host integration without another implementation. Keep the old host selectable until required behavior passes.

Back up Pixie metadata before schema changes; native transcripts/configuration are not migration targets. Keep conversion repeatable and declare rollback compatibility. Settle or explicitly interrupt work before ownership or deployment-mode switches. Preserve uncertain delivery claims.

Accept only after read-only discovery leaves native files unchanged; accepted prompts are not duplicated; clone/fork identity is correct; descendants and dialogs clean up; both controller modes behave explicitly; optional dependencies can be absent; install/restart/rollback/mode switching pass with real archives.

Measure assistant plus Pi children and controller/module costs separately by variant: startup, idle/active RSS, catalog, cold/warm history, concurrency, shutdown and repeated latency samples. Distribution/ownership justify Go before a performance gain is proven; main-process RSS alone does not prove one.
