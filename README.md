# Pixie

Pixie is a self-hosted Web UI for [Pi](https://github.com/earendil-works/pi). A Go host assistant supervises the selected Pi executable through native RPC; the Pixie controller, interface and Browser MCP publisher run in Docker. The release cutover is incomplete; the [roadmap](roadmap/README.md) is the canonical status and plan.

The interface provides persistent chats, streaming, image/text attachments, native model controls, read-only files and Git views, goals and schedules. Optional native integrations provide delegation, plans, MCP tools and memory.

Host integration lives in [assistant/](assistant/README.md); application code, contracts and interface live in `package/`.

Follow [deployment](docs/deployment.md) to configure the host service, private secrets, application state and read-only project mounts. For the prebuilt image:

```sh
docker compose --env-file .pixie up -d
```

Open <http://127.0.0.1:7312>. Pixie is intended for one trusted user.

[Architecture](docs/architecture.md) · [Pi integration](docs/pi.md) · [Security](docs/security.md) · [Deployment](docs/deployment.md) · [Development](docs/development.md) · [Release checklist](docs/release-checklist.md)

The roadmap separates verified source behavior, integration defects, deferred work, evidence gaps and improvement ideas.

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
