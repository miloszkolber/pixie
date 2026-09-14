# Pixie

Pixie is a self-hosted web workspace for [Pi](https://github.com/earendil-works/pi). The `pixie_assistant` host runs Pi sessions, the `pixie_web` Go controller and UI serve the workspace, and the two share the protocol contracts in `shared/`. The project is in an experimental development phase; the [roadmap](roadmap/roadmap.md) is the canonical plan.

The interface provides persistent chats, streaming, image/text attachments, native model controls, read-only files and Git views, goals and schedules. Optional native integrations provide delegation, plans, MCP tools and memory.

Source lives in [assistant/](docs/assistant.md), `web/` and `shared/`; `cli/` packages the assistant with a bundled Pi.

Follow [deployment](docs/deployment.md) to configure the host service, private secrets, application state and read-only project mounts. For the prebuilt image:

```sh
docker compose --env-file .pixie up -d
```

Open <http://127.0.0.1:7312>. Pixie is intended for one trusted user.

[Architecture](docs/architecture.md) · [Pi integration](docs/pi.md) · [Security](docs/security.md) · [Deployment](docs/deployment.md) · [Development](docs/development.md) · [Release checklist](docs/release-checklist.md)

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
