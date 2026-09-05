# Pixie

Pixie is a self-hosted Web UI for [Pi](https://github.com/earendil-works/pi). Pi runs on the host; Pixie and its Browser MCP service run in Docker.

- Persistent concurrent chats, streaming, images and text attachments, steering, queues, search and forks.
- Project directories with read-only files, image previews and Git diffs.
- Native Pi provider, credential, model and thinking-level management.
- Optional defined subagents, plans, MCP tools, interactive MCP Apps, Browser and Signet memory.
- Pixie-owned goals, tasks, questions and schedules.

Optional controls appear when a compatible extension is available. The baseline uses the unmodified Pi SDK and its normal tools and settings.

Pi source and configuration examples live in [`pi/`](pi/README.md); the application lives in `pixie/`.

Follow [deployment](docs/deployment.md) to configure the host service, secrets, state and project mounts, then run:

```sh
docker compose --env-file .pixie up -d --build
```

Open <http://127.0.0.1:7312>. Pixie is intended for one trusted user.

[Architecture](docs/architecture.md) · [Pi integration](docs/pi.md) · [Extensions](docs/pi-extensions.md) · [MCP service](docs/mcp.md) · [Development](docs/development.md) · [Security](docs/security.md)

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
