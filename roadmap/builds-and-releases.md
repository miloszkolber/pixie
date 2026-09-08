# Builds, services and releases

Every release must include both variants below for every supported architecture. This is a product requirement, not an optional packaging follow-up. Track BUILD and PKG tasks in [execution.md](execution.md).

## 1. Required build variants

| Variant | Release executable | Purpose | Runtime composition |
| --- | --- | --- | --- |
| Assistant only | `pixie-assistant` | Host companion for a Docker Pixie instance | Go assistant on the host; controller, web UI and application modules in Docker |
| Assistant + Pixie interface | `pixie` | Complete workspace directly on the host | One Go binary and one service containing assistant runtime, controller, embedded web UI and module registry |

Both use the same selected host Pi executable, native state, assistant implementation and protocol contracts. Neither embeds a replacement Pi SDK or installs Pi silently. Pi still needs whatever runtime its own installation requires.

The full-host build is not an archive containing two independently managed executables. It must start without a separate `pixie-assistant` installation, Docker, a checkout, web assets beside the executable, or a frontend toolchain. The assistant-only build must not depend on UI assets, Chromium, Design parsing, the controller or a scheduler.

Browser/Canvas rendering and Design parsing can require optional isolated workers and their documented dependencies. Missing optional dependencies must not prevent basic chat, configuration diagnostics or workspace navigation in either deployment. Do not conceal a required assistant process or runtime in an installer to call the full build self-contained.

Initial supported targets: Linux amd64 and arm64. Each release therefore has four required binary archives: two variants multiplied by two architectures. Additional operating systems need their own tested service and packaging policy; cross-compilation alone does not establish support.

## 2. Process composition

### Assistant-only + Docker

```text
Host: pixie-assistant.service -> pixie-assistant -> selected pi processes
                          ^
                          | authenticated loopback /pi
Docker: Pixie controller + embedded web UI + optional module registry
```

The host service accepts the existing loopback-oriented controller transport. The Docker instance connects to it; “connecting with Docker” does not mean an agent process inside Docker owns the user's Pi. Preserve the deliberate Linux host-network deployment until another network arrangement has its own connectivity/security tests.

Pair the Docker controller with the assistant through a private shared secret. Never publish the host assistant port externally. Preserve read-only admitted project mounts at the same absolute paths, and keep controller/module data in its declared application mount. Adding a session does not automatically mount its cwd.

### Full host

```text
Host: pixie.service -> pixie
                      + assistant runtime -> selected pi processes
                      + controller + scheduler + embedded UI
                      + optional module registry -> isolated workers
```

The full binary starts one shared assistant runtime in-process, not a second executable. Reuse the same controller-facing host transport through an internal-only loopback listener initially. Use a private ephemeral endpoint/credential owned by the composition root; do not expose it through the public UI listener or log it. It is not a security boundary against the same OS user, but it preserves the tested adapter contract without duplicating methods.

Use the same controller services for schedules, workspace metadata and modules in both modes. Do not create an alternate host-only queue, persistence layer or HTTP API. A later in-process transport optimization is optional and must pass the same conformance fixtures before replacing loopback adaptation.

`pixie` defaults to full-host mode. Provide an explicit controller-only mode for the Docker image, such as `pixie serve --mode controller`; the image entrypoint must select it. Controller-only mode never starts, discovers or takes ownership of Pi locally, including during degraded host connectivity. Full-host mode never silently switches to an already-running external assistant. Invalid/mixed configuration fails clearly.

## 3. Shared Go source boundary

Keep the existing application module under `package/` and the assistant module under `assistant/`. A narrow public facade under `assistant/host/` exposes configuration, startup, authenticated host transport, readiness and shutdown. It delegates to `assistant/internal/*` and contains no controller dependencies.

```text
assistant/cmd/pixie-assistant -> assistant/host -> assistant/internal/*
package/cmd/pixie            -> assistant/host + package/internal/controller
```

The application cannot import another module's `internal` packages. Do not copy supervisor/RPC code or create a second implementation to bypass that rule. Use a local Go module replacement to `../assistant` when building from the repository; the release build uses both modules from the exact same checkout. Do not resolve a moving remote assistant version. A root Go workspace is not required merely for this composition.

Keep schema definitions in `package/contracts` and generated bindings in each consumer. The assistant build must not need the frontend build to compile or generate its essential contracts. Build the web assets before compiling the full variant and fail packaging if only an embed placeholder exists.

Refactor the current application main into explicit composition/startup hooks only as needed. Shared libraries return errors and lifecycle signals; only executable entrypoints decide exit codes. In-process assistant code must never call `os.Exit` and bypass controller shutdown.

## 4. CLI, configuration and service lifetime

Proposed command surface, implemented and documented together:

```text
pixie-assistant serve --config /absolute/path/assistant.json
pixie-assistant doctor --config /absolute/path/assistant.json
pixie-assistant --version

pixie serve --config /absolute/path/pixie.json
pixie serve --mode controller --config /absolute/path/pixie.json
pixie doctor --config /absolute/path/pixie.json
pixie --version
```

The full configuration includes the same assistant settings under one explicit section. Preserve existing environment variables through a bounded documented compatibility path. Configuration precedence must be identical where settings are shared. Do not overwrite native Pi settings or auto-install optional extensions during startup.

Default units are systemd user services under the Pi owner. Run `pixie-assistant.service` for Docker mode or `pixie.service` for full-host mode, not both for the same Pi ownership scope. The full unit does not `Require` a separate assistant unit. Do not silently stop an unrelated service; detect conflicting ownership and provide an explicit mode-switch procedure.

Both use the same stable assistant installation identity, lock location and native session ownership rules. A canonical agent-directory lock prevents the two variants from owning the same managed sessions. It cannot coordinate an unrelated vanilla TUI; separate-session and idle-handoff rules still apply.

Share one tested service policy: absolute executable/config paths; private files; stdout/stderr to the journal; bounded shutdown; restart delay/start limit; exit 75 for requested restart with matching systemd policy. See the unit example in [assistant-go.md](assistant-go.md#9-systemd-and-artifact-delivery). `Type=exec` is not application readiness.

In full-host mode, an assistant restart request must ask the composition root to drain scheduling/admission, close or cancel module work, perform bounded assistant shutdown, and restart the complete service. Do not leave a half-dead controller or orphan Pi children. A browser disconnect does not trigger service restart. Optional worker failure does not terminate the full binary.

Readiness reports component state separately: controller/state/UI, assistant transport, Pi compatibility/provider availability, and optional modules. Distinguish service liveness from ability to submit a model turn; missing provider configuration must still allow setup/diagnostics.

## 5. Artifact and release contract

Use one release version and source commit for both variants. Proposed names:

```text
pixie-assistant-<version>-linux-amd64.tar.gz
pixie-assistant-<version>-linux-arm64.tar.gz
pixie-<version>-linux-amd64.tar.gz
pixie-<version>-linux-arm64.tar.gz
checksums.txt
release-manifest.json
```

Each archive contains its executable, matching user unit, non-secret configuration example, concise installation instructions and applicable license notices. The full archive embeds the web UI and does not require a second assistant executable. The assistant archive contains no UI bundle or optional renderer/parser runtimes.

The release manifest records version, source revision, variant, OS/architecture, artifact checksums, assistant protocol version, supported/tested Pi range, UI asset revision where applicable, and the matching Docker image digest/compatibility information when that image is published. SBOM/provenance cover both executables and optional separately distributed assets. Do not bundle font files without independently established redistribution rights.

Treat the four binary archives as one required release set. Build and test all of them before publishing a final release. A failed variant/architecture job prevents final publication and promotion; do not publish an assistant-only partial release because the full build failed. Use a draft/staging step where the hosting platform lacks atomic multi-asset publication, and promote only after completeness/checksum verification.

Docker remains a supported distribution, not a replacement for either binary build. Its controller mode must be compatible with the released assistant and point to immutable image identity when used for acceptance. Maintain native Pi compatibility separately from Pixie-to-assistant protocol compatibility.

Keep PR/main/scheduled validation free of publication unless explicitly approved. An approved release uses exact revision, version, destinations and workflow; reject fallback development versions. Published releases remain immutable. Legacy npm releases are not retroactively rebuilt; the two-build contract applies to releases produced by the new release pipeline.

## 6. Build and verification matrix

Provide explicit repository targets `build:assistant`, `build:host` and a release target that invokes both. Name/build tooling may be adapted to current scripts, but the modes must remain unambiguous. Build the assistant independently of Svelte/Mewa assets. The full build embeds freshly verified web assets from the same source revision.

Use pinned toolchains, dependency lockfiles, reproducible source inputs, version/revision stamping and clean output directories. Build `CGO_ENABLED=0` where selected dependencies permit; run race tests separately with their required environment. Verify the real final archives, not only `go run` or workspace fixtures.

| Test | Assistant-only + Docker | Full host |
| --- | --- | --- |
| Fresh vanilla Pi, optional native packages absent | Required | Required |
| Customized Pi HOME/PATH/agent directory, symlinked executable | Required | Required |
| Core prompt/images/model/abort/reopen/clone/fork | Required | Required |
| Mid-run/tool/dialog reconnect and uncertain delivery | Required | Required |
| Service startup/stop/requested restart, descendants cleaned | Required | Required; whole composition |
| No assistant Node/Bun/toolchain at runtime | Required | Required for core workspace |
| No separate UI assets, assistant executable or Docker | Not the deployment model | Required |
| Docker entrypoint never launches local Pi | Required | Controller mode regression |
| Optional Browser/Canvas/Design unavailable | Core remains functional | Core remains functional |
| Wrong mode, conflicting lock, port in use, invalid secret | Clear failure | Clear failure |
| Both variants report matching release/source identity | Required | Required |
| Clean amd64 and arm64 artifact installation | Required | Required |

Tests that exercise the full UI use the combined artifact's embedded assets. Verify the Docker-facing deployment with the real controller image and assistant artifact, not only a mocked client. Measure total memory/latency with Pi children and optional workers, separately by variant.

## 7. Upgrade, rollback and mode switching

Both variants preserve native Pi state. Pixie metadata has one schema/owner regardless of process composition. Do not relocate Pi's agent directory or duplicate transcripts to switch modes.

Before upgrade or Docker/full-host switch: stop new schedule dispatch, settle or explicitly interrupt active work, preserve uncertain-delivery claims, back up application metadata/private configuration, and release the old runtime lock. Configure the new mode's explicit application data path and permissions. Validate project roots: a container bind path existing does not prove the corresponding host path is admitted, and vice versa.

Start the selected mode, verify identity/capabilities/metadata and reopen sessions without resending accepted prompts. Keep prior artifacts and configuration for rollback. A schema migration must declare rollback compatibility or a tested restore procedure; swapping binaries alone is not a guarantee.

Removal stops/disables only the selected Pixie service and removes its artifacts. Native Pi installation, credentials and transcripts remain untouched. Canvas/Design source deletion is separate and explicit; cleanup must not interpret uninstall as consent to erase native or user-authored documents.

## Completion

This plan is complete only when every supported release contains both tested variants, full-host mode is a single binary/service, Docker mode does not launch a second agent, shared source prevents implementation drift, and the install/upgrade/rollback/mode-switch matrix passes. Artifact preparation can complete while remote publication awaits its separate approval.
