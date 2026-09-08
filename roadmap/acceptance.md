# Cross-boundary acceptance

This supplements FC01–FC33 in [feature-coverage.md](feature-coverage.md). It is not another feature inventory or progress ledger. [execution.md](execution.md) owns task status; [contracts.md](contracts.md) owns limits, identities and transitions. Evidence from the third review is not production-fix acceptance.

Use disposable native/application directories and owned fixtures. Run targeted unit/contract tests during implementation, then the actual controller plus assistant-only artifact and combined host artifact on both supported architectures. Native Pi, bridge, browser assets, systemd and optional worker checks must be distinguished from mocks. No tests use live credentials, repositories or documents by default.

| ID | Required assertion | Task / retained rows |
| --- | --- | --- |
| X01 | Unapproved Host fails even with a matching Origin; normalized approved local/public authorities pass; untrusted forwarded headers cannot change authority. Cover HTTP, WS upgrade, files, module APIs and authenticated service calls. | FIX-10, SEC-01; FC01/18/25/30/31 |
| X02 | Git views do not execute configured clean/process/textconv/diff/fsmonitor/hook commands. Use marker probes, native config includes, nested attributes, worktree/submodule metadata and output/encoding cases. Raw conversion limitations are visible. | FIX-11; FC30 |
| X03 | Controller subprocess cancellation/output overflow terminates only its managed descendants and returns within the configured run-plus-drain ceiling despite inherited pipes. Exercise both final Docker entrypoint and host process cleanup. | FIX-12, BUILD-05; FC22/30/31 |
| X04 | Inject storage failure before/after each publication/durability step. Distinguish known-uncommitted from installed/uncertain results, preserve mutation identity and reconcile primary state. No unsafe retry or stale backup resurrection. | FIX-13, MIG-01; FC05/08/09/19/21/25/26/29/32/33 |
| X05 | A fixture module's registered MCP, management and artifact routes work through the real top-level HTTP handler without module-name branches. Unknown API/MCP paths are non-success errors, never index.html. Reject overlaps/core-route takeover. | ROUTE-01; FC31/32/33 |
| X06 | Browser string IDs, host positive-integer IDs and native optional string IDs round-trip without coercion or collision. Reconnect changes transport mappings but retains durable mutation identity and uncertainty. | API-02; FC01/03/05/13 |
| X07 | Individual-image, aggregate base64, text/resource and serialized-message limits agree at browser/controller/assistant/native boundaries. Reject before dispatch with exact units; Unicode/escaping cannot bypass byte limits. | LIMIT-01; FC04/05 |
| X08 | Aggregate buffer accounting limits simultaneous large inputs/outputs/replay. Stop/UI cancellation remains admitted under ordinary-load saturation; no partially parsed request is executed. Measure decoded allocation separately from serialized-byte quotas. | LIMIT-01, GO-02; FC03/05/06/13 |
| X09 | A valid 16–64 MiB Design index uses its dedicated bounded artifact path, while generic JSON state stays capped at 16 MiB. Queries/cursors stay small; no full-index browser/host response or arbitrary worker-supplied path. | LIMIT-01, FIG-02/03; FC33 |
| X10 | Stop pauses automatic dispatch, settles/cancels pending UI and verifies remaining work ended. Unverifiable detached work triggers bounded managed-child teardown, not false idle. Report uncertain external effects and prohibit automatic resumed prompts. | LIFE-01, GO-05/07; FC05/13/15/24/28 |
| X11 | At the child-resident cap, Release idle runtime offers a safe path to free capacity while preserving draft, history, archive/grouping and selection. Active/pinned/unknown work cannot be silently evicted. TUI handoff remains separate. | LIFE-01, UI-05; FC02/06/08/24/28 |
| X12 | Contained workers have verified pre-exec placement, no unauthorized mounts/sockets/egress, bounded memory/swap/PIDs/CPU/wall/scratch bytes/inodes/output and complete cleanup. Worker failure cannot consume the controller's whole allowance or kill Pi. | SEC-02; FC31/32/33 |
| X13 | Legacy Docker Browser posture is explicitly non-isolated. Direct-host Browser does not silently inherit it. Canvas/Design refuse processing without their required enclosure; core and retained-document removal remain available. | SEC-01/02, EXT-03; FC31/32/33 |
| X14 | Upgrade with old browser tabs, old lazy assets and pending/uncertain mutations preserves local drafts, never sends a fresh duplicate and negotiates browser compatibility separately from host v2. Explicit recovery replaces blank panels/reload loops. | UI-07, PKG-03; FC01/03/04/05/06 |

## Fixtures and completion rules

X01 includes IPv4/IPv6 loopback, localhost, selected port, equivalent default ports, malformed authorities, approved reverse proxies and absent browser Origin for explicitly authenticated non-browser service calls. Matching client-supplied Host/Origin is not sufficient. Authentication and approved network authority remain independent checks.

X02 uses only a harmless marker/counter in a temporary directory. Test process filters and configuration/attribute changes, not only external diff. A copied command-policy fixture is useful early, but release acceptance calls the real Files/Git endpoint. A hidden conversion-dependent exception that executes a helper in the controller fails the test.

X03 uses a child that outlives a short test deadline and retains a pipe; shorten the configured test grace rather than waiting for production timeouts. Assert process exit and descriptor cleanup, not only a returned timeout string. Finite pipe drain and process-group cleanup are separate assertions.

X04 records the state visible after each injected error and after restart. A returned storage error is not always “nothing changed.” Repeated mutation IDs with identical input reconcile the original operation; different input conflicts. Queue/schedule/deletion rollback must not rewind effects that occurred after a backup. No multi-file transaction is claimed from a single rename.

X06 retains current browser request/replay strings during a compatible migration. Host v2 changes require explicit adaptation. URL `#/v2` is a layout-schema choice, not a transport negotiation. Never use a release hash as a monotonically ordered protocol or database version.

X07/X08 count base64 characters, serialized UTF-8 bytes, decoded image pixels and structured allocation separately. Keep the current 4.5 MiB per-image and 24 MiB aggregate base64 budgets as the compatibility baseline. The whole-frame cap is additional, not a replacement. Any lower accepted input limit needs an identified FC04 change and approval; do not truncate text/images to pass it. Admission reservations are released after cancellation/error and bounded for slow incomplete readers.

X09 stages a representative normalized index over 16 MiB and below 64 MiB without oversized individual nodes. Validate it without raising every generic state/transport limit. Metadata commit points refer to an immutable validated index artifact. A malformed index or missing source remains a recoverable error, not an empty document slot.

X10 does not assert that killing a local process reverses a remote side effect or job already launched elsewhere. Verify the local generation has no further dispatch authority and expose ambiguity honestly. Native optional-extension behavior is exercised with real fixtures; simulated pins do not prove native cancellation.

X12 includes scratch exhaustion and inode flooding, denied cgroup delegation/user namespaces, unexpected inherited descriptors, external receiver and host-service probes, cancellation during startup, late artifacts and removal after a crash. A process cannot run untrusted input before limits are active. Do not mount the user's runtime directory, D-Bus socket, Docker socket or writable cgroup tree into the worker.

X14 includes both deployments, protocol-compatible asset changes and intentionally incompatible peers. Persist/recover the current draft before offering refresh; keep per-client drafts private. A client cannot infer that a mutation failed merely from its old bundle or lost socket. Do not retain unlimited old asset bundles as a workaround; provide a tested recovery state.

## Existing gates remain mandatory

Keep all five content-filled six-slot layouts, Mewa keyboard/lifecycle tests, independent native Pi/bridge profiles, archive/ungrouped metadata, schedules, images/fork/retry/compaction and both release compositions. These additions do not replace existing FC rows or permit feature deletion.

Gate 5 requires X01–X08, X10–X11, applicable Browser X12–X13, X14 and the existing core matrix. Canvas Gate 6 adds its X04/X05/X12/X13 cases. Openfig structure adds X09 and all applicable worker/storage/authority cases; real frame rendering still has its independent milestone. No review probe, mock image or saved cover closes a future integration gate.

Record actual command, fixture revision, platform/profile, result and artifact source for every completed task. Keep probe evidence in the review separate from the implementation ledger. Report unresolved native API and renderer boundaries explicitly rather than claiming all doubts are removed by documentation.
