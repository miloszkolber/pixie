# @pixie_ai/pixie-assistant

Pi SDK service with Pixie's extension profiles: todo, web, questions, Signet marker, subagents, MCP adapter, agent authoring and the generic extension UI bridge. No Pi fork; the unmodified `@earendil-works/pi-coding-agent` SDK stays the runtime.

Requires [Bun](https://bun.sh) 1.4.0 or compatible. Published from [pixie](https://github.com/miloszkolber/pixie) release tags (`pi-host-v*`); see `docs/deployment.md` there for the full setup.

```sh
PIXIE_PI_SECRET_KEY=<at-least-16-chars> bunx @pixie_ai/pixie-assistant@<version> \
	--agent-dir ~/.pi/agent \
	--extensions mcp,agents,rpiv-todo,rpiv-web,rpiv-ask
```

The service listens on `127.0.0.1:3284` by default (`--host`/`--port` override it). Omit `--extensions` for baseline Pi. Provider authentication and model selection remain native Pi operations. Local `llama.cpp` needs the `llama` profile plus `LLAMA_BASE_URL` in the environment.

One known gap for registry installs: `subagent` child runs resolve Pi's RPC entrypoint through a [local patch](../../extensions/local-patches/README.md) that applies to workspace installs only. Until upstream accepts it, run child delegations under the workspace checkout; everything else, including local `llama.cpp` inference, works unchanged from the published package.
