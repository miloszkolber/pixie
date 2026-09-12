# Pi extensions

The current assistant loads ordinary Pi resources through the native resource loader. Install and configure extensions through Pi. File/package/project resources do not need a Pixie-specific marker to execute. Optional web administration requires a supported API; native tools and supported `ctx.ui` calls do not require package-specific wrappers.

Native project trust controls project resources. User/global resources load under the native configuration. Saved trust or resource changes apply on a subsequent native load; do not assume changing configuration changes an already resident session.

## Inventory and configuration

Settings → Extensions distinguishes configured resources from extensions loaded in a resident session. Missing sources stay visible; inspection does not install packages, import extension code or create/reload a session. Non-resident sessions have no live loaded inventory. Loaded versions/interface support remain unknown when native metadata does not supply them.

Browser `pi.nativeExtensions` maps to host `pi.extensions.list`; `pi.nativeExtensionConfigure` maps to `pi.extensions.configure`. The current configuration writer uses native resolver/settings APIs, a scoped resource key and expected revision. Confirmed changes preserve unrelated settings and resource filters. Unsupported changes, malformed targets and stale revisions are rejected. Static CLI resources such as `--llama` are not editable here.

A successful save reports `saved=true`, `loaded=false` and `reload=deferred`. Refresh inventory before retrying an uncertain save. A subsequent load failure is reported separately and does not silently roll back the saved configuration. Reopen the session or restart the configured host service to apply changes. Per-session hot reload is not exposed. The [roadmap](../roadmap/README.md) covers the replacement runtime and compatibility rules.

Native extension inventory is separate from MCP connection administration. The similarly named `pi.config.extensions.*` and `pi.session.extensions.*` methods concern MCP connections, not this resource inventory.

## Current Web UI bridge

The SDK host maps `select`, `confirm`, `input`, `editor` and `notify`, plus text status/widgets, transient title and working-message hints. Blocking requests are scoped to session and request ID, settle once, and expire within 30 minutes. Select returns an offered string, confirm a boolean and text dialogs preserve text; dismissal returns the native cancellation value.

Pending requests replay on session load. Browser replies use `session.uiReply`/`session.uiCancel`; the controller calls host `session.uiResponse`/`session.uiCancel`. All clients dismiss a settled request. History questionnaire cards are read-only tool-result recaps. Without a reliable tool-call association, active requests stay session-level rather than being attached by guessed timing or tool name.

Each session permits 16 pending dialogs. Passive status/widget collections each permit 16 keys; widgets accept string arrays, not component factories. Text is escaped, ordinary passive updates are bounded, and clears remain deliverable. Passive state currently clears on connection/context loss and is not replayed. A transient extension title does not rename the saved conversation.

Terminal input, custom TUI factories, footers/headers, autocomplete and composer get/set/paste APIs are unsupported by the current SDK bridge. The multiline editor dialog has its own draft. Stop and session teardown cancel pending interactions. Generic extension liveness can keep background work resident without redefining native run settlement.

The planned native-RPC bridge differs from this implementation; the [retained feature index](../roadmap/README.md#retained-feature-index) records required behavior and the [integration findings](../roadmap/README.md#confirmed-defects-and-integration-risks) record unresolved fidelity work. Do not assume RPC provides every SDK bridge method.

## Agent definitions

Definitions live in `<agentDir>/agents/*.md` and `<project>/.pi/agents/*.md`. Pixie's `pi.sources.*` API provides Markdown CRUD and `@agent` discovery without registering model tools or implementing delegation.

Frontmatter includes `name`, `description` and optional `model`; unspecified fields are preserved. Names accept letters/numbers/spaces/underscores/hyphens up to 80 UTF-8 bytes. Complete files are limited to 64 KiB. Invalid definitions produce diagnostics. Editability does not establish execution eligibility: discovery, trust, children and execution belong to the installed native extension.

## Native MCP

The operator-installed native adapter is the only Pi MCP runtime. The current assistant's thin administration bridge discovers the public runtime-snapshot interface and registers session connections through runtime-register APIs, tested against adapter 2.32.1. Native tools remain the model-facing interface.

Identical attachments are idempotent; conflicting definitions fail. Global saved connections and session membership are distinct. Native `{mcpServers: ...}` configuration is not rewritten by legacy Pixie connection administration. Without a compatible adapter, MCP administration stays unavailable and baseline sessions remain usable.

See [MCP publication](mcp.md) for the application-side Browser module and its credentials.

## Local models and memory

`--llama` loads Pi's built-in llama.cpp provider through the current SDK export patch. The operator supplies `LLAMA_BASE_URL` and optional native credentials/`LLAMA_API_KEY`. Model selection, refresh and inference work headlessly; `/llama` management requires terminal UI. A requested profile fails explicitly when its required factory is unavailable.

Signet is operator-owned and loads through Pi's normal file-extension discovery. Its daemon, configuration and enablement are not managed by Pixie and are not a Pixie MCP connection. Other unfamiliar native extensions follow the same native loading and supported UI boundaries.
