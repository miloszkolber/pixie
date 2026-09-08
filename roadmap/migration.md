# State migration, ownership and rollback

Owner B/G with A/F. Apply this contract to the SDK-to-Go transition, upgrades within either deployment, and Docker+assistant to combined-host switching. All migration tests use disposable state. A coding request does not authorize relocating or rewriting the operator's live Pi state.

## State inventory

Location alone does not determine ownership. The existing assistant stores some Pixie metadata beneath the native agent directory. Do not lose that metadata by interpreting “do not migrate Pi” as “ignore everything under agentDir”.

| Existing state | Owner | Migration rule |
| --- | --- | --- |
| `<agentDir>/sessions/**/*.jsonl` | Pi | Never rewrite, relocate, truncate or normalize as a Go migration. Read bounded projections; preserve unknown entries and native identities. |
| Native auth.json, settings.json, models.json, resources, packages, trust and agent definitions | Pi/operator; agent authoring is an explicit user action | Not migration targets. Do not copy credentials into controller storage or perform implicit extension installs/trust changes. |
| `<agentDir>/pixie/identity.json` | Pixie assistant identity | Preserve/adopt only after validating the same canonical installation/agent directory. Keep stable identity distinct from fresh process epochs and release ID. |
| `<agentDir>/pixie/sessions.json` | Pixie assistant catalog sidecar | Import validated session path/cwd, archivedAt and parent associations into the declared new owner. Cross-check native identity; do not synthesize missing transcripts. |
| `<agentDir>/mcp.json` legacy name-map, `<agentDir>/mcp-sessions.json` | Existing Pixie MCP administration records | Detect exact legacy schema and preserve connection intent, membership and host-only secrets. Native `{mcpServers: ...}` is adapter-owned configuration, not this legacy map. A schema collision is a diagnostic, not permission to overwrite. |
| Controller `config.json` and project/session/objective state | Pixie controller | Preserve IDs, admitted roots, grouping, visibility, goals/tasks, user choices and schema. Inventory every concrete store in MIG-01 before conversion. |
| Controller `pi-session-queues.json` | Pixie durable delivery ledger | Preserve mutation identities, pending/attempted/uncertain claims and paused state. Never restore an older runnable queue over work already sent. |
| Controller `schedules.json` | Pixie schedules and execution ledger | Preserve schedule IDs, timezone, occurrence/run identities, native session links, mutation replay and uncertain/interrupted claims. Migration does not run missed jobs. |
| Controller `pi-session-deletions.json` | Pixie deletion authority | Preserve requested/confirmed phases and validated agent binding. An older backup must not restore deleted content or authorize a delete against a different agent. |
| Controller `mcp-modules.json`, Browser panel/lease records and artifacts | Pixie module owners | Preserve complete enablement intent; validate transient handles after restart. Do not revive an old browser process/lease merely because a saved handle exists. Retain unknown module settings without executing them. |
| Versioned browser workspace preferences/drafts | Browser/Pixie presentation | Map former chat tabs to primary selections and file/diff/Browser tabs to secondary selections. Preserve drafts separately; reject stale identities, not legitimate unsent text. |
| Canvas documents and Design source/index/selection/tombstones when introduced | Respective Pixie module | Preserve durable source/revisions and deletion generations. Evict only regenerable caches; worker absence never justifies deleting source. |

MIG-01 produces an exact allowlist of existing files/schema versions and relevant browser storage keys from current source, including controller stores not individually named above. Each receives preserve/convert/rebuild-cache/retire-after-confirmation status and a fixture. Unknown files remain untouched. Do not write a wildcard cleanup of agentDir or dataDir.

The target keeps controller application metadata in its configured Pixie data directory. A small stable assistant identity/ownership sidecar may remain under the existing `<agentDir>/pixie` namespace so both builds identify the same installation. Registry, archive and connection ownership must each have one final store, not dual writers. Secrets needed by native integrations remain on the host side of the boundary; encrypted transport does not make controller persistence appropriate.

## Prepare and inspect

Expose a migration dry-run with no writes, package loading, model calls or native configuration changes. It reports source/target roots, schema versions, required conversions, identity conflicts, missing dependencies and backup needs without dumping secrets or transcripts. A schema newer than the binary supports fails closed for mutation; safe diagnostics remain available.

Before apply, pause schedule/outbox dispatch, settle or explicitly interrupt work, and stop the previous owner. Validate no active managed native writers remain. The old assistant uses proper-lockfile; a new Go flock on a similar pathname is not automatically compatible. Test the transition locking protocol or require explicit stopped-owner verification. Never signal an unrelated TUI or infer cooperative locking from its absence in Pixie's process table.

Back up Pixie-owned durable stores and configuration with restrictive permissions and record their hashes, source build/schema and canonical paths. Native state is not rewritten; an operator backup of it is separate and authorized. Confirm source/destination path mapping instead of assuming Docker `/var/lib/pixie` and host XDG data identify the same files.

## Transaction and recovery

Apply migration through a versioned plan and durable receipt with source identity, input hashes, destination schema and phase. Stage converted files in the destination filesystem, validate referential integrity, synchronize files/directories as required, then publish the migration checkpoint/pointer last. Do not claim one rename makes arbitrary multi-file conversion atomic.

A failed apply leaves old committed metadata intact or resumes from an explicit recorded phase. Re-running with identical inputs is idempotent; changed source data conflicts rather than being merged opportunistically. Test permission errors, disk full, interruption before/after each rename, lost receipt and startup with partial staging. No missing primary execution/deletion ledger may silently fall back to an older backup.

Archive/grouping imports validate native session ID/path/cwd and retain unknown/missing sessions as reported unresolved metadata rather than deleting files. Duplicate native IDs block ambiguous actions. Parent/fork links may be retained even when a source is unavailable; do not invent a new branch. Historical pixie-input/plan records remain readable in native transcripts; new presentation metadata does not require editing old JSONL.

Stable deletion authority needs explicit mapping during a topology change. The existing controller binds deletion claims to its agent connection scope. A random embedded-loopback credential or changed endpoint must not automatically make a previous deletion claim valid for a different installation. Verify the same stable native owner, retain confirmed tombstones, and quarantine unresolved destructive claims for explicit reconciliation.

## Switching deployment mode

Docker to combined host: quiesce and stop the Docker controller and host assistant, verify native ownership release, map/copy Pixie state only with authorization, validate permissions and source identity, then start only `pixie.service`. The combined service has no dependency on pixie-assistant.service. Its internal ephemeral credential is not copied from a host secret file.

Combined to Docker: quiesce/stop pixie.service, validate state mapping and schema, install/start only the assistant unit, and start the exact matching digest-pinned Docker controller in external mode. Configure the external assistant bearer privately. Re-admit/check read-only project mounts with the same absolute paths used by Pi; lack of a mount affects Files/Git, not native session identity.

Do not auto-enable both units or automatically copy an entire HOME. Separate installations using different agent directories are allowed; conflict detection is scoped, not a global process-name kill. Reopen known sessions only after identity is established. Schedules/outbox remain paused until migration and readiness checks pass and the operator resumes dispatch.

## Rollback is not ledger rewind

Document old-reader compatibility for every changed Pixie schema. A previous binary is sufficient only when it can safely read the current state. Otherwise use a tested reverse migration or a verified compatible snapshot plus reconciliation of all subsequent effects.

Never restore an old queue/schedule/deletion snapshot as authoritative runnable state after the newer version may have dispatched work or removed resources. Native tool side effects cannot be undone by restoring JSON. Preserve monotonic dispatch identities and tombstones; when an old schema cannot represent them, keep dispatch disabled and require explicit reconciliation. Do not replay “missed” occurrences as part of rollback.

A rollback receipt names the source and target schema, actual restored files and unresolved work. Report loss of regenerable cache separately from loss of durable user data; only the former can be automatic. Test no-op rollback, interrupted rollback, newer unknown schema, removal after backup, accepted prompt after backup and topology-switch rollback.

## Completion and uninstall

MIG-01 inventory, MIG-02 transactional conversion, MIG-03 topology switch and MIG-04 rollback assertions are release-blocking for Gate 5. Both architectures and binary compositions must preserve native file hashes during discovery/migration, user-visible archive/project/attachment state and execution uncertainty.

Uninstall removes only the selected Pixie executable/unit/config by default. Deleting Pixie data or optional worker state is a separate explicit action. Never delete native auth/settings/transcripts, installed extensions or agent definitions because they were visible in Pixie. Removing a document cannot recall bytes already downloaded or recorded in a Pi transcript.

Sources: [assistant storage](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/storage.ts), [session sidecar](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/sessions.ts), [MCP stores](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/assistant/src/extensions/mcp-connections.ts), [queues](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/session_queues.go), [schedules](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/schedules.go), [deletion ledger](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/session_deletions.go) and [connection identity](https://github.com/miloszkolber/pixie/blob/f63d0d5bcb7f6e342058734b2e741ad2619d8867/package/internal/controller/pi_client.go).
