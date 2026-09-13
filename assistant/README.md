# Pixie assistant

The runtime is the Go `pixie-assistant` binary in `cmd/pixie-assistant`. It supervises the user's selected Pi executable through native RPC and exposes the private loopback service consumed by the Pixie controller. The release build has no Bun runtime dependency.

Build from source, from this directory:

```sh
CGO_ENABLED=0 go build -trimpath -o ../package/dist/pixie-assistant ./cmd/pixie-assistant
```

The service reads an absolute private JSON configuration file that selects the literal loopback host, port, Pi agent directory and optional Pi executable. Provider authentication, model selection and extension configuration remain native Pi operations. Install and start the service as documented in [deployment](../docs/deployment.md).

`bridge/` holds the opt-in administration bridge sidecar. It runs under Bun, resolves exactly one selected Pi installation through that installation's public SDK entrypoint, and stays typechecked and tested as part of the assistant workspace; it is not part of the Go assistant binary. The Go adapter is not ready to replace a working deployed service until the session-routing, settlement, capability, recovery, restart, and native-profile gaps in the [roadmap](../roadmap/README.md#confirmed-defects-and-integration-risks) are closed.
