# Pixie

Pixie is a self-hosted Web UI for [Pi](https://github.com/earendil-works/pi). The current host assistant uses Pi's SDK; the Pixie controller, interface and Browser MCP publisher run in Docker, with a supported single-binary install documented in [deployment](docs/deployment.md).

The interface provides persistent chats, streaming, image/text attachments, native model controls, read-only files and Git views, goals and schedules. Optional native integrations provide delegation, plans, MCP tools and memory.

Host integration lives in [assistant/](assistant/README.md). Application code, contracts and interface live in package/.

Follow [deployment](docs/deployment.md) to configure the host service, private secrets, application state and read-only project mounts. For the prebuilt image:

```sh
docker compose --env-file .pixie up -d
```

Open <http://127.0.0.1:7312>. Pixie is intended for one trusted user.

[Architecture](docs/architecture.md) · [Pi integration](docs/pi.md) · [Extensions](docs/pi-extensions.md) · [MCP](docs/mcp.md) · [Development](docs/development.md) · [Security](docs/security.md)

The [implementation roadmap](roadmap/README.md) covers the shared Go assistant, six-column workspace, both host builds, commit-named Docker/releases and optional Canvas/Design modules. Those are implementation targets, not a description of features already shipped.

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
