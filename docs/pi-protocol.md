# Assistant protocol

The wire contract between the controller and the Pi host service. The service embeds the pinned Pi SDK and translates these frames onto native APIs; the protocol is the stable surface that keeps the host service swappable. [Pi integration](pi.md) describes ownership and projection; this file defines the transport.

## Transport

The service listens on loopback `/pi` over WebSocket. Requests carry `Authorization: Bearer <PIXIE_PI_SECRET_KEY>`; connections without a valid bearer are rejected, and browser `Origin` headers are refused. Frames are JSON text.

## Welcome

The first request is `runtime.hello`:

```json
{ "id": 1, "method": "runtime.hello", "params": {} }
```

The result carries `protocolVersion` (`1`), a stable `runtimeId`, the host SDK `version`, and a capability map. `sessions`, `providers` and `agents` are always `1`; optional feature groups (for example `mcp`, `llama`) appear only when a supported native runtime is loaded. Clients must check capability versions before using their methods.

## Frames

Client requests are `{ "id": <positive safe integer>, "method": string, "params": object }`. Replies are `{ "id", "result" }` or `{ "id", "error": { "code", "message" } }`. Events carry a `method` and `params` without an id; `session.event` frames carry `sessionId` and a monotonically increasing `sequence` used by snapshot checkpoints.

- Malformed JSON closes the connection with `1007`.
- An id that is not a positive safe integer closes the connection with `1008`.
- A reused in-flight id answers with an error frame (code `-32000`) and keeps the connection open.
- Frames above 32 MiB close the connection with `1009`; a consumer slower than a 32 MiB send or session-attachment buffer closes with `1013`.
- More than 128 pending requests per connection answer with a `-32000` error frame.
- Operation failures answer with a `-32000` error frame unless the failure carries a specific code (for example `-32002` for an unknown or ambiguous session).
- An event payload that cannot be serialized degrades to a `host.unserializable` stub with `sessionId` and `sequence` preserved; unserializable replies degrade to an error frame.

## Methods

| Group | Shape |
| --- | --- |
| `runtime.hello`, `runtime.capabilities` | Service identity, protocol version and capability versions |
| `runtime.restart` | End the process for the service manager; enabled per deployment with `PIXIE_ALLOW_SELF_RESTART=1` and rejected otherwise. Replies `ok`, then closes peers and exits ([deployment](deployment.md)) |
| `pi.extensions.list` / `configure` / `reload` | Native resource inventory, deferred configuration and reload status |
| `pi.providers.*`, `pi.defaults.*`, `pi.preferences.*`, `provider.login*` | Provider catalog, credentials and OAuth flows; secrets never leave the host |
| `session.create` / `fork` / `load` / `list` / `prompt` / `steer` / `abort` / `queue*` / `delete` / `rename` / `archive` / `setModel` / `setThinkingLevel` and related | Session lifecycle, runs and configuration |
| `session.goal*`, `session.plan*`, `session.stats`, `session.commands`, `session.agentMentions` | Application projections on top of native sessions |
| Capability methods (`mcp.*`, `llama` feature surface) | Versioned groups advertised in the welcome capabilities |

Unknown methods return an error frame. Features are gated by capability versions, not assumed.

## Versioning

`protocolVersion` changes only for breaking wire changes. Within a version the protocol is additive: new methods, fields and capability groups join without a bump, and optional features stay behind capability versions. The controller negotiates at `runtime.hello` and refuses incompatible hosts.
