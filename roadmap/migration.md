# State migration and rollback

Owner A/B/G with F. This is the detailed procedure for MIG-01, GO-09 and PKG-03. [contracts.md](contracts.md) remains authoritative for identity, target storage, publication/durability outcomes and runtime state transitions. [feature-coverage.md](feature-coverage.md) defines retained behavior; [acceptance.md](acceptance.md) supplies cross-boundary tests. Do not maintain a second migration status ledger here.

## State inventory

Location does not determine ownership. Pixie stores some metadata beneath the native agent directory. Preserving Pi must not discard those sidecars or treat them as permission to rewrite native configuration.

| Existing state | Owner | Required handling |
| --- | --- | --- |
| Native sessions/**/*.jsonl | Pi | No migration rewrite, move, truncation or normalization. Retain native IDs, branches, summaries and unknown/custom records. |
| Native auth/settings/models/resources/packages/trust | Pi/operator | Not migration targets. No credential copy into the controller, package installation or automatic trust change. |
| Native agent-definition Markdown | Operator, with explicit Pixie authoring actions | Preserve; migration is not an authoring or cleanup request. |
| agentDir/pixie/identity.json | Pixie assistant | Validate and preserve stable identity for the same native storage. New assistant metadata uses the XDG location defined in contracts.md; never confuse it with a fresh boot epoch. |
| agentDir/pixie/sessions.json | Pixie assistant sidecar | Import validated archive/parent/catalog associations; cross-check native file identity/cwd without synthesizing missing transcripts. |
| Legacy agentDir/mcp.json and mcp-sessions.json | Pixie-managed MCP records where their exact schema matches | Preserve host-side secrets, connection intent and session membership. Native {mcpServers: ...} belongs to the installed adapter and is not a legacy map to convert. Reject schema collisions. |
| Controller config, projects/session associations, model visibility, objectives/tasks/questions | Pixie controller | Preserve IDs, admitted roots, user settings and state. Make project membership optional only where contracts permit it; schedules stay project-scoped. |
| pi-session-queues.json | Durable delivery authority | Preserve mutation/delivery IDs, attempted/accepted/uncertain claims and paused work. Never restore an older runnable queue over dispatched effects. |
| schedules.json | Schedules and execution ledger | Preserve IDs, timezone, occurrence/run identities, native session links, replay records and interrupted/uncertain claims. Migration does not dispatch missed jobs. |
| pi-session-deletions.json | Deletion authority | Preserve requested/confirmed phases and tombstones. Verify old binding before migration; otherwise retain recovery-blocked state. |
| mcp-modules.json and module configuration | Pixie modules | Preserve complete desired state, including unknown entries without executing them. Reconcile readiness independently. |
| Browser panel/lease records and artifacts | Browser/controller | Revalidate transient handles; saved IDs do not prove an old process or lease still exists. Classify regenerable cache separately from retained data. |
| Browser layout preferences and drafts | Client presentation | Map valid chat/file/diff/Browser tabs into independent selections; preserve unsent text separately from stale layout IDs. |
| Canvas/Design documents, revisions, source/index/focus/tombstones when introduced | Respective module | Preserve durable source and deletion generations. Rebuild or evict only declared derived data; unavailable workers do not authorize source deletion. |

MIG-01 must enumerate every concrete file, schema and browser-storage key from the implementation checkout, including controller stores summarized above. Assign preserve/convert/rebuild-cache/explicitly-retire, an owner and a fixture. Unknown files stay untouched; no wildcard cleanup of HOME, agentDir or dataDir.

Keep one final writer/store for each concern. Follow contracts.md for XDG assistant state and controller data. Convert project-required queue/deletion keys only after paired host/native associations are verified; retain projectId as optional metadata, not a fabricated hidden all-files project. Cwd discovery never creates filesystem admission or a Docker mount.

## Read-only preparation

Provide an inspect/dry-run operation with no writes, package loading, extension execution, model calls or native configuration changes. Report redacted source/target roots, schema versions, conversions, conflicts and backup requirements. A newer unsupported schema blocks affected mutations while diagnostics remain available.

Before apply, pause schedule/outbox admission, settle or explicitly interrupt active work and stop the previous owner. Test transition locking: the TypeScript service's proper-lockfile identity and a Go flock are not automatically coordinated. Require verified release of the old managed owner; never terminate an independent TUI or assume it honors Pixie's locks.

Back up Pixie-owned durable stores/private configuration with restrictive permissions, hashes, source build/schema and canonical paths. Native backup is a separate operator action, not a reason to rewrite native files. Map container volumes to host destinations explicitly; `/var/lib/pixie` and an XDG directory do not become the same state by sharing a product name.

## Staged conversion and uncertainty

Use a versioned migration plan/receipt containing source identity, input hashes, target schema and phase. Stage converted files on the destination filesystem, validate references and expected input revisions, then publish checkpoints/pointers in the defined order with required synchronization. One rename does not make multiple unrelated files atomic.

Re-running identical inputs is idempotent. Changed inputs conflict instead of being merged opportunistically. Follow contracts.md after a post-rename error: the primary may already have changed. Retain mutation/receipt identity, reread and reconcile the validated primary, and report uncertainty until the outcome is established. Do not claim every returned error left the old state untouched.

Test interruption and failure before/after staging, backup replacement, primary replacement, directory sync, receipt publication and response delivery; include permissions, disk full, lost receipt and partial staging. Missing/corrupt execution or deletion authority never silently falls back to an older backup.

Keep unresolved catalog metadata visible rather than deleting its native source. Duplicate native IDs block ambiguous actions. Preserve parent links when their source is missing without inventing a new branch. Existing pixie-input and plan records remain readable; new UI metadata does not justify modifying old JSONL.

Deletion claims require special treatment. An ephemeral embedded endpoint, new boot credential or changed topology is not sufficient proof that an old claim belongs to the current native storage. Migrate only with verified pairing and file association. Preserve confirmed tombstones and quarantine unverifiable destructive recovery as recovery-blocked.

## Deployment switching

Docker plus assistant to combined host: pause dispatch, stop the Docker controller and the managed host assistant, verify ownership release, validate/copy only authorized Pixie state with explicit path mapping, then start pixie.service. It has no dependency on pixie-assistant.service. Its private internal transport credential is not a copied external bearer.

Combined host to Docker plus assistant: pause dispatch and stop pixie.service; validate schema/path mapping; install/start the host assistant with its private external credential; start the matching digest-pinned Docker controller in controller-only mode. Revalidate read-only mounts at Pi's actual absolute paths. Missing file mounts affect Files/Git availability, not native conversation identity.

Do not automatically enable both units, copy all HOME or stop a different installation. Ownership conflicts are scoped to canonical native storage. Reopen only after pairing/capability/state checks pass. Resume schedules/outbox explicitly after migration, never as an accidental consequence of startup.

## Rollback cannot rewind external effects

Declare old-reader compatibility for each changed schema. An older binary is sufficient only when it can safely read current state. Otherwise require a tested reverse conversion or compatible backup plus reconciliation of all later effects.

Never restore an older queue/schedule/deletion snapshot as runnable authority after the new version may have dispatched work or deleted content. Restoring JSON does not undo native tool effects. Preserve monotonic claims and tombstones; when the older schema cannot represent them, keep dispatch disabled and require explicit reconciliation. Do not replay missed occurrences during rollback.

A rollback receipt names schemas, restored files, retained post-backup effects and unresolved work. Test interrupted/no-op rollback, unknown newer schema, accepted prompt and confirmed deletion after backup, and rollback across both topologies. Cache loss is not durable-user-data loss; only declared regenerable cache can be discarded automatically.

## Completion and uninstall

MIG-01 is complete only with the exact state inventory, dry run, phase/failure fixtures, identity mapping, both switching directions and schema-aware rollback. GO-09 and PKG-03 repeat relevant checks with actual released archives on both architectures. Hash native files around discovery/migration; preserve archive/grouping, attachments, MCP membership, execution uncertainty and deletion authority.

Uninstall removes only the selected Pixie binary/unit/config by default. Removing Pixie/module data is a separate explicit action. Native credentials, settings, transcripts, extensions and agent definitions are never implicit uninstall targets. Document removal cannot recall bytes already downloaded or included in native transcripts.

Source locations and findings F21/F25/F32 are recorded in [repository-review.md](repository-review.md); the [source appendix](sources.md) pins the native/controller implementations. This plan carries forward the detailed migration work from the reconciled branch without changing the later contract's identities, locations or persistence semantics.
