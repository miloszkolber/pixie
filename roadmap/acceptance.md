# Acceptance and coverage

Owner F/A with each implementation owner. This file maps requirements to observable evidence. It is a test specification, not a statement that the tests have already run. Delivery status and evidence links belong only in execution.md.

## Required environments

Use disposable agent/data directories, deterministic local fake providers for failure scenarios, and independently installed released Pi for compatibility. Record Pi distribution/version, executable path, architecture, binary release ID and test command. Workspace SDK tests remain useful but do not substitute for selected-executable tests.

Run both host variants on Linux amd64 and arm64. The assistant-only fixture uses the real built Docker controller in external mode; the combined fixture has no separate assistant executable/service or running Docker dependency. Test Vanilla with no extensions, no provider configured, and a configured native provider. Test the managed bridge profile and each retained optional integration enabled and absent. Do not spend paid provider quota or invoke real user tools without authorization.

## Core assertions

| ID | Required observable result | Tasks / gate |
| --- | --- | --- |
| AC-01 | Explicit/default Pi discovery handles standalone/npm/symlink/space/custom HOME/PATH/agentDir; read-only doctor performs no package install, project import or model call. Wrong installation is not silently selected. | GO-01, Gate 2 |
| AC-02 | Vanilla create/prompt/images/Stop/reopen works. The model picker works while provider administration is unavailable. Missing provider leaves navigation, safe history and setup reachable. | GO-03, CP-01/02/06, Gate 2 |
| AC-03 | Acceptance and settlement are distinct for normal turns, retries, compaction, commands without an LLM turn and deferred work. No early completion at agent_end. | GO-05, CP-03/04, Gate 3 |
| AC-04 | Disconnect at each dispatch/acknowledgment point never silently duplicates a prompt. Uncertain delivery is visible; default Stop pauses outbox, clears continuations and aborts without losing unsent drafts. | GO-05, CP-03, Gate 3 |
| AC-05 | Independent clone and earlier-message fork preserve source/history/IDs, respect cancellation and reconcile changed child identity. No Go transcript rewriting. | GO-04, CP-05, Gate 3 |
| AC-06 | Large history, one oversized record, interrupted JSONL tail, unknown entries, hidden custom records and external replacement produce bounded correct results or explicit limits. No transcript repair by truncation. | GO-02/04, CP-01/02/04, Gate 3 |
| AC-07 | Mid-text/tool/dialog reconnect restores a consistent snapshot/checkpoint, final native output wins, old child epochs cannot accept answers and slow consumers do not stall stdout indefinitely. | API-02, GO-02/06, CP-12/17, Gate 3 |
| AC-08 | Real executable and systemd restart changes PID/boot identity after bounded cleanup; explicit stop has no orphan managed descendants. Browser disconnect/Hide does not stop native work. | FIX-01, GO-07, BUILD-05, Gate 5 |
| AC-09 | Every CP-01–17 row is mapped to actual callers and passes on both compositions in its supported profile. No optional dependency is mandatory for Vanilla. No disabled Web action/TUI workaround is counted as managed parity without a separately approved reduction. | API-01/03, GO-08, CUTOVER-01 |
| AC-10 | Native settings/resource saves preserve unknown fields, scope and unrelated concurrent changes; missing sources are not installed; saved/loaded state stays distinct. Agent CRUD handles concurrent create/update/delete without silent overwrite. | FIX-06/07, CP-08/09/10 |
| AC-11 | Native MCP is the only Pi MCP runtime; generic bridge uses supported APIs and private typed IPC, with real registration/disposal, no stdout pollution or credentials in transcripts. Wrong-session credentials fail. | API-03, EXT-04, CP-11/12/16 |
| AC-12 | Module failed save leaves committed state/routes intact; unchanged enable preserves Browser handle; invalid config makes only that module unavailable; sibling settings survive toggles/failed starts. | FIX-02/03/08, EXT-02 |
| AC-13 | Human schedules CRUD/preview/run-now/pause/stop reuses the existing ledger and works without model MCP. Model schedule tools require supported MCP. Test DST, clock changes, missed occurrences, non-overlap and interrupted claims. | UI-04, CP-14, SEC-01 |
| AC-14 | Native discovery, migration, downgrade, topology switch and uninstall do not modify native files. Archive/grouping/attachments and host MCP membership survive; rollback never rewinds dispatched work or tombstones. | MIG-01–04, GO-09, Gate 5 |

Strict transport tests include missing/wrong-type IDs, numeric strings/booleans/null, unknown method, duplicate in-flight IDs, absent/non-object parameters, malformed/fragmented/coalesced JSONL, CRLF and Unicode separators, oversized input/output, stalled stdin/stderr and child exit. Do not weaken schema tests to accept copied legacy coercion in a new protocol.

## Six-column UI fixtures

The desktop shell must render the user-provided composition as six logical slots, not six equal-width grid cells. Assert each rectangle and selected owner. Required fixtures: split 1–6; secondary focus 1/2/4/5/6; primary with context 1/2/3/5/6; primary with sidebar 1/2/3/6; primary focus 1/3/6.

| ID | Scenario and assertion |
| --- | --- |
| UI-A | Real streaming conversation in slot 3 and selected file in slot 4. Selecting another file never replaces chat. No secondary selection means no empty slot-4 track/divider. |
| UI-B | Collapse either sidebar independently; focus/restore/hide/close preserve the specified selection, draft, runtime and return focus. Escape first closes the top transient dialog/menu. Hidden regions are inert. |
| UI-C | Chats grouped/flat/ungrouped use one catalog. Selected row stays visible after Show more changes. Repeated New while pending makes one create; late results cannot steal focus after newer navigation. Legitimate duplicate titles remain allowed. |
| UI-D | Archive is persisted and distinct from closed views/delete. Unarchive preserves native identity. Removing a project does not delete Pi transcripts. Running-session actions retain explicit Stop/confirmation behavior. |
| UI-E | Schedules and Settings occupy the primary sidebar/detail surfaces, not a giant modal or a second scheduler. Scheduled run links target the actual native session. |
| UI-F | Details follow native session; file/Git context follows admitted project; multi-repository/no-repository states work; unrelated Settings do not retain actionable stale session controls. Instance Design scope is explicit. |
| UI-G | Reload/back/forward, invalid/deleted resource, project switch during slow fetch and old saved-tab migration yield the intended selected loading/content/missing/error state, not an unrelated empty project. Include the supplied blank-content regression with captured route/state. |
| UI-H | Missing/disconnected Pi/provider/MCP/worker leaves the shell usable with accurate per-action availability and stale indicators. Reconnect does not discard an unsent draft. |
| UI-I | Mewa controls retain correct tokens, semantics and lifecycle. Repeated mount/unmount leaves no duplicate listeners/popovers. No second color/type/spacing source remains after migration. |

Capture content-filled light and dark states at desktop, intermediate split threshold and narrow/mobile widths; include long names, tool output, Markdown/code, large diff, images, loading/error/empty. Test 200% zoom, keyboard-only use, reduced motion and forced colors where supported. Verify independent scroll regions, visible composer, no document horizontal overflow, labelled rails, accessible truncated names, keyboard/pointer splitters with min/max/reset, and no focus left in a hidden pane. Do not mark visual acceptance from screenshot generation alone; compare computed geometry and the intended reference behavior.

## Release and deployment assertions

| ID | Assertion |
| --- | --- |
| REL-01 | Full source commit resolves once; releaseId equals sha- plus its first 12 lowercase hex characters. Git tag/title, both binary versions, all four names and Docker tag match; full revision metadata and tag target agree. |
| REL-02 | Same short ID with a different full commit is rejected. Missing/wrong-platform artifact, invalid checksum, placeholder UI, stale local assistant module and dirty source stamping all fail before publication. |
| REL-03 | Archives unpack/run without a checkout or extra frontend runtime. Full-host works with pixie-assistant absent; assistant-only builds without Svelte/controller/worker closure. Version output works without Pi/config/network. |
| REL-04 | Both Docker architectures use external mode and never spawn Pi, initialize an embedded bridge or acquire host session ownership. Effective final entrypoint reaps/terminates Browser descendants; inherited build-stage tini alone is not evidence. |
| REL-05 | Dedicated release workflow tests candidates before publishing. PR/main/schedule workflows publish nothing. Draft remains unpublished after partial upload failure. Retry validates/reuses identical artifacts, never clobbers or moves an immutable tag. |
| REL-06 | Four archives plus both image-platform digests, release.json, checksums and provenance are verified as a complete set. OCI labels and provenance subjects identify the full source. Floating apt/base dependencies cannot change bytes under an existing commit tag. |
| REL-07 | Both units use the same native owner, correct config precedence, private credentials and bounded lifecycle. Conflicting owner is rejected; missing Pi/provider produces truthful availability rather than restarting forever or choosing another instance. |
| REL-08 | Upgrade, rollback and Docker↔host switching pass with schema receipts and dispatch paused. Old-reader incompatibility fails safely; native data and post-backup side effects/tombstones are preserved. |

## Worker and optional-module assertions

SEC-02 must choose and document the actual supported enforcement implementation before Canvas or Design accepts untrusted payloads. A worker's changed HOME, separate process or V8 heap limit is not a filesystem/network/total-memory enclosure. Prove no host service/project/Pi/controller/sibling access; bound CPU, memory, PIDs, output, pixels and wall time; test cancellation and enclosure setup failure. Core stays available while unsupported processing is unavailable.

Canvas: one document per authenticated native session; six tools; 512 KiB UTF-8 full-document writes, expected-version conflict and mutation fingerprint; quota reservation, exact-version read/image, real bounded image content, no Browser-session borrowing, safe version-aware live viewer. Verify hostile HTML navigation/CSS/fonts/forms/WebSocket/redirect/background egress against receivers, no reusable tokens in jobs/artifacts, stale render after remove/disable, restart cleanup and new identity after recreate. Raster-backed live iteration is not claimed to be interactive DOM in the user's browser.

Openfig structure: real licensed .fig files, one labelled instance-wide slot, conflicting second upload, stream limits, archive/path/expansion/schema/node/image attacks, isolated parser termination and validated index. Verify node order, transforms, direct text versus unresolved overrides, absent/invalid cover, empty/internal pages, source retention, shared-focus CAS/private browsing, headless read tools, disabled dependency-free removal and tombstone persistence.

Openfig frames: independently verify the supported released renderer API, Design rather than Slide behavior, actual frame content, explicit licensed offline fonts/assets, masks/clipping/effects/instances, bounded pixels/cache, source/font/renderer-version cache identity and cancel/delete races. Compare representative reference images. Saved cover and PNG-header checks alone do not satisfy this milestone.

## Evidence and completion

Record test command, fixture/source revision, environment, result and artifact location for each task/CP row. Redact credentials and do not expose private fixture content in logs. Mock tests, independent Pi tests, browser comparisons, systemd tests and final archive/image tests are distinct evidence.

Keep named blocked items with their missing API/control, owner, tested version and exit assertion; continue unrelated work. Passing documentation checks does not mark runtime tasks done. Gate 5 needs the full core/compatibility/migration/release matrix; Gate 6 Canvas; Gate 7a structure and Gate 7b frames are independent statuses. Release publication is separately authorized even after all preparation gates pass.
