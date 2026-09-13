# Development

Use the pinned Bun and Go versions in `package.json` and `package/go.mod`. Go filesystem checks require Linux; use disposable containers on macOS.

The `pixie/` directory holds the Bun workspace, lockfile and shared build/test tooling. The Go assistant module and the opt-in Bun administration bridge live in `assistant/`; application source and tests live in `package/`.

From `pixie/`:

```sh
bun install --frozen-lockfile
bun run check:deps
bun run check:docs
bun run check:contracts
bun run check:coverage
bun run lint
bun run typecheck
bun run test
bun run build
```

`bun run test` runs the WebUI/contract suites plus the Go suite. To run the Go suite directly, use `go test -race -count=1 ./...` and `go vet ./...` from `package/`.

Go tests use temporary native state and authenticated WebSockets. They cover the assistant facade and application persistence, queues, schedules, nullable project grouping, reserved routing, project ownership and browser-MCP registration. The `assistant/bridge` Bun suite uses temporary package fixtures and can smoke-test a real installed Pi SDK when present. No real provider credentials are required.

Container and acceptance checks, also from `pixie/`:

```sh
sh package/tests/deployment/compose.test.sh
docker build -f package/Dockerfile --target ui-acceptance -t pixie-ui-acceptance .
docker run --rm --network none --shm-size 256m pixie-ui-acceptance
docker build -f package/Dockerfile --target pixie -t pixie .
```

Mount `/artifacts` to retain the acceptance run's generated artifacts. The container-image workflow runs one shared validation graph before publishing the image. Acceptance covers short viewport composer access, file/Git views, attachments, streaming/reconnect, provider setup, keyboard focus and both themes at narrow and wide sizes. Apple Container validates Linux processes and images; it does not establish Docker Compose host-network behavior.

`bun run dev:web` uses the same frontend entry and Linux Go fixture. Builds verify vendored Mewa assets and enforce the initial JavaScript budget. Keep WebUI and contract tests under `package/tests/`; new Go unit tests may be colocated; use regression cases for observable behavior and realistic failure modes.

`bun run --cwd package scripts/check-performance.ts` validates an operator-supplied `performance-evidence.json` record. It intentionally fails when that live artifact is absent; static fixtures and cross-compilation do not establish PERF-01 on either architecture.
