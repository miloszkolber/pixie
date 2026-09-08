# 07 — Two builds, systemd and release

Owner: G with B/A/F. Every release must provide both the assistant-only build for a Dockerized interface and the complete host application. This requirement applies to stable releases and prereleases. Build both from the same commit/version; passing one build is not release acceptance. Core remains usable without optional worker runtimes.

## Release matrix

| Variant | Executable | Contents | Intended deployment |
| --- | --- | --- | --- |
| Assistant only | pixie-assistant | Go assistant service and shared native-Pi supervisor; no frontend/controller assets | Assistant on the Pi host, Pixie interface/controller in Docker |
| Assistant + interface | pixie | Same assistant engine plus Go controller and embedded compiled web UI | Complete Pixie on the host in one process and one systemd user service |

Initial architectures are Linux amd64 and arm64. Each release therefore has these four archives:

```text
pixie-assistant_<version>_linux_amd64.tar.gz
pixie-assistant_<version>_linux_arm64.tar.gz
pixie_<version>_linux_amd64.tar.gz
pixie_<version>_linux_arm64.tar.gz
```

Each archive carries its binary, applicable unit/configuration example, license/notice and short install instructions. Publish one checksum manifest and release metadata linking all four to the same revision and version, plus provenance. An approved Docker image is a companion deployment artifact, not a substitute for either host build. Missing, failed or mismatched matrix entries block release publication.

“All-in-one” means assistant, controller and web assets in one executable, not two binaries in an archive or a script that installs another service. It still uses the separately installed Pi and whatever runtime that installation needs. Browser automation and optional Canvas/Design workers have explicit dependencies and isolation requirements; do not hide those in the promise of a single core binary or auto-install them on startup.

## Composition and code ownership

Keep assistant/go.mod and package/go.mod. Expose a narrow reusable assistant/host facade over the assistant internals. It owns construction, listener/readiness, cancellation and shutdown, not process-global os.Exit or CLI parsing. The standalone CLI and combined CLI compose this same engine. The application imports the public facade, never assistant/internal or its main package; the assistant never imports controller internals.

The repository build must resolve the assistant module from this exact checkout, for example through an explicit local replace directive in package/go.mod with the correct module requirement. A release must not accidentally link an older downloaded assistant. CI verifies build metadata and dependency resolution. No repository-wide module move or third runtime service is required. [Go module reference](sources.md#external-contracts).

Standalone mode exposes the existing authenticated loopback host protocol for the Docker controller. Combined mode starts that same engine within the pixie process and, initially, connects the controller over an authenticated private loopback listener with an ephemeral port and boot-scoped secret supplied internally. This reuses the tested wire path without a second daemon or protocol implementation. Do not expose this private endpoint through the public UI router, require a second operator-managed secret for it, or log the secret. A later in-process transport optimization must pass identical semantics/conformance tests.

The combined process starts the embedded engine before dispatch is available, quiesces schedules/outbox before shutdown, and coordinates controller, module and Pi-child cleanup. An explicit whole-service restart restarts the combined process, not a detached assistant process; the UI warns that active work can be interrupted. Do not silently retry uncertain scheduled or chat work afterward.

Keep an explicit external-assistant mode for the Docker entrypoint, for example `pixie serve --assistant=external`. It starts the controller/UI only and must not discover or spawn Pi inside the image. Direct-host mode is explicit/defaulted consistently, for example `pixie serve --assistant=embedded`. A failed embedded start must not silently attach to another daemon. If the same binary is used in Docker, its unused embedded engine has no startup side effects.

## Build contract

Use the pinned Go toolchain, -trimpath, version/revision metadata and CGO_ENABLED=0 where dependencies permit. Run race tests separately in a suitable CGO-enabled test environment. Add root commands for each variant and one release command that builds the complete matrix.

Assistant-only builds need Go and its dependencies, not Svelte assets or a frontend build. Combined builds compile and verify the frontend first, then embed its actual production assets into the binary. Fail if only the current .gitkeep/placeholder bundle exists. Do not require a checkout, web asset directory, Node, Bun or a development web server to run the resulting combined core application.

Test final archives outside the checkout. An npm-installed Pi may still need Node; a standalone Pi fixture verifies neither released binary introduces an independent Node/Bun dependency. Include assets/legal notices for what each build actually ships. Keep the small assistant-only dependency closure free of controller, renderer and UI packages.

## systemd user services

Run the selected deployment as the Pi owner, with absolute executable paths and private configuration. Do not depend on login-shell initialization. Supply two unit templates, but install/enable only the one appropriate to the chosen topology for a given agent directory.

Assistant-only unit, used with Docker:

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

Combined unit, without Docker or a separately installed assistant:

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

The combined unit must not Require or start pixie-assistant.service. An application-level agent-directory lock prevents conflicting writers; units can serve different explicitly configured directories, so do not indiscriminately kill another instance. Document switching modes by stopping the old owner before starting the new one.

Reserve 75 for an explicitly requested restart; ordinary graceful stop exits successfully. Test that cleanup completes within TimeoutStopSec, repeated restart is idempotent, and the actual executable gets a new PID/boot identity. Type=exec is startup-error detection, not application readiness. Use authenticated readiness or implement the complete notify contract before switching to Type=notify. KillMode=mixed gives the main process graceful coordination with final service-wide descendant cleanup. [Service references](sources.md#external-contracts).

Do not use DynamicUser, ProtectHome=true or blanket project write restrictions as default host-Pi policy. Validate optional NoNewPrivileges/hardening against required native tools. Explain optional user lingering without changing operator policy automatically. Read-only file browsing in the combined host process is application policy, not a read-only mount protecting every native Pi tool or compromised same-UID worker.

## Installation and configuration

Docker path: install pixie-assistant on the existing Pi host; configure executable/directory/private assistant bearer; start its user unit; run the Docker interface/controller in external mode with matching endpoint/bearer. Preserve admitted project bind paths for read-only Files/Git. The controller initiates the assistant connection; do not reverse that protocol merely to match the phrase “connecting with Docker.”

Direct-host path: install pixie only; select the existing Pi; configure Pixie state and any remote-UI authentication; start pixie.service. The compiled UI is served directly. There is no second assistant installation/unit, no required Docker container and no frontend runtime. Surface optional Browser/worker availability locally.

Both paths use the same native Pi state and compatible Pixie metadata, defaults, events and capabilities. Keep credentials private and secrets out of URLs/logs. Combined mode does not inherit Docker filesystem/network restrictions; document and test its actual boundaries. Host UI remote exposure still requires the same explicit authenticated/origin-controlled configuration.

## CI and publication

Required matrix: 2 variants × 2 architectures, plus the Docker external-mode integration. Test archive contents/version, independent Pi installation, native session continuity and install/start/stop/upgrade/rollback/remove for every supported entry. The all-in-one smoke must work with the standalone assistant binary absent and Docker stopped. The assistant-only smoke must work without UI assets and serve an actual Docker controller. Verify no Pi child starts in external-mode Docker.

Add contract-equivalence scenarios for both compositions: prompt acceptance/settlement, pending dialog, image, clone/fork, reconnect, queue uncertainty and restart. Verify graceful combined shutdown and no orphan native children. Check embedded UI routes/assets, both light/dark shell fixtures and readiness without optional dependencies.

Keep PR/main validation and disposable artifacts separate from approved publication. A publication names exact revision, common version/tag, destinations and workflow. Stage/check all artifacts before making a release available; a failed variant does not yield a partial release. Use least privilege, pinned actions, checksums/provenance and immutable versions. Manual runs without approved identity are validation/packaging only, never fallback 0.0.0-dev publication.

Current container publication can run on main/schedule and promote latest. Change that policy deliberately rather than assuming this plan enforces it. An approved release image should record compatibility with the matching assistant archive. Test any supported adjacent-version upgrade pair; do not assume indefinite cross-version support.

Legacy npm support remains only for a tested transition. Reconcile its stale pack checks and contradictory development-version dry run if another npm release is actually needed. Retire old packaging/dependencies/patches only after retained features and rollback pass. This roadmap commit does not alter or dispatch release workflows.

## Migration and rollback

Convert only Pixie metadata, never native transcripts, credentials, settings or extension installations. Back up metadata before versioned repeatable conversion and declare old-reader compatibility. Keeping the old binary alone is insufficient after an incompatible schema change.

Test upgrade/rollback within each topology and switching Docker+assistant ↔ combined host. Quiesce schedules/outbox and stop active work before ownership changes. Do not start both against the same native session/agent-directory lock. Preserve native identity, archive/project associations, layout preferences and module data; copy/relocate application state only with explicit path mapping and authorization. Do not assume host XDG state and Docker volume paths are identical.

Test missing executable/interpreter, symlinks, spaces, custom HOME/PATH/agent directory, no provider, absent extensions, network loss, doctor side effects, service stop/restart, partial migration and removal. Uninstall leaves native Pi data intact and only removes optional Pixie data on explicit request.

## Carry-forward work and done

Verify optional subagents from a real independent Pi installation. Reassess Bun child-launch and SDK llama export patches against that executable, preserving necessary legacy cases until proven obsolete. Upstream submissions require separate approval. Reopen/whole-service restart applies configuration; non-disruptive per-session reload is not a new prerequisite.

Measure each variant and its full topology: assistant plus Docker controller or combined process, with Pi children and optional workers included. Record artifact/image size, startup, idle/active RSS, repeated p50/p95 and machine/runtime versions on amd64/arm64. Build success is not a latency measurement.

K5 requires both variants on both architectures, independent vanilla/custom Pi, both service/install paths, mode-switch migration and rollback, embedded UI evidence and truthful security/docs. Release publication remains separate. Continue locally to [Canvas](08-canvas.md) and then [Design](09-openfig.md) after K5. Both variants remain mandatory when those modules are added; module-specific dependencies cannot make the assistant-only archive depend on the interface.
