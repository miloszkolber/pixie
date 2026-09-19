# Assistant host

`pixie_assistant` is the archive-internal host bundle `libexec/pixie_assistant.js`, built from `src/assistant/serve.ts` and run by the pinned Bun `1.4.0` runtime `runtime/bin/bun`. It is not a compiled executable or a public product, command, systemd unit, or archive entrypoint. No Node runtime is bundled.

The host runs Pi sessions in-process through the archive's bundled `@earendil-works/pi-coding-agent` SDK. It exposes a private authenticated loopback protocol to the controller and does not start `pi --mode rpc`, discover an external Pi executable, or use an administration bridge sidecar.

`pixie serve --config ABS` starts the host. Bare `pixie` runs the native Pi TUI concurrently with the server without taking the owner lock.

Pi owns execution, transcripts, credentials, models, settings, tools, extensions, and trust. The host projects supported native behavior to the web controller; it does not intercept tools, replace prompts, silently install packages, or establish a second model or MCP policy.

The host requires literal-loopback configuration, an absolute agent directory, and a `PIXIE_PI_SECRET_KEY` of at least 32 characters. It reports its negotiated operation set at `runtime.hello`; callers must use that result instead of assuming optional operations are present. See [Pi integration](pi.md) and [SDK coverage](sdk-coverage.md).
