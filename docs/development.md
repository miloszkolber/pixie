# Development

Use the pinned Bun and Go versions in the root `package.json` and the per-module `go.mod` files. Go filesystem checks require Linux; use disposable containers on macOS.

The repository root holds the Bun workspace, lockfile and shared build/test tooling. Source roots are `assistant/`, `web/` and `shared/`. The interim Go assistant and the opt-in administration bridge live in `assistant/`; the controller, UI and application tests live in `web/`; the protocol schema and generated catalogs live in `shared/`.

From the repository root:

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

`bun run test` runs the WebUI and contract suites from `web/tests` and `shared/tests` plus the Go suite. To run a Go module directly, use `go test -race -count=1 ./...` and `go vet ./...` from `web/`, `assistant/` or `shared/`. The root `go.work` links the three modules; set `GOWORK=off` to build one module with only its own `replace` directives.

Go tests use temporary native state and authenticated WebSockets. They cover the assistant facade and application persistence, queues, schedules, nullable project grouping, reserved routing, project ownership and browser-MCP registration. The `assistant/bridge` Bun suite uses temporary package fixtures and can smoke-test a real installed Pi SDK when present. No real provider credentials are required.

Container and acceptance checks, also from the repository root:

```sh
sh web/tests/deployment/compose.test.sh
docker build -f web/Dockerfile --target ui-acceptance -t pixie-ui-acceptance .
docker run --rm --network none --shm-size 256m pixie-ui-acceptance
docker build -f web/Dockerfile --target pixie -t pixie .
```

Mount `/artifacts` to retain the acceptance run's generated artifacts. The container-image workflow runs one shared validation graph before publishing the image. Acceptance covers short viewport composer access, file/Git views, attachments, streaming/reconnect, provider setup, keyboard focus and both themes at narrow and wide sizes. Apple Container validates Linux processes and images; it does not establish Docker Compose host-network behavior.

`bun run dev:web` uses the same frontend entry and Linux Go fixture. Builds verify vendored Mewa assets and enforce the initial JavaScript budget. Keep WebUI and contract tests under `web/tests` and `shared/tests`; new Go unit tests may be colocated; use regression cases for observable behavior and realistic failure modes.

`bun run --cwd web scripts/check-performance.ts` validates an operator-supplied `performance-evidence.json` record. It intentionally fails when that live artifact is absent; static fixtures and cross-compilation do not establish a performance result on either architecture.
