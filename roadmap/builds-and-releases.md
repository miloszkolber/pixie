# Builds, services and releases

This file owns distribution, source identity and publication. Both binary variants and the matching Docker image are mandatory for every release, including prereleases. [Execution tasks](execution.md), [shared contracts](contracts.md) and [coverage gate](feature-coverage.md) apply to all artifacts.

## 1. Required build variants

| Build | Contents and deployment |
| --- | --- |
| `pixie-assistant` | Host Go supervisor and authenticated host API, used by the Docker controller/interface. No UI/controller/scheduler or parser/browser dependency in its core build. |
| `pixie` | One host Go executable containing that same assistant engine, controller, scheduler, module registry and embedded UI. One systemd user service, no external assistant executable or UI directory. |

Both use the selected independently installed Pi. Pi keeps its own runtime requirements; neither build installs Pi or packages another SDK. Browser/Canvas/Design workers remain optional declared dependencies. Missing workers do not block chat, settings or diagnostics.

Initial targets are Linux amd64 and arm64. Both builds must exist on each target. Additional operating systems need tested service, filesystem and packaging policies, not just successful cross-compilation.

## 2. Process composition

Assistant + Docker:

```text
host pixie-assistant -> selected host pi processes
        ^ authenticated loopback /pi
Docker pixie serve --mode controller -> UI/controller/scheduler/modules
```

Keep the current deliberate Linux host-network arrangement until an alternative is tested. Assistant binds literal loopback only. The Docker controller uses a private configured shared secret; read-only admitted project mounts retain their host absolute paths. Session discovery does not create mounts.

Full host:

```text
pixie.service -> pixie serve
                 + shared assistant -> selected pi processes
                 + controller/scheduler/embedded UI/module registry
                 + optional enclosed workers
```

Use the same tested host protocol through a private loopback listener initially, not a second assistant executable or duplicate methods. The composition root owns startup, internal credentials and complete shutdown. An internal listener is not a security boundary against the same OS user. Its credentials never reach UI, native children or module workers.

`pixie serve` defaults to full-host mode. `pixie serve --mode controller` never discovers, starts or locks local Pi, including during external-assistant failure. Full-host mode never silently attaches to an external assistant. Reject mixed configuration before starting anything. Both modes share controller state, scheduler, queues and HTTP APIs.

The internal dialing endpoint may be ephemeral. Durable authority MUST NOT depend on that ephemeral endpoint or token: migrate the existing deletion-agent binding as specified in [contracts.md](contracts.md#identity-and-durable-authority). Do not relax recovery authorization simply to make combined mode reconnect.

## 3. Shared source and build targets

Keep `assistant/go.mod` and `package/go.mod`. Expose a narrow `assistant/host` facade for config, startup, readiness, authenticated transport and bounded shutdown. It depends on assistant internals, not the controller. Both entrypoints use it; the application must not import another module's `internal` packages.

Use a repository-local module replacement to `../assistant` so both modules build from the exact same checkout. Do not resolve a moving remote assistant version. Keep shared schemas in `package/contracts` and checked generated bindings in consumers. Assistant compilation needs neither frontend generation nor web assets.

Provide these root commands, keeping existing build aliases working until migrated:

```text
bun run build:assistant
bun run build:host
bun run build:release
```

The first compiles only the assistant. The second builds/verifies the frontend then embeds it in full-host Pixie. The release target creates both architectures/variants and release metadata; it does not publish. Go-only assistant build also works from `assistant/` with ordinary `go build` and committed generated bindings.

Pin Go/Bun, Go modules, JS lockfiles, Mewa assets, CI actions and image inputs. Freeze OS/browser package inputs for a release using a recorded repository snapshot or equivalent pinned package set. A moving apt repository cannot justify calling two different payloads the same immutable build. Update dependency pins in a new source commit for security rebuilds.

Use clean output directories, `-trimpath`, deterministic archive order/permissions and stable timestamps derived from the source commit. Prefer CGO-free release binaries where dependencies allow it; race tests run separately. Never package placeholder embedded UI, source/test trees, secrets, unneeded runtimes or unlicensed fonts. Recheck a clean source diff and unchanged lockfiles after building.

## 4. CLI, configuration and service lifetime

```text
pixie-assistant serve --config /absolute/path/assistant.json
pixie-assistant doctor --config /absolute/path/assistant.json
pixie-assistant --version
pixie serve --config /absolute/path/pixie.json
pixie serve --mode controller --config /absolute/path/pixie.json
pixie doctor --config /absolute/path/pixie.json
pixie --version
```

Configuration and initial bounds are defined in [contracts.md](contracts.md#configuration-and-initial-bounds). The full configuration contains the same assistant section. Mode determines which inputs are valid. Preserve existing environment variables only through a documented migration adapter; do not silently prefer two contradictory settings.

Install one user service under the Pi owner: `pixie-assistant.service` for Docker mode, or `pixie.service` for full-host mode. The latter has no Requires/dependency on a separate assistant unit. Both share the assistant identity/lock for the canonical agent directory. Do not stop a conflicting service automatically.

Use absolute paths, private config, journal logging, a 30-second systemd stop ceiling, a 25-second application drain deadline, restart delay/start limit and exit 75 for an explicit requested restart. `Type=exec` means executable startup, not application readiness. See [assistant-go.md](assistant-go.md#9-systemd-and-artifact-delivery).

Full-host restart drains scheduler admission and outboxes, closes/cancels module work, terminates managed Pi descendants within the deadline and restarts the whole process. Libraries return lifecycle signals, never call os.Exit. Worker failure stays local. Browser disconnect never triggers a service restart.

Report controller/state/UI, assistant connection, native Pi compatibility/provider readiness and optional module health separately. No provider configuration is a usable setup state, not a global shell failure.

## 5. Commit-based release identity

Derive once from the selected source revision, not the workflow branch name or a PR merge ref:

```sh
SOURCE_COMMIT=$(git rev-parse --verify 'HEAD^{commit}')
RELEASE_ID="sha-$(printf '%s' "$SOURCE_COMMIT" | cut -c1-12)"
```

Validate the full revision as 40 lowercase hex characters and ensure HEAD is exactly the requested commit. `RELEASE_ID` is `sha-` plus its first 12 characters. Do not use git-describe, semantic versions, timestamps or workflow run numbers as the release identity. A colliding existing short ID with a different full commit is a hard failure requiring an explicit naming-policy change; never retarget it.

Use the identical RELEASE_ID for:

| Surface | Exact rule |
| --- | --- |
| Git tag | `sha-<12>` pointing to SOURCE_COMMIT |
| GitHub Release title/tag | `sha-<12>` / same tag |
| Binary build version | RELEASE_ID, with separate full revision and variant fields |
| Host archives | Names below |
| Docker tag | `ghcr.io/miloszkolber/pixie:sha-<12>` |
| OCI labels | `org.opencontainers.image.version=RELEASE_ID`, `org.opencontainers.image.revision=SOURCE_COMMIT` |

Example names, not an actual publication:

```text
pixie-assistant-sha-71590cac4892-linux-amd64.tar.gz
pixie-assistant-sha-71590cac4892-linux-arm64.tar.gz
pixie-sha-71590cac4892-linux-amd64.tar.gz
pixie-sha-71590cac4892-linux-arm64.tar.gz
checksums.txt
release-manifest.json
```

Both binary names inside archives remain `pixie-assistant` or `pixie`, so systemd paths do not change each release. Each archive includes its unit, non-secret config example, concise install instructions and applicable notices. Optional module helpers that are distributed additionally must cover the same supported architectures, declare checksums/runtime requirements and never replace either core build.

The image's commit tag is one multi-architecture index with linux/amd64 and linux/arm64 manifests. Verify each runnable platform image, its controller-only entrypoint and embedded build identity. Attestation descriptors are not runnable architectures. Do not publish independent architecture tags as a substitute for the canonical multi-architecture tag.

The manifest records RELEASE_ID, full commit, clean source tree, build recipe/toolchain/dependency identities, both variants/architectures, archive hashes, protocol/schema versions, tested Pi profiles, UI revision and image index/platform digests. Include SBOMs and provenance for distributed components. Never infer Pi's native version from the Pixie release ID.

Hash IDs have no version ordering. Compatibility uses explicit protocol/capability/schema versions. Release chronology uses publication metadata and verified source ancestry, not lexicographic hash sorting.

## 6. Triggers and authorization

Target policy after explicit approval to enable publication: validate every main push; automatically create a commit-named release for a successful release-relevant main commit. Include assistant, controller, contracts, UI/assets, dependency/build/deployment and workflow changes in release-relevant paths. Documentation-only pushes can validate without creating a binary release. A manual release input is a full commit SHA reachable from main, with an explicit prerelease flag if required.

PRs, feature branches and scheduled dependency/security validation do not publish. Scheduled rebuilds do not overwrite an existing commit release; changed dependency inputs require a new source commit. A release-tag push does not trigger another build/publish loop. Development/dirty builds can be tested but cannot enter release publication.

The initial workflow/policy enablement requires explicit approval. Once approved, eligible main commits publish under that standing policy; an agent must not ask for an invented per-artifact version. Until policy approval, prepare and test the pipeline without publishing. Live deployment remains a separate action. This roadmap/documentation commit grants none of those runtime side effects.

Keep minimal publication permissions in the publish job only, pinned actions and protected authorization controls. Never execute untrusted PR code with release credentials. Manual inputs are validated as revisions, not interpolated shell commands.

## 7. Complete-set publication and retries

Build/test all four host archives and both runnable image platforms before promotion. Test assistant-only against the actual candidate Docker image by digest; test full-host from its archive outside the checkout. Reuse the candidate payloads after testing; do not rebuild during upload.

Publication order:

1. Verify source identity, coverage/cutover eligibility and every required artifact job.
2. Stage the image and obtain its immutable index/platform digests. Prepare a draft GitHub Release at the commit tag; verify an existing tag has the same full commit.
3. Attach all archives, checksums, manifest, SBOM/provenance and concise compatibility/limitations notes. Verify downloaded staged payloads against hashes and source identity.
4. Publish the image's `sha-<12>` tag to the verified digest. Then publish the complete draft Release. Enable GitHub immutable releases as an explicitly approved repository setting; attach everything before immutable publication.
5. Verify published assets, tag target, image digest/platforms and binary version output. Only then update an optional `latest` image alias and Release latest metadata.

GitHub Releases and GHCR have no single cross-service transaction. If failure occurs between image tagging and Release publication, keep the release incomplete/draft, do not update latest, record the exact step, and resume with the same verified hashes/digests. A published commit tag must never be moved to a different image or source. Existing identical payloads are a no-op; a mismatch aborts. Do not use --clobber on published artifacts.

Serialize publication for one RELEASE_ID and serialize latest promotion across main. Do not cancel an in-progress publication because a newer build starts. A slower older build may publish its own immutable release but must not move latest backward; compare verified source ancestry/current approved release, not job completion time.

A prerelease uses the same commit identity and full artifact set, marked through Release metadata. Promotion changes metadata/approved aliases, not payloads. Keep legacy semantic/npm releases intact; retire their automatic publication when the new pipeline cuts over. Do not republish old tarballs to fit the new scheme.

## 8. Verification matrix

Both architectures must run their final binaries, not only cross-compile them. Required in assistant + Docker and full-host modes:

- Independent vanilla Pi and configured Pi; custom HOME/PATH/agentDir and symlinked executable; missing/incompatible Pi; optional extensions absent.
- Every mandatory coverage row, images, clone/fork, retry/compaction, queued work, lost acknowledgments and pending UI recovery.
- Real user-unit startup/readiness, stop, requested restart, hung children, port/config/lock conflicts and complete cleanup.
- Core functionality without an assistant Node/Bun runtime, optional module dependencies or external UI assets. Pi's own runtime is installed only when its chosen distribution needs it.
- Credential/authority stability across restart, internal endpoint changes, paired-secret rotation and mode switch; no pending deletion replay against a different authority.
- Upgrade/rollback/schema migration, ungrouped sessions and archives, existing outbox/deletion records, safe uninstall and no native data relocation.
- Docker tag/Release/tag/binary/manifest/source identity agreement, both OCI platforms, missing-job failure, duplicate publish, collision, partial upload and concurrent main promotion.

Measure total controller/assistant/Pi-child/worker cost separately by deployment. Main-process RSS alone is not a performance comparison.

## 9. Upgrade, rollback and mode switching

Both variants preserve native Pi files and share one logical application schema. Explicitly configure the application data path when switching Docker/host; validate ownership and admitted roots. Stop admission, settle or explicitly interrupt work, preserve uncertain claims, back up metadata/private config and release the old lock before starting the new mode. Never run both writers concurrently.

Use the authority/migration rules in contracts.md for pending deletions and session metadata. A stable native session ID does not by itself authorize destructive recovery at another endpoint. Reopen without resending prompts. Keep previous immutable release artifacts/digests and document schema rollback compatibility; replacing a binary is not always enough.

Uninstall removes only the selected Pixie service/artifacts. Native installation, credentials, transcripts and retained user-authored module documents are not implicit deletion targets.

## References

The requested [llama.cpp release page](https://github.com/ggml-org/llama.cpp/releases) illustrates continuous source-bound builds; its inspected releases use build-number tags. Pixie deliberately uses the source hash directly as requested. [GitHub immutable releases](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases) and [draft-first publication](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository) define the platform controls. These are policy references, not evidence that Pixie's new pipeline has run.
