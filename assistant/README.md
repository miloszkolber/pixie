# Pixie assistant

The intended runtime is the Go `pixie-assistant` binary in `cmd/pixie-assistant`. It supervises the user's selected Pi executable through native RPC and exposes the private loopback service consumed by the Pixie controller. The release build has no Bun runtime dependency.

Build from source:

```sh
CGO_ENABLED=0 go build -trimpath -o ../package/dist/pixie-assistant ./cmd/pixie-assistant
```

Run it with an absolute private configuration file:

```sh
PIXIE_PI_SECRET_KEY=<at-least-32-characters> \
PI_CODING_AGENT_DIR="$HOME/.pi/agent" \
./package/dist/pixie-assistant serve --config "$HOME/.config/pixie/assistant.json"
```

The configuration selects the literal loopback host, port, Pi agent directory, and optional Pi executable. Provider authentication, model selection, and extension configuration remain native Pi operations.

`src/`, this directory's private package manifest, and the root optional-extension patches are retained legacy implementation and compatibility-test inputs. They are not an npm distribution path. The Go adapter is not ready to replace the deployed legacy service until the session-routing, settlement, capability, recovery, restart, and native-profile gaps in the [roadmap](../roadmap/README.md#confirmed-defects-and-integration-risks) are closed.
