# Development

Use the pinned Bun and Go versions in `package.json` and `package/go.mod`. Go filesystem checks require Linux; use disposable containers on macOS.

The repository root holds the Bun workspace, lockfile and shared tooling. Pi source lives in `pi/`; application source and tests live in `pixie/`.

From `pixie/`:

```sh
bun install --frozen-lockfile
bun run check:deps
bun run lint
bun run typecheck
bun test tests
go test -race -count=1 ./...
go vet ./...
bun run build
```

Native SDK tests use temporary Pi state, local fixture providers, authenticated WebSockets and real local MCP transports. They cover vanilla fallback, credentials, sessions, streaming/replay, agents and MCP. The Go controller suite also launches the real Bun/Pi host and verifies vanilla and optional-extension sessions through the application WebSocket. This requires Bun; CI runs it on amd64 and arm64. Go tests cover application persistence, queues, schedules, project ownership and Browser boundaries. No real provider credentials are required.

Host fixture measurements (`bun x bun@1.4.0 package/tests/pi-native-parity/host-benchmark.ts`, Linux x86-64, fresh process per sample): baseline session creation ~55 ms with ~8 MB RSS growth and 4 tools (1.1 KB definitions); the optional extension set (todo, web, ask, subagent) adds ~5 ms, ~1 MB and 5 tools (6.6 KB definitions). Cold/warm MCP transport timings live in `package/tests/pi-native-parity/mcp-benchmark.ts`. These are fixture numbers, not provider token counts or production timings; arm64 stays unmeasured here.

From the repository root:

```sh
sh package/tests/deployment/compose.test.sh
docker build -f package/Dockerfile --target ui-acceptance -t pixie-ui-acceptance .
docker run --rm --network none --shm-size 256m pixie-ui-acceptance
docker build -f package/Dockerfile --target pixie -t pixie .
```

Mount `/artifacts` to retain browser evidence. The container-image workflow runs one shared validation graph before publishing the image. Acceptance covers short viewport composer access, file/Git views, attachments, streaming/reconnect, provider setup, keyboard focus and both themes at narrow and wide sizes. Apple Container validates Linux processes and images; it does not establish Docker Compose host-network behavior.

`bun run dev:web` uses the same frontend entry and Linux Go fixture. Builds verify vendored Mewa assets and enforce the initial JavaScript budget. Keep tests under `tests/`; use regression cases for observable behavior and realistic failure modes.
