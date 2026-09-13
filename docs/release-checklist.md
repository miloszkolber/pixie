# Release checklist

This is the validate-only checklist for REL-01–REL-04 and PKG-01–PKG-03. It describes the evidence a commit-named release must provide; it does not publish a release or enable a workflow.

## Source identity

1. Resolve the selected revision with `git rev-parse --verify 'HEAD^{commit}'` and validate the complete 40-character lowercase SHA.
2. Derive exactly `sha-<first 12 lowercase hex characters>` once. Use that value unchanged for the Git tag, GitHub Release title/tag, archive names, binary version metadata and Docker tag.
3. Keep the complete source SHA in binary metadata, OCI revision labels and `release-manifest.json`. Short names are display identity only; they are never authoritative.
4. Reject semantic-version, timestamp, hash-ordering and workflow-counter fallbacks. A short-ID collision with a different full SHA is a hard failure and must not retarget an existing release.

## Required staged evidence

Before promotion, stage all four archives from the same source commit:

```text
pixie-assistant-sha-<12>-linux-amd64.tar.gz
pixie-assistant-sha-<12>-linux-arm64.tar.gz
pixie-sha-<12>-linux-amd64.tar.gz
pixie-sha-<12>-linux-arm64.tar.gz
```

Each archive records its SHA-256, variant, architecture, full source SHA and contained binary name (`pixie-assistant` or `pixie`). The staged release manifest records all archive hashes, the full source SHA, a clean-tree result and checksums. Publication adds SBOM/provenance and Docker image index/platform digests only from verified image evidence; staging never claims attestations that have not been produced.

The Docker reference is `ghcr.io/miloszkolber/pixie:sha-<12>`. Verify one multi-architecture index plus runnable `linux/amd64` and `linux/arm64` manifests; attestation descriptors do not replace platform evidence.

## Live evidence production

From `package/`, the evidence job runs the committed producer before the bundle collector:

```sh
bun run scripts/produce-evidence-inputs.ts \
  --artifacts <staged-archives> \
  --output <evidence-dir> \
  --source-commit <40-hex> \
  --release-id sha-<12> \
  --reduction-manifest package/contracts/reductions.json \
  [--base-url <running-host-origin>]
```

It extracts the native-architecture staged binaries, executes only the checks it can honestly complete (`--version`, `doctor` when supported, and a readiness GET when `--base-url` is supplied), and marks those `actual: true, live: true`. A skipped, unsupported or failed check stays absent or blocked; it never becomes a pass. It then measures at least five fresh-process startup samples per native target with p50/p95 and peak process RSS and writes `coverage.json` and `performance.json`.

`package/contracts/reductions.json` records the operator-approved reductions that make the live matrix and the staged-artifact gates runnable. The coverage input reduces the mandatory FC rows the producer does not execute (FC01 is executed), the 14 X rows and Gates 1–5. The performance input reduces only the legacy worker/decoded-buffer fields; the workflow measures both the amd64 targets and the native arm64 targets on `ubuntu-24.04-arm`, then merges them before collection. `packageArtifacts` reduces only the staged-binary live-execution and full-host rows that need a running host: non-native arm64 `--version`/`doctor`, every `readiness`, `lifecycle` and `uninstall` row, host `embedded-UI` rows, and the full-host native-engine facade. `releaseGate` reduces only publication inputs: Git tag and GitHub Release state, the published `ghcr.io` tag, registry provenance/SBOM, registry image and platform digests, the staged manifest's publication-only fields, and latest-promotion ancestry. Every structural archive/systemd/config/static command check, the static workflow publication-policy checks, source reachability from main, archive/binary identity, archive checksum consistency and the controller-image evidence mapping stay mandatory. A reduced row is not evidence that the behavior passes: `check-package-artifacts` and `release-gate` report it as reduced, never as passing, and an unreduced row without evidence still fails the gate.

The `evidence` job runs `check-package-artifacts --evidence` and `release-gate --evidence`; both load the committed manifest (with `--reductions` or `PIXIE_REDUCTIONS_MANIFEST` as an override) and default to the checked-in file. `release-gate` also reads the staged local `release-manifest.json` the collector embeds, so release ID, source commit, clean-tree, archive hashes and checksums stay mandatory. `automaticMainAuthorized` and `sourceReachableFromMain` default to false and must be explicitly enabled (`PIXIE_AUTOMATIC_MAIN_AUTHORIZED`, `PIXIE_SOURCE_REACHABLE_FROM_MAIN`) by the release job after it verifies them.

The amd64 and native arm64 startup measurements and the native `--version`/`doctor` probes are real candidate-binary measurements. The reduced rows above remain unproduced: arm64 platform digests, non-native arm64 `--version`/`doctor`, per-feature live coverage, packaged-binary lifecycle/uninstall/embedded-UI and full-host facade checks, Git tag/GitHub Release state, the published OCI tag and digests, registry provenance/SBOM and latest-promotion ancestry. The `linux/arm64` OCI image build and run remain unproven.

## Architecture evidence status

The host campaign exercises `linux/amd64`; a Mac arm64 pass is recorded in [roadmap/arm64-test-pr23.md](../roadmap/arm64-test-pr23.md). That pass covers the arm64 Go tests, native-Pi host profiles, serialized race suite, both release archives with matching checksums, and the packaged full-host readiness on a Linux arm64 VM. Still open and required for a real release: the `linux/arm64` OCI image build and run (the Apple Container probe failed on the percent-encoded patch filename and an empty `package/webui` context, so verify with Linux BuildKit or a GitHub arm64 runner), a fresh-artifact systemd lifecycle, and the registry digests/SBOM/provenance. The release workflow now measures native arm64 performance on `ubuntu-24.04-arm`. No amd64 result substitutes for arm64 evidence, and the static gates continue to fail closed on the missing arm64 inputs.

## Validation-only paths

Pull requests, scheduled checks and manual validation paths may derive and test candidate identities, but they must not publish tags, releases, archives or images. Only the explicitly authorized release path for a verified main-commit candidate may promote a complete set.

Documentation-only changes follow the same validate-only path and never create a binary release. Automatic publication is safe only when the repository policy explicitly authorizes it, the candidate is a full SHA reachable from protected `main`, and the publish job is guarded to a main push. A release-tag push must not start another publication loop.

## Collision and retry handling

An existing tag or release with the same `sha-<12>` is reusable only when its recorded full source SHA matches exactly. A different full SHA is a collision and stops publication.

If publication stops after a partial upload, retain the staged payload and immutable identity, do not move `latest`, and retry with the same full source SHA, release ID, artifact hashes and image digests. Existing assets are downloaded and compared by SHA-256: identical payloads are no-ops, while a mismatch stops the retry without overwriting the asset.

The static gate combines identity, package and policy checks:

```sh
bun scripts/release-gate.ts
```

Its failure output separates static violations, missing live inputs and reduced live inputs. A green report is not live evidence: both final binaries on both architectures, systemd lifecycle, Docker platform runs, downloaded payload verification, collision/retry behavior and provenance attestations still require real candidate artifacts; the reduced rows in `package/contracts/reductions.json` are documented gaps, not results.

## Current repository evidence

From `package/`, `bun scripts/check-release-identity.ts`, `bun scripts/check-package-artifacts.ts` and `bun scripts/release-gate.ts` are static checks. The commit release workflow is present and validate-only paths are guarded. With `--evidence`, `check-package-artifacts` and `release-gate` consume the collector's staged bundle and the committed reductions file; they still need the exact-commit archives, the local controller image tar and the executed native binary probes. This checkout still lacks a published tag/release, the published OCI tag/digests, registry provenance/SBOM, non-native arm64 `--version`/`doctor` execution and explicit publication authorization. Those missing inputs are reported as reduced or missing and are never represented as completed release evidence.

Browser MCP registration is implemented at source: the controller writes one setting into Pi's MCP configuration and reports a bounded probe, and the WebUI exposes the setting under Settings → Browser. Pixie hosts no browser. Compose ships an optional `pixie-browser` service that is not a `pixie` dependency, is not part of the Pixie archive or image, and is not required for controller release. Host verification of registration, probe and that Compose service is recorded in the roadmap and is not release evidence.

The canonical status, release gaps, and sequencing are in the [roadmap](../roadmap/README.md#current-verdict) and its [next steps](../roadmap/README.md#dependency-ordered-next-steps).
