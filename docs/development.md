# Development

Use the pinned Bun and Go versions in the root `package.json` and the per-module `go.mod` files. Go filesystem checks require Linux; use disposable containers on macOS.

The repository root holds the Bun workspace, lockfile and shared build/test tooling. Source roots are `assistant/`, `web/` and `shared/`. The Bun assistant host lives in `assistant/src/`; the controller, UI and application tests live in `web/`; the protocol schema and generated catalogs live in `shared/`.

The release builder has three public Linux product targets: controller-only `pixie_web`, bundled-Pi `pixie_cli`, and full-suite `pixie`. `pixie_cli` and `pixie` both expose the regular native Pi TUI as `pixie`, bundle the pinned Bun `1.4.0` runtime `runtime/bin/bun` and Pi `0.85.1`, bundle no Node runtime, exclude Pi RPC, and block Pi self-update. Bun `1.3.14` cannot run Pi's bundle (`undici` `webidl.util.markAsUncloneable`), so `1.4.0` is required. The assistant is the bundled JavaScript host `libexec/pixie_assistant.js`, not a compiled executable, and remains archive-internal. Source builds and archive-layout checks do not prove a standalone distribution, credentialed Pi behavior, an approved Docker deployment, arm64 live lifecycle, or a published release.

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

`bun run test` runs the WebUI and contract suites from `web/tests` and `shared/tests` plus the Go suite. To run a Go module directly, use `go test -race -count=1 ./...` and `go vet ./...` from `web/` or `shared/`. The root `go.work` links the two modules; set `GOWORK=off` to build one module with only its own `replace` directives.

The source gates run alongside those commands:

- `bun run check:deps` runs `web/scripts/check-catalog.ts` and `web/scripts/check-boundaries.ts`. The boundary gate fails an `assistant/` import of `web/` internals, a `web/` import of `assistant/src`, a `shared/` import of either module, and any reintroduced Tailwind dependency or directive, reporting the file and line.
- `bun run check:conventions` runs `web/scripts/check-conventions.ts`, the portable anti-slop subset: no `object` parameters, no `unknown` parameters or returns outside the architectural boundary roots, no chained type assertions, a `SAFETY:` comment for every non-const assertion, no `filter().map()`, and no accumulating spread. Adapter-local occurrences outside those roots are recorded as reviewed ceilings in `web/scripts/conventions-boundaries.json`; a new violation fails the gate, and the list is debt to shrink.
- `bun run generate:config-schema` writes `docs/config-schema.json`, and `bun run check:config-schema` fails when that schema is stale relative to `web/cmd/runtime.go`, `assistant/src/serve.ts` and `web/cmd/internal/assistantconfig/config.go`. The schema documents the accepted JSON configuration; fail-closed runtime validation remains authoritative.
- `bun run check:runtime` runs `web/scripts/check-runtime.ts`. It matches the running Bun against the root `packageManager` pin, requires a writable bounded temporary workspace, and reports Go and CGO toolchain readiness. An unsupported runtime, full filesystem or missing toolchain is reported as `environment-blocked` with exit status 3, never as success. The wrapper form `bun run runtime:exec -- <command>` refuses to run under a blocked environment and otherwise exports `TMPDIR`, `TMP`, `TEMP` and `GOTMPDIR` into `.tmp-work/runtime` (override with `PIXIE_RUNTIME_TMPDIR`); the `test` script uses it.

`check:conventions` and `check:config-schema` are part of `bun run lint`; `check:boundaries` is part of `bun run check:deps`.

Go tests use temporary native state and authenticated WebSockets. They cover the application persistence, queues, schedules, nullable project grouping, reserved routing, project ownership and browser-MCP registration. The `assistant/tests` Bun suite uses temporary package fixtures and can smoke-test a real installed Pi SDK when present. No real provider credentials are required, so that smoke test does not establish credentialed Pi validation.

Container and acceptance checks, also from the repository root:

```sh
sh web/tests/deployment/compose.test.sh
docker build -f web/Dockerfile --target ui-acceptance -t pixie-ui-acceptance .
docker run --rm --network none --shm-size 256m pixie-ui-acceptance
docker build -f web/Dockerfile --target pixie -t pixie .
```

Mount `/artifacts` to retain the acceptance run's generated artifacts. The container-image workflow runs one shared validation graph before any separately authorized publication. Acceptance covers short viewport composer access, file/Git views, attachments, streaming/reconnect, provider setup, keyboard focus and both themes at narrow and wide sizes. Apple Container validates Linux processes and images; it does not establish Docker Compose host-network behavior or an approved Docker deployment.

`bun run dev:web` uses the same frontend entry and Linux Go fixture. Builds verify vendored Mewa assets and enforce the initial JavaScript budget. Keep WebUI and contract tests under `web/tests` and `shared/tests`; new Go unit tests may be colocated; use regression cases for observable behavior and realistic failure modes.

`bun run --cwd web scripts/check-performance.ts` validates an operator-supplied `performance-evidence.json` record. It intentionally fails when that live artifact is absent; static fixtures and cross-compilation do not establish a performance result on either architecture.
