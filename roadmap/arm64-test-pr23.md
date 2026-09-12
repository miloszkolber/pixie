# PR #23 arm64 test record

Test record for [PR #23](https://github.com/miloszkolber/pixie/pull/23), “Go engine execution, settlement, recovery and full-host lifecycle repair”.

## Test identity

- Test date: 2026-09-13
- Tested source commit: `e940743b0942dbe1d384c36a9e3f5fa1c4832f0f`
- Merge commit: `1308be12038e07e88b7d584386059aeb093412f4`
- Host: Apple Silicon macOS 27.0, `arm64`
- Host tools: Bun 1.4.0, Go 1.27.1 (`darwin/arm64`)
- Linux test runtime: native Apple Container Linux VM, `aarch64`, Go 1.27.1 (`linux/arm64`)

The Linux arm64 checks ran without CPU emulation. The test checkout was clean and isolated from the operator workspace.

## Passing checks

### Host-side frontend and assistant

- `bun install --frozen-lockfile`: passed.
- `bun run build:web`: passed, including Mewa validation, Svelte checks, bundle-size checks, and artifact checks.
- `bun test tests/pixie-assistant`: **206 passed, 0 failed**, 1,238 assertions across 40 files.

The first suite attempt contained one five-second startup-validation timeout. The test passed in isolation and on the complete rerun; it is recorded as a transient host-run flake, not a reproducible failure.

### Linux arm64 Go and native-Pi checks

- Workspace tests with `CGO_ENABLED=0`: passed.
- Assistant module tests with `CGO_ENABLED=0`: passed.
- `TestApplicationThroughNativePiHost`: passed for the vanilla, optional, and project profiles.
- Full Go race suite: passed with `CGO_ENABLED=1 GOMAXPROCS=2 go test -race -p 1 -count=1 ./...`.

The default-parallel race run was killed while building `tests/go/design` because the disposable Linux VM has 1 GB of memory. The serialized run passed all packages; this was resource pressure, not a test or race failure.

### Arm64 release artifacts and runtime

- The release staging build produced the Linux amd64 and arm64 assistant and host archives.
- Checksums matched the generated release manifest.
- The arm64 archives contained the expected executable, metadata, license, and service files.
- The packaged arm64 assistant and full-host binaries launched in the Linux arm64 runtime.
- With a deterministic native-Pi fixture, the packaged full host returned successful `/readyz`, `/health`, and `/livez` responses and shut down cleanly.

## Checks not available on macOS

The Go tests do not compile directly as macOS arm64 because the source uses Linux-only `unix.O_PATH`. The authoritative Go and native-Pi test execution therefore used the Linux arm64 VM above.

No arm64 systemd archive install, upgrade, rollback, uninstall, or service-supervision test was run. The full-host runtime check was a direct packaged-binary run, not a systemd lifecycle proof.

## Open image-build gate

The submitted image command was attempted directly:

```text
container build --platform linux/arm64 --file package/Dockerfile --target pixie .
```

Apple Container 1.3.1 failed during the web-build dependency install:

```text
Couldn't find patch file: 'patches/@mjakl%2Fpi-subagent@3.0.1.patch'
```

Two minimal build-context probes identified host-builder behavior behind the failure:

- The percent-encoded patch filename arrives inside the image as the nested path `@mjakl/pi-subagent@3.0.1.patch`, while Bun expects the literal `%2F` filename.
- With the submitted `.dockerignore`, the transferred `package/webui` context was empty on this builder.

This leaves the OCI image gate open. The result is a limitation of the Apple Container build path unless reproduced with Linux BuildKit; it is not evidence that the arm64 runtime binary failed. The image should still be verified with Linux BuildKit or a GitHub arm64 runner before the image gate is marked green.

## Conclusion

Arm64 source, test, release-archive, and packaged full-host runtime evidence is green. The remaining arm64 evidence gaps are the OCI image build and fresh-artifact systemd lifecycle. No final release, registry image, SBOM, or provenance claim follows from this local test record.
