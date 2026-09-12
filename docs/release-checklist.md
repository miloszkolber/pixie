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

Its failure output separates static violations from missing live inputs. A green static report is not live evidence: both final binaries on both architectures, systemd lifecycle, Docker platform runs, downloaded payload verification, collision/retry behavior and provenance attestations still require real candidate artifacts.

## Current repository evidence

From `package/`, `bun scripts/check-release-identity.ts`, `bun scripts/check-package-artifacts.ts` and `bun scripts/release-gate.ts` are static checks. The commit release workflow is present and validate-only paths are guarded, while this checkout still lacks a published tag/release, complete archive/binary evidence, Docker labels/digests, provenance, live package runs and explicit publication authorization. Those missing inputs are intentionally not represented as completed release evidence.

The canonical status, release gaps, and sequencing are in the [roadmap](../roadmap/README.md#current-verdict) and its [next steps](../roadmap/README.md#dependency-ordered-next-steps).
