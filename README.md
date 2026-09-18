# Pixie

Pixie is a self-hosted web workspace and bundled native interface for [Pi](https://github.com/earendil-works/pi). The repository builds three public Linux products from shared contracts in `shared/`; the project remains experimental and the [roadmap](roadmap/roadmap.md) is the canonical plan.

## Public products

| Product | Use |
| --- | --- |
| `pixie_web` | Controller-only Go web workspace. It contains and starts no Bun, Node or Pi, and connects to a separately managed `pixie_cli` host over authenticated loopback. |
| `pixie_cli` | Split-topology Pi owner. Bundles Bun `1.4.0`, the Pi SDK and the assistant host. Its `pixie` command is the regular bundled native Pi TUI; run its host with `pixie_cli serve --config ABS`. |
| `pixie` | Full-suite alternative Pi owner. Bundles Bun `1.4.0`, the Pi SDK, assistant host and controller. Its `pixie` command is the regular bundled native Pi TUI; the archive-internal service command is `libexec/pixie_full serve --assistant-config ABS --web-config ABS`. |

`pixie_assistant` is the archive-internal bundled JavaScript host `libexec/pixie_assistant.js`, run by the bundled Bun runtime. It is not a compiled executable or a public product, command, unit, or archive.

Pi-bearing archives and the full image bundle the pinned Bun `1.4.0` runtime `runtime/bin/bun` and Pi `0.85.1`; no Node runtime is bundled. Bun `1.3.14` cannot run Pi's bundle (`undici` `webidl.util.markAsUncloneable`), so `1.4.0` is required. The root `pixie` command opens Pi's native TUI through `runtime/bin/bun runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js`. The archives retain Pi's normal TUI, extensions, configuration, trust, and session behavior while excluding Pi RPC and blocking Pi self-update; update the Pixie archive through its installer or package manager instead.

## Choose one Pi owner

Use `pixie_cli` with `pixie_web` for the split topology, or use full `pixie` as the alternative owner. Do not co-install their global `pixie` commands or point them at the same Pi agent directory. The owner lock rejects a collision with exit status `73`; stop the managed owner and wait for the TUI to be idle before handoff, or choose separate `PI_CODING_AGENT_DIR` values. Pixie does not attach to an active TUI session.

The interface provides persistent chats, streaming, image attachments, native model controls, read-only files and Git views, goals, and schedules. See the [SDK coverage inventory](docs/sdk-coverage.md) for the supported boundary and its gaps.

## Docker

The source Compose file defines a full `pixie` image on Debian trixie-slim (glibc) with a dedicated Pi-state volume and a controller-only `pixie_web` image that connects to a separately installed `pixie_cli` host. The full image runs the bundled Bun runtime with the host and controller under `tini`; the controller-only image contains no Bun, Node or Pi. The Compose file declares no fixed container names. Run the native TUI in the full image from an interactive terminal with `docker compose --profile full run --rm --entrypoint /bin/sh pixie -c 'exec /app/pixie'`. The Compose definition is neither a sandbox nor deployment evidence.

The verified release workflow publishes GitHub releases and controller images. Publication does not approve a deployment.

[Architecture](docs/architecture.md) · [Pi integration](docs/pi.md) · [SDK coverage](docs/sdk-coverage.md) · [Security](docs/security.md) · [Deployment](docs/deployment.md) · [Development](docs/development.md) · [Release checklist](docs/release-checklist.md)

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md).
