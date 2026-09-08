# Builds and releases

Owner G with A/B/F. This file defines the only release identity, artifact matrix, composition and publication policy. Requirements apply to every new release and prerelease after this workflow is implemented. Existing npm/image/GitHub releases are not renamed or overwritten.

## 1. One commit-derived identity

Resolve the requested source once to the full 40-character lowercase Git commit SHA. Define `releaseId = "sha-" + sourceCommit[0:12]`. The same string is the Git tag, GitHub Release title, binary version and Docker tag. Archive names include it. Store the full commit everywhere that validates identity; a short name is not the integrity proof.

Example only, using an existing roadmap commit:

```text
sourceCommit: d8f73c35ae5fd286c557fff88d5868bde0d3c3ab
releaseId:    sha-d8f73c35ae5f
Git tag:      sha-d8f73c35ae5f
Release:      sha-d8f73c35ae5f
Docker:       ghcr.io/miloszkolber/pixie:sha-d8f73c35ae5f
```

This is a direct commit-ID scheme. The supplied llama.cpp releases are an example of downloadable commit-associated builds, not a requirement to copy their build-number tags. Do not introduce `v*`, `pixie-assistant-v*`, timestamps, run numbers or different prefixes for Docker versus binaries.

Derive from the checked-out source commit, not the workflow's branch HEAD, PR merge SHA, a UI label or the latest tag. Release inputs require an exact commit; verify it belongs to the approved release branch and approval. Check annotated/lightweight tag targets by peeling to the commit. Check every existing name before upload: a matching short name with a different full commit is a hard conflict. Do not silently extend or replace one artifact's identity to resolve a collision; require an explicit naming change or a new source commit.

`latest` is not a required output and is not the installation default. Remove automatic moving-alias promotion from the release path. Old aliases may remain for legacy consumers but must not be advanced by ordinary main/schedule CI. Prerelease status is GitHub metadata, not a different identity for the same source. Do not publish two artifact sets under one release ID.

## 2. Mandatory artifacts

| Variant | Contents | Required platforms |
| --- | --- | --- |
| `pixie-assistant` | One Go assistant binary and shared native-Pi supervisor; no controller/UI/Chromium/parser | linux/amd64, linux/arm64 |
| `pixie` | One Go binary containing that same assistant engine, controller and compiled embedded web UI | linux/amd64, linux/arm64 |
| Docker `pixie` | Controller/interface in explicit external-assistant mode with declared optional Browser runtime; never starts host Pi inside the image | linux/amd64, linux/arm64 |

Four required host archives:

```text
pixie-assistant_<release-id>_linux_amd64.tar.gz
pixie-assistant_<release-id>_linux_arm64.tar.gz
pixie_<release-id>_linux_amd64.tar.gz
pixie_<release-id>_linux_arm64.tar.gz
```

Each contains the executable, its corresponding systemd unit, private-configuration example, installation instructions and applicable licenses/notices. The full-host archive contains no second assistant executable or installer that downloads one. Both host binaries use separately installed Pi and its own runtime. Optional workers and Browser dependencies are named separately; neither absence nor disable blocks core chat.

The companion GHCR image has one multi-platform index at `ghcr.io/miloszkolber/pixie:<release-id>`. Record the immutable index digest and platform manifest digests. Do not create an assistant image as a substitute for the host assistant. A release is incomplete if any archive or either Docker platform is missing, mismatched or untested.

Required release-level assets: `SHA256SUMS`, `release.json`, the matching prebuilt Compose example, dependency/SBOM and provenance evidence. Checksums cover archives and static manifest/example/SBOM assets without self-referential checksum fields. Attestations identify their actual subjects and source commit. Keep release metadata small and machine-readable; do not embed logs or source files in the runtime archives.

## 3. Composition

Keep separate `assistant/go.mod` and `package/go.mod`. Add a narrow public `assistant/host` facade. It owns engine construction, readiness, endpoint, cancellation and bounded shutdown; commands own flags, signals and process exit. The application must not import assistant/internal, duplicate supervisor code or start pixie-assistant as a separate executable.

Resolve the assistant module from this exact checkout with a committed local module requirement/replace or equally explicit tested workspace setup. Verify the selected module directory and build metadata. The release build must not download a stale assistant version with the same module path. Keep the standalone dependency graph free of frontend/controller/worker packages.

Supported CLI compositions:

```text
pixie-assistant serve --config /absolute/path/assistant.json
pixie serve --assistant=embedded --config /absolute/path/pixie.json
pixie serve --assistant=external --config /absolute/path/pixie.json
```

Direct-host defaults to embedded, while the Docker entrypoint explicitly selects external. A failed embedded start never silently connects to an unrelated daemon. External mode does not discover Pi, acquire native-session ownership, materialize a bridge or create a child Pi process.

Initially embedded mode connects the controller to the shared host protocol through a private ephemeral loopback endpoint and boot-scoped credential managed inside the process. No second operator-managed assistant bearer or public route is required. Do not log/pass that internal bearer to native children. Both modes pass the same wire/semantic fixtures; an in-process transport optimization later must remain equivalent.

Both host variants use the same agent-directory/session ownership protocol. Refuse a conflicting owner rather than killing it. Compatible instances may use different explicitly selected agent directories. Do not add a blanket systemd Conflicts rule that prevents legitimate separate installations.

Quiesce schedules/outbox before shutting down the embedded engine and modules. A restart request returns intent to the composition root; it restarts the entire combined service after cleanup. No library-level os.Exit, half-running controller or invisible resend of uncertain work.

## 4. Build inputs and commands

Introduce documented root build targets for assistant-only, full-host and the complete release candidate. Assistant-only needs Go and its dependencies, not a Svelte build. Full-host builds and verifies production web assets before Go embedding. Fail if the bundle is only a placeholder/.gitkeep or references missing runtime assets.

Use the pinned Go/Bun/frontend/dependency versions, `-trimpath`, explicit target architecture and CGO_ENABLED=0 when the dependency closure permits. Run race tests separately with CGO in a supported test environment. Baseline amd64 must not silently require a newer microarchitecture. Keep native binaries runnable on the documented minimum OS/libc profile or report an actual external runtime requirement.

Stamp both binaries with releaseId/full commit and build-target information. `--version` must work without Pi, a provider, configuration, network or workers; a machine-readable version form reports binary variant, source commit and schema/protocol versions separately. Doctor reports the separately discovered Pi/bridge/worker versions without conflating them with the build identity.

Record lockfile hashes, toolchain versions, Mewa release/assets, base-image digest, build flags and resolved OS/runtime package versions. A commit name does not make an image reproducible if apt/base inputs float. Pin a repository snapshot/resolution manifest for release inputs or materialize and retain the exact dependency closure before the first candidate build. Verify retries use that closure. Automatic scheduled package refresh must prepare a new reviewed dependency commit, not rebuild different bytes under the old tag.

Use the source commit timestamp for normalized archive metadata where practical; keep the workflow execution time only as provenance. Compile-time version stamping must not edit tracked source into a dirty release tree. Do not claim bit-for-bit reproducibility until a clean rebuild has actually demonstrated it.

## 5. Release manifest and integrity

`release.json` schema version 1 contains: releaseId; full sourceCommit; source repository; selected workflow/build-input identity; supported Pi/distribution/protocol matrix; Pixie schema and minimum-readable-schema versions; all four host artifact names, variants, architectures, byte sizes and SHA-256 values; GHCR repository, exact tag, index digest and per-platform digest; optional worker availability/dependencies; test evidence references and known limitations.

The manifest is the matching deployment record, not a mutable pointer to latest. Verify its releaseId recomputes from sourceCommit and matches the tag target, binary versions and OCI labels `org.opencontainers.image.revision` and `org.opencontainers.image.version`. Image revision labels contain the full source commit; version labels contain releaseId. Source URL and created metadata are truthful.

A short tag authenticates nothing by itself. Verify checksums and provenance before executing downloaded binaries. Installation examples pin release ID and, for unattended/container deployment, the manifest digest. No secrets, user paths, auth files, fixtures or fonts without verified redistribution rights enter assets.

## 6. Release pipeline

Implement a dedicated release-candidate workflow with an explicitly approved source commit. Normal PR/main CI produces disposable validation artifacts only. Scheduled jobs validate dependencies/security and may report an update candidate, but do not publish artifacts, move image aliases or create a release. New releases are assembled by the release workflow, not by independent assistant/npm/container publishers.

The order is mandatory:

1. Validate approval/input, resolve source commit and releaseId once, verify source reachability, fetch exact source and freeze build inputs. A manual run lacking publication authorization is validate/build-only.
2. Run source, contract, native Pi and migration tests. Build both host variants on both architectures and the two image platforms. Build on native architecture runners for release evidence; cross-compilation/QEMU alone is not a native runtime result.
3. Test the actual extracted archives outside the checkout. Test each built image with the matching assistant archive, and the full-host archive with no separate assistant binary/container running. Complete optional-dependency-absent and managed-parity fixtures.
4. Produce/validate the complete manifest candidate, checksums, SBOM/legal material and provenance subjects. Keep failed or missing matrix outputs from the publisher. Build artifacts are not yet a published GitHub Release.
5. After publication authorization, create or verify the exact Git tag and a draft GitHub Release. Push the validated image manifests/index under the exact immutable release tag; verify digests/labels. Upload all validated release assets and attestations to the draft, then verify the complete expected set again.
6. Publish the draft only after all checks pass. Prefer repository immutable releases after operator approval of that setting. Never mark the release available and then build missing variants asynchronously.

There is no distributed transaction across GHCR and GitHub Releases. An image may have been pushed when a later release upload fails; record that partial publication and keep the GitHub Release draft. Do not advertise or promote an incomplete deployment. Retry verifies and reuses exact matching digests/assets; it never overwrites published content or creates another identity for the same source. If matching artifacts cannot be recovered/reproduced, require a new source commit and approved release.

Concurrency is keyed by full source commit, with release publication not cancelled by a newer main push. Prevent two authorized workflows racing to mutate one tag/release. Existing final assets with wrong digest or tag target are a hard failure, not a `--clobber` operation. Re-running a fully published matching release validates and exits successfully without mutation.

Use least-privilege read-only validation jobs and separately permissioned publication jobs. Pin actions. Verify release/tag protection and provenance against actual repository settings rather than claiming the YAML alone provides them. Never run untrusted PR code with publication secrets/permissions.

Retire new npm publication after the Go cutover; keep old published packages immutable. If a legacy transition release is separately requested, first repair its stale package-path checks and invalid development-version dry run. It is not the new release mechanism.

## 7. systemd and startup

Install one unit per selected topology, as the Pi owner. Use absolute paths and a private config; no dependency on login-shell aliases. Only the assistant-only topology needs an operator-configured assistant bearer shared with Docker. The combined topology owns its private host credential internally. Public web authentication and module credentials remain distinct in both.

Assistant-only unit:

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

Full-host unit:

```ini
[Unit]
Description=Pixie
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=exec
ExecStart=%h/.local/bin/pixie serve --assistant=embedded --config %h/.config/pixie/pixie.json
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

The full-host unit does not require or start pixie-assistant.service. Requested service restart exits 75 after bounded cleanup; ordinary graceful stop exits 0. Verify a new PID/boot identity, no orphan descendants, repeated request idempotence and a shutdown grace shorter than TimeoutStopSec. Type=exec detects executable startup errors; it is not application readiness. Do not switch to Type=notify without implementing its full contract.

Invalid authentication/configuration or conflicting native ownership is a startup error. An unavailable Pi/provider is an explicit agent-unavailable state: full-host keeps the shell/diagnostics safely available without dispatch and standalone reports non-ready through its authenticated health surface. It does not spin in a restart loop, select another installation or silently start an external assistant. Distinguish liveness, controller readiness, agent readiness and optional module readiness.

Test supported target-systemd versions. Do not default to DynamicUser, ProtectHome=true or blanket write restrictions that break intentional host Pi tools. Validate optional NoNewPrivileges/hardening against real tools. User lingering is an explicit operator choice; do not change it automatically.

Docker does not inherit user-systemd descendant cleanup. Give its final entrypoint a working init/subreaper or equivalent verified behavior; the current Dockerfile's final ENTRYPOINT bypasses its earlier tini entrypoint. Test the final image's effective command, SIGTERM and detached Browser descendants, not only a build-stage file. Do not start native Pi in that image.

## 8. Installation, configuration and rollback

Publish a short setup for each archive. Standalone: select existing Pi/agent directory, configure private bearer and absolute paths, start pixie-assistant.service, run the matching digest-pinned Docker image in external mode with the same endpoint/bearer. Preserve admitted read-only project bind paths; explain missing mounts independently of chat.

Full-host: install pixie, select existing Pi/agent directory, configure Pixie data and remote web auth where needed, start pixie.service. No separate assistant, Docker, web asset directory or frontend runtime is required for core use. Browser and worker capabilities report their own missing dependencies.

Normalize old documented environment variables and new CLI/config at one startup boundary with precedence explicit CLI > config > supported environment > defaults. Do not maintain two independently mutable configurations. Configuration file paths are absolute; shell `~`, systemd `%h` and environment substitution are not implicitly evaluated inside JSON. Config/secret files are 0600 and their parent private. Never place tokens in argv, URLs, logs, examples or tool-visible instructions.

The two topologies share native Pi state, but Docker volume paths and host XDG paths need explicit mapping for Pixie metadata. Do not copy or relocate a user's source data during ordinary startup. [Migration and rollback](migration.md) governs schema backups, locks, mode switching and restoration; keeping an old binary alone is not a rollback plan after an incompatible schema change.

Uninstall removes the selected binary/unit/config only, with an explicit optional separate action for Pixie data. It never deletes native Pi sessions, auth, settings or extensions. A retained legacy provider/MCP configuration is not disposable cache.

## 9. Release acceptance

PKG-02/03 and Gate 5 require every matrix entry with evidence: exact source/name/label checks; checksums/attestation subjects; no conflicting tag reuse; no incomplete release; retry after partial upload; builds with missing placeholders/assets rejected; native architecture smoke; managed compatibility rows; both units/topologies; state upgrade/downgrade/mode switch; uninstall preserves native files; service/readiness and optional-dependency behavior; image external mode and descendant cleanup.

Measure full topologies, not just supervisor RSS: artifact/image sizes, startup, controller+assistant+Pi children, optional workers, idle/active/peak memory and repeated p50/p95 on recorded machines. Do not claim a Go speed/memory improvement from implementation language alone.

Core release readiness does not require Canvas/Openfig to be implemented. Once those stages land, their enabled/disabled/isolation tests and optional worker metadata join this same release manifest; both host builds and the Docker tag keep the same schema. Any additional optional worker artifact must use the same releaseId and source evidence, not a separate product-version stream.

## Sources

[User-supplied llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases); [GitHub immutable releases](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases); [Go build flags](https://pkg.go.dev/cmd/go); [Go modules](https://go.dev/ref/mod); [systemd.service](https://www.freedesktop.org/software/systemd/man/systemd.service.html); [systemd.kill](https://www.freedesktop.org/software/systemd/man/systemd.kill.html). Current workflow/packaging evidence is pinned in sources.md and second-pass-review.md. These are implementation requirements, not claims that the current workflows already enforce them.
