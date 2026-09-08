# @pixie_ai/pixie-assistant

Small Pi SDK service with native resource loading, agent authoring, optional MCP administration and a generic extension UI bridge. The unmodified `@earendil-works/pi-coding-agent` SDK owns execution. Optional packages are installed and configured through Pi, not bundled by the assistant.

Requires [Bun](https://bun.sh) 1.4.0 or compatible. See [deployment](https://github.com/miloszkolber/pixie/blob/main/docs/deployment.md) for setup. Publication uses `pixie-assistant-v*` tags and requires explicit release approval. Published `0.1.0` remains unchanged.

```sh
PIXIE_PI_SECRET_KEY=<at-least-16-chars> bunx @pixie_ai/pixie-assistant@<version> \
	--agent-dir ~/.pi/agent
```

The service listens on `127.0.0.1:3284` by default (`--host`/`--port` override it). Provider authentication, model selection and extension configuration remain native Pi operations. Local `llama.cpp` can use `--llama` plus `LLAMA_BASE_URL` in the environment.

The workspace's [subagent patch](../patches/README.md) does not travel with an independently installed optional package. Native child-launch compatibility remains a separate verification gate. The assistant does not install or patch the user's optional packages.
