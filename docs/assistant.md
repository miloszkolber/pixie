# Assistant

The assistant is the `pixie_assistant` process. It owns Pi interaction for Pixie: it starts or resumes Pi sessions, projects transcripts and dialogs, and exposes the private loopback service the `pixie_web` controller consumes.

## Target runtime

The target `pixie_assistant` is a Bun host that runs Pi sessions in-process through the operator's installed Pi SDK. It resolves the SDK from the operator's Pi installation at runtime and never bundles or forks Pi. This replaces the interim Go host's `pi --mode rpc` child processes and the separate Bun administration bridge: there is no RPC child model and no bridge sidecar.

The `cli/` build flavor ships the assistant plus a bundled Pi for users who do not already have one. It is a packaging variant of the same assistant source, not a separate supervisor.

Pi still owns execution, transcripts, native credentials, models, settings, tools, extensions and trust. The assistant projects that native behavior for the web controller; it does not intercept tools, replace prompts, install packages silently or implement a second model or MCP policy.

## Interim host

Until the Bun host lands, `assistant/` also contains the interim Go `pixie_assistant` in `assistant/cmd`. It supervises the selected public `pi` executable over native RPC and exposes the same private loopback service. The Go host is production-replacement work in progress: its remaining parity, lifecycle and real-Pi gaps are tracked in the [roadmap](../roadmap/roadmap.md). Do not replace a working deployment merely because the Go binary builds.

`assistant/bridge/` holds the opt-in administration bridge sidecar used by the interim Go host. It runs under Bun, resolves exactly one selected Pi installation through that installation's public SDK entrypoint and is not part of the Go binary. The bridge is retired together with the RPC child model once the in-process Bun host covers its operations.

## Build and run

Build the interim Go assistant from the repository root:

```sh
bun run build:assistant
```

The service reads an absolute private JSON configuration file that selects the literal loopback host, port, Pi agent directory and optional Pi executable. Provider authentication, model selection and extension configuration remain native Pi operations. Install and start the service as described in [deployment](deployment.md).

The shared wire contracts live in `shared/`; see [architecture](architecture.md) and [Pi integration](pi.md) for the current protocol and projection behavior.
