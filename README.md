# Pixie

Pixie is a self-hosted web workspace and bundled native interface for [Pi](https://github.com/earendil-works/pi). The repository builds two public Linux products from one Go module and one Bun package; the project remains experimental and the [roadmap](roadmap/roadmap.md) is the canonical plan.

## Public products

| Product | Use |
| --- | --- |
| `pixie_web` | Controller-only Go web workspace. It contains and starts no Bun, Node or Pi, and connects to a separately managed `pixie` host over authenticated loopback. |
| `pixie` | Host Pi owner. Bundles Bun `1.4.2`, the Pi SDK and the assistant host connector. Bare `pixie` is the regular bundled native Pi TUI; run its host with `pixie serve --config ABS`. The TUI runs concurrently with the server. |

`pixie_assistant` is the archive-internal bundled JavaScript host `libexec/pixie_assistant.js`, run by the bundled Bun runtime. It is not a compiled executable or a public product, command, unit, or archive.

The host archive bundles the pinned Bun `1.4.2` runtime `runtime/bin/bun` and Pi `0.86.1`; no Node runtime is bundled. Bun `1.3.14` cannot run Pi's bundle (`undici` `webidl.util.markAsUncloneable`), so `1.4.2` is required. The root `pixie` command opens Pi's native TUI through `runtime/bin/bun runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js`. The archive retains Pi's normal TUI, extensions, configuration, trust, and session behavior while excluding Pi RPC and blocking Pi self-update; update the Pixie archive through its installer or package manager instead.

## One Pi owner

Use `pixie` with `pixie_web`: the host owns Pi sessions and the controller serves the UI, reaching the host over loopback. Only `pixie serve` takes the agent-directory lock; a second server collides with exit status `73`. Stop the managed owner or choose a separate `PI_CODING_AGENT_DIR` for it.

The interface provides persistent chats, streaming, image attachments, native model controls, read-only files and Git views, goals, and schedules. See the [SDK coverage inventory](docs/sdk-coverage.md) for the supported boundary and its gaps.

## Docker

Docker publishes only the controller-only `pixie_web` image, which connects to a separately installed `pixie` host. The image contains no Bun, Node or Pi. The Compose file declares no fixed container names. The Compose definition is neither a sandbox nor deployment evidence.

The verified release workflow publishes GitHub releases and controller images. Publication does not approve a deployment.

[Architecture](docs/architecture.md) · [Pi integration](docs/pi.md) · [SDK coverage](docs/sdk-coverage.md) · [Security](docs/security.md) · [Deployment](docs/deployment.md) · [Development](docs/development.md) · [Release checklist](docs/release-checklist.md)

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
