# Pixie

Pixie is a self-hosted web interface for [Pi](https://github.com/earendil-works/pi). The current assistant runs on the host using Pi's SDK; the interface, controller and optional Browser MCP module run in Docker.

Pixie supports persistent chats, streaming, images and text attachments, queues, search and forks. Projects provide read-only file previews and Git diffs. Provider configuration uses Pi's native APIs. Plans, delegation, MCP tools and Signet use optional native integrations; goals, questions and schedules are Pixie features.

Host integration lives in `assistant/`; the application and interface live in `package/`. Pixie is intended for one trusted user.

Follow [deployment](docs/deployment.md) to configure the host service, credentials, state and read-only project mounts. For the prebuilt Compose image:

```sh
docker compose --env-file .pixie pull
docker compose --env-file .pixie up -d --no-build
```

Open <http://127.0.0.1:7312>.

The [implementation roadmap](roadmap/README.md) covers the Go assistant, six-column workspace, dual host builds and optional Canvas/Design modules. These are implementation plans, not claims that the new release artifacts are already available.

[Architecture](docs/architecture.md) · [Pi integration](docs/pi.md) · [Extensions](docs/pi-extensions.md) · [MCP](docs/mcp.md) · [Development](docs/development.md) · [Security](docs/security.md)

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
