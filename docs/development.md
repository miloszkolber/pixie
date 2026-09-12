# Development

Use the pinned Bun and Go versions in `package.json` and `package/go.mod`. Go filesystem checks require Linux; use disposable containers on macOS.

The `pixie/` directory holds the Bun workspace, lockfile and shared build/test tooling. The Go assistant module and retained legacy compatibility implementation live in `assistant/`; application source and tests live in `package/`; workspace patches live in `patches/`.

From `pixie/`:

```sh
bun install --frozen-lockfile
bun run check:deps
bun run check:docs
bun run check:coverage
bun run lint
bun run typecheck
bun run test
bun run build
```

`bun run test` runs the WebUI/contract suites plus the Go suite. To run the Go suite directly, use `go test -race -count=1 ./...` and `go vet ./...` from `package/`.

Legacy native-SDK tests use temporary Pi state, local fixture providers, authenticated WebSockets and real local MCP transports. They cover the behavior the Go replacement must retain. The controller compatibility test still launches that Bun/Pi host, so it is legacy parity evidence rather than Go-assistant evidence. Separate Go tests cover the assistant facade and application persistence, queues, schedules, nullable project grouping, reserved routing, project ownership and Browser boundaries. No real provider credentials are required.

## SDK upgrade gate

Bumping the pinned `@earendil-works/pi-coding-agent` and `@earendil-works/pi-ai` versions is gated by `bun run check:parity` (`package/tests/pi-native-parity`): native loading, extension matrix, MCP parity, lifecycle and patch-freshness suites must pass before the protocol and projection claims stay true. The gate requires the pinned Bun toolchain — older runtimes fail loudly (child launches need Bun 1.4+ for the SDK's network stack, and Bun below 1.4.0 wedges `server.stop()` after server-initiated WebSocket closes). Transport conformance for the assistant wire contract lives in `package/tests/pixie-assistant/protocol-conformance.test.ts` ([protocol](pi-protocol.md)).

Host fixture measurements (`bun x bun@1.4.0 package/tests/pi-native-parity/host-benchmark.ts`, Linux x86-64, fresh process per sample): baseline session creation ~55 ms with ~8 MB RSS growth and 4 tools (1.1 KB definitions); the optional extension set (todo, web, ask, subagent) adds ~5 ms, ~1 MB and 5 tools (6.6 KB definitions). Cold/warm MCP transport timings live in `package/tests/pi-native-parity/mcp-benchmark.ts`. These are fixture numbers, not provider token counts or production timings; arm64 stays unmeasured here.

Container and acceptance checks, also from `pixie/`:

```sh
sh package/tests/deployment/compose.test.sh
docker build -f package/Dockerfile --target ui-acceptance -t pixie-ui-acceptance .
docker run --rm --network none --shm-size 256m pixie-ui-acceptance
docker build -f package/Dockerfile --target pixie -t pixie .
```

Mount `/artifacts` to retain browser evidence. The container-image workflow runs one shared validation graph before publishing the image. Acceptance covers short viewport composer access, file/Git views, attachments, streaming/reconnect, provider setup, keyboard focus and both themes at narrow and wide sizes. Apple Container validates Linux processes and images; it does not establish Docker Compose host-network behavior.

`bun run dev:web` uses the same frontend entry and Linux Go fixture. Builds verify vendored Mewa assets and enforce the initial JavaScript budget. Keep WebUI and contract tests under `package/tests/`; new Go unit tests may be colocated; use regression cases for observable behavior and realistic failure modes.

`bun run --cwd package scripts/check-performance.ts` validates an operator-supplied `performance-evidence.json` record. It intentionally fails when that live artifact is absent; static fixtures and cross-compilation do not establish PERF-01 on either architecture.
