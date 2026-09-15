# Release checklist

This is a validate-only checklist. It describes evidence required before a separately authorized release; it does not publish a release, an image, a tag, or enable a workflow.

## Source identity

1. Resolve the selected revision with `git rev-parse --verify 'HEAD^{commit}'` and validate its complete 40-character lowercase SHA.
2. Derive `sha-<first 12 lowercase hex characters>` once and retain the complete SHA in the staged manifest.
3. Reject semantic-version, timestamp, hash-ordering, and workflow-counter fallbacks.
4. Stop on a short-ID collision with a different full SHA.

## Public archive candidates

Stage exactly six public Linux archive candidates from one source commit:

```text
pixie_web-sha-<12>-linux-amd64.tar.gz
pixie_web-sha-<12>-linux-arm64.tar.gz
pixie_cli-sha-<12>-linux-amd64.tar.gz
pixie_cli-sha-<12>-linux-arm64.tar.gz
pixie-sha-<12>-linux-amd64.tar.gz
pixie-sha-<12>-linux-arm64.tar.gz
```

Each archive records its SHA-256, product, architecture, full source SHA, and literal public entrypoint. `pixie_web` exposes `pixie_web`; `pixie_cli` exposes `pixie_cli` and the bundled native `pixie`; full `pixie` exposes the bundled native `pixie`. The latter two contain their internal bundled JavaScript host `libexec/pixie_assistant.js`; only full `pixie` contains `libexec/pixie_full` and `libexec/pixie_web`. `pixie_assistant` is never a public archive or entrypoint.

The Pi-bearing archives bundle the pinned Bun `1.4.0` runtime (`runtime/bin/bun`) and Pi `0.85.1`, bundle no Node runtime, retain the normal Pi TUI, exclude Pi RPC, and block Pi self-update. The staged manifest records archive hashes, the complete source SHA, clean-tree result, and checksums. It must not claim SBOM, provenance, image digest, published release, or published image evidence that has not been produced.

## Validation evidence

Run static package and release checks against the exact candidate artifacts:

```sh
bun scripts/check-release-identity.ts
bun scripts/check-package-artifacts.ts
bun scripts/release-gate.ts
```

These commands are static gates unless they are supplied separately captured evidence. Archive-layout checks are not proof of a credentialed Pi session, an interactive TUI/extension PTY, arm64 lifecycle, systemd behavior, Docker behavior, update/rollback, or remote publication.

Required unproven evidence includes live amd64 and arm64 archive behavior, full Docker Compose behavior, real systemd installation/start/stop/restart and upgrade/rollback, real Pi provider credentials, and the declared image/platform/provenance artifacts if publication is later authorized. Canvas containment, Openfig's licensed parser and worker, and the native resource-attachment API remain separate feature blockers.

## Publication boundary

This repository has no published release or approved published Docker image. A placeholder image reference or a Compose image field is not publication evidence. Remote publication, tags, releases, images, and deployment require separate authorization.
