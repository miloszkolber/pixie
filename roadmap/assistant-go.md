# Go assistant implementation plan

Replace the TypeScript service with a reusable Go engine that launches the selected host Pi. Both pixie-assistant and the full-host pixie executable use the same engine. Read [contracts.md](contracts.md), [feature-coverage.md](feature-coverage.md) and [builds-and-releases.md](builds-and-releases.md) before implementation; execution.md tracks GO/BRIDGE/BUILD tasks.

The current service embeds a pinned SDK under Bun. Native subprocess RPC has different response and administration semantics. Preserve observable features and ownership, not TypeScript structure. The [consolidated review](repository-review.md) records migration blockers; [migration.md](migration.md) details state conversion and rollback under the shared contracts.

## 1. Runtime ownership

```text
browser -> Pixie controller -> shared Go assistant -> selected native pi
           app state           supervision/projection  native execution/state
```

Docker mode uses the authenticated host endpoint; full-host mode composes controller and assistant in one process using a private loopback transport initially. Neither starts a second assistant executable in full-host mode or Pi inside Docker controller mode.

Pi retains native settings, credentials, resources, extensions, trust, tools, queues, retry and compaction. The assistant has no replacement prompt, provider policy, scheduler or separate native MCP client. Optional workers belong to application modules. The optional native administration bridge uses the selected Pi installation, not a separately installed SDK.

## 2. Source structure and contracts

Keep assistant/go.mod separate from package/go.mod. Use a narrow public assistant/host facade; another Go module cannot import assistant/internal directly. The application uses a repository-local replacement to the exact checkout. Do not copy RPC/supervisor implementation or combine this with a repository-wide move.

```text
assistant/
  cmd/pixie-assistant/main.go
  host/
  internal/config/
  internal/discovery/
  internal/pirpc/
  internal/supervisor/
  internal/catalog/
  internal/projection/
  internal/hostapi/
  internal/service/
  internal/wire/
  bridge/
  testdata/
  systemd/pixie-assistant.service
  go.mod
  go.sum
package/cmd/pixie/
```

The facade owns configuration/start/readiness/transport/close and returns lifecycle signals. Only entrypoints own process exit. Build metadata reports sourceCommit/releaseId independently of native Pi and protocol versions. Shared schemas belong in package/contracts with checked bindings; assistant compilation must not require Svelte assets.

Implement target host protocol v2, not an unannounced semantic change to v1. Capture legacy traces first; keep a versioned legacy adapter while needed. API-01 maps actual browser/controller/assistant/native methods to coverage rows and catches hardcoded Administration/provider assumptions.

## 3. Discovery and configuration

Use the exact precedence/defaults/bounds in contracts.md. Resolve the explicit executable or service PATH pi to an absolute executable; pin it for that service lifetime. Version detection/read-only doctor does not load project extensions, install packages, prompt a provider or write native settings.

Preserve native HOME/PATH/agentDir/cwd and intended provider/proxy/tool environment. Remove service/control secrets before exec. Native argv is an operator-configured array, not shell text or web-controlled flags. Reserve mode/session/resume/continue/print behavior and reject conflicting aliases/equals forms. Local-model flags remain explicit and version-tested.

Serve on literal loopback only; validate port/secret/config before effects. Full-host composition owns its private endpoint. Distinguish missing executable, missing interpreter, fresh native state, unconfigured provider, incompatible RPC, optional bridge and optional module failure. Setup/navigation remain available.

Test independent npm/standalone Pi, symlinks, spaces, custom prefixes and systemd's non-login PATH. Do not assume a binary distribution offers the same import surface as npm. Native first-run creation remains native; revalidate the resulting state-directory identity without moving it.

## 4. Native RPC transport

Use one reader and serialized writer, correlated native request IDs, bounded pending operations and independently drained stderr. LF JSONL permits a trailing CR but not arbitrary Unicode delimiter splitting. Bound whole records and writes; never truncate JSON or block child reading behind UI backpressure.

The native prompt acknowledgment is acceptance/queueing/handling. Full settlement follows native agent_settled and tested command semantics, not the first agent_end. Command-only and extension-triggered work require dedicated traces. Unknown settlement blocks automatic outbox continuation rather than guessing from elapsed quiet time.

Use native model/thinking/compaction/session APIs and preserve unknown native event fields as data. Native thinking levels are model-dependent; do not duplicate the old xhigh-only preference enum. Strict Pixie envelopes and forward-compatible native payloads are separate validation concerns.

Implement the prepared/dispatching/accepted/settled/uncertain state machine and Stop behavior from contracts.md. Preserve the controller outbox until handoff, then remove native-runnable ownership. A lost acknowledgment never authorizes automatic prompt resubmission. Keep attachment identity, not only prompt text, on queued work.

## 5. Catalog and native persistence

Index native files read-only with bounded scanning/cursors and metadata-aware invalidation. Handle incomplete trailing JSONL, external file replacement, unknown records, duplicate native IDs, native branch/leaf identity and missing files. Discovery must not reopen/import/run a session just to list it.

Use authority/session associations with optional project metadata. Native cwd does not imply file/Git admission. A fresh conversation remains a draft until native persistence; never synthesize headers. Import old assistant archive/parent metadata as part of a tested application-metadata migration, not a transcript rewrite.

Use native set_session_name, clone, get_fork_messages/fork, compact and state/statistics queries. Clone current position is not fork-at-message. A successful fork/clone can change the native identity in the child: transfer ownership and leave the original independently reopenable. Respect native cancellation.

Delete is an explicit user operation with durable tombstone, exact current native-file identity and authenticated authority. Migrate old endpoint/secret-bound recovery records before ephemeral combined-mode endpoints. Unverifiable old records remain recovery-blocked, not silently removed or replayed elsewhere.

Keep one shared assistant identity/lock for the canonical agent directory across both binaries. It does not coordinate an unrelated vanilla TUI. Explicit Release to TUI requires settled work and terminates the managed owner before providing resume information.

## 6. Supervision and recovery

Use serialized session owners with process generation, command admission, active/detached work, pending dialogs, subscriptions and teardown state. Launch on demand. Initial resident/active/launch limits and disabled automatic idle eviction are in contracts.md; unknown detached work is not idle. Explicitly releasing runtime differs from closing a view.

Browser/controller disconnect removes subscriptions, not accepted runs. Child exit invalidates callbacks, fails pending requests and marks unsettled delivery interrupted/uncertain while keeping transcript and claims. Never re-launch a child merely to resend accepted work.

Shutdown stops admission/outbox scheduling, settles/cancels dialogs, requests native abort, drains bounded work and terminates stubborn managed descendants. Enforce the overall deadline around construction, admin I/O, extension teardown and pending operations, not only abort. Use process groups and the service cgroup as final cleanup; never signal an unrelated TUI.

Repeated restart is idempotent. Standalone exits through its entrypoint; combined mode drains controller/scheduler/modules and assistant before whole-process exit. No os.Exit in libraries, half-dead controller or orphan native children. Preserve explicit uncertain external-side-effect outcomes after forced termination.

## 7. Projection and reconnect

Final native messages are authoritative; assemble partials by content index/tool ID. Preserve text, thinking, images, visible custom content, summaries, usage and error state without exposing hidden native entries. Latest bounded status/widget projections can survive UI reconnect while their child remains valid.

Separate stable authority, bootId and childGeneration. Event checkpoints use the correct epoch. Snapshot/bootstrap and event buffering share an owner; several native read requests are not automatically atomic. Test initialization events, simultaneous attach, compaction and fork while restoring.

Pending UI ownership includes native ID, sessionKey, child generation, primitive and original deadline. Native RPC's incomplete cancellation observability and working-message no-op are explicitly covered by FC13–FC16/BRIDGE-03. Do not fabricate confirmations or timing-based associations. Draft conflict handling uses per-client revision and never auto-submits.

Page old history through a bounded read-only index. Native whole-response materialization remains an upstream constraint; reject oversized native records transparently while retaining their source. Eager serialization followed by chunking is not a memory bound. Measure complete process trees and image-heavy cases.

## 8. Capability disposition and compatibility

[feature-coverage.md](feature-coverage.md) is the required 33-row migration contract. V is vanilla RPC, A the explicit native administration bridge, M native MCP integration, and W application modules. Both binaries expose the same tested profile behavior.

Core compatibility cannot require provider login/default administration. Session models use native RPC; full provider inventory/auth/defaults/native resource configuration use verified public native APIs through A. No Go provider database, auth implementation or generic settings-file patch fallback.

Deliver the optional bridge as embedded content-addressed assets loaded explicitly into selected native Pi, with a bounded private inherited channel. Verify actual import origin/context/API access before advertising each operation. Do not assume a normal ExtensionContext contains the old assistant's AgentSession or its tool array. No private deep imports, model-facing admin commands or extra JS daemon.

A native-TUI fallback is useful but is not web parity. Keep legacy host selection until mandatory retained rows are implemented/tested or a specific reduction is approved. Never make both runtimes writers to the same session. Already unsupported terminal factories stay explicit; newly missing working hints/cancellation cannot be hidden as terminal-only limitations.

Native optional subagents/todo/web/Signet/local models remain optional and operator-installed. Test actual tools/children and native config inheritance; mocks or package-name discovery are not execution evidence. Do not mutate native project trust to make compatibility tests pass.

## 9. systemd and artifact delivery

Use a systemd user service under the Pi owner. Standalone example:

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

The combined unit uses pixie serve with combined config and no separate assistant dependency. Exit 75 means requested restart; normal stop succeeds. Test real unit/executable behavior and target systemd directives. Type=exec is not readiness. Optional lingering is explained, never enabled silently.

Do not add blanket ProtectHome/DynamicUser/native-tool restrictions that break the user's Pi environment. Optional untrusted worker containment is configured separately under the contract, including its tested delegation requirements; native Pi retains its intended authority.

Build both variants and Docker from one source commit with the exact sha-<12> release scheme. Four host archives plus matching image platforms are a mandatory set. See builds-and-releases.md for immutable tags, source/digest verification, complete-set staging, approval and rollback. No new semantic-version pipeline.

## 10. Migration and acceptance

Implement in this order: capture v1 traces and FC inventory; shared facade/discovery/transport; vanilla session slice; authority/grouping metadata migration; history/reconnect/native lifecycle; bridge feasibility then administration/MCP/UI rows; both real executable compositions; artifact/systemd/rollback evidence; only then legacy retirement.

Capture every old metadata store and define repeatable conversion/rollback under stopped admission, following [migration.md](migration.md). Native credentials/settings/transcripts are not migration targets. Preserve archive/parent relationships, attachment displays, uncertain outbox claims and recovery-blocked deletions. A mode switch does not mean a new host authority or automatic resend.

Accept only after independent Pi/profile tests, both final artifacts on both architectures, UI continuity, bounded shutdown, no native mutation from discovery and the coverage gate pass. Record actual tests/commands, not intended checks. Measure cold/warm startup, catalog/history, total RSS including Pi, concurrent turns, cancellation and shutdown with repeated samples. Go's distribution/ownership benefit does not establish a performance improvement by itself.
