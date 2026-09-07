# Development

Use the pinned Bun and Go versions in `pixie/package.json` and `pixie/go.mod`. Go filesystem checks require Linux; use disposable containers on macOS.

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

Native SDK tests use temporary Pi state, local fixture providers, authenticated WebSockets and real local MCP transports. They cover vanilla fallback, credentials, sessions, streaming/replay, agents and MCP. Go tests cover application persistence, queues, schedules, project ownership and Browser boundaries. No real provider credentials are required.

From the repository root:

```sh
sh pixie/tests/deployment/compose.test.sh
docker build -f pixie/Dockerfile --target ui-acceptance -t pixie-ui-acceptance .
docker run --rm --network none --shm-size 256m pixie-ui-acceptance
docker build -f pixie/Dockerfile --target pixie -t pixie .
docker build -f pixie/Dockerfile --target mcp -t pixie-mcp .
```

Mount `/artifacts` to retain browser evidence. Acceptance covers file/Git views, attachments, streaming/reconnect, provider setup, keyboard focus and both themes at narrow and wide sizes. Apple Container validates Linux processes and images; it does not establish Docker Compose host-network behavior.

`bun run dev:web` uses the same frontend entry and Linux Go fixture. Builds verify vendored Mewa assets and enforce the initial JavaScript budget. Keep tests under `tests/`; use regression cases for observable behavior and realistic failure modes.
