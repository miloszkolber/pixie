# MCP client verification

The `mcp` and `pi-mcp-adapter` profiles use the same pinned upstream adapter. The custom transport and its duplicate storage implementation are removed. The old package/file entry remains an eight-line compatibility shim for persisted configuration. A [local upstreamable patch](../pi/extensions/local-patches/README.md) supplies the two host APIs required by retained Apps, without changing model-tool or transport execution.

## Protocol boundary

| Surface | Adapter bridge behavior |
| --- | --- |
| `mcp.attach`, session objective and Browser definitions | Public runtime registration, proxy-only, no transport implementation |
| `pi.config.extensions.*` | Locked legacy configuration administration, no rewrite of native `{mcpServers: ...}` files |
| `pi.session.extensions.*` | Persisted additions/removals, owned runtime disposal, reload restoration |
| `pi.tools.call` | Decode persisted `<server>__<tool>` aliases only at the protocol boundary, execute upstream `{server, tool, args}`, return raw `mcpResult` |
| `pi.apps.tools.call` | Controller-selected App origin after ownership checks, raw result through `callMcpAppToolV1`, no model proxy bypass flag |
| Idle eviction and shutdown | Native `session_shutdown`, upstream connection cleanup, bridge registration disposal |
| Caller authority | Existing host peer attachment checks and controller project/session checks remain unchanged |
| `pi.resources.read` | Patched public `readMcpResourceV1` API, raw MIME/metadata preserved, same-session registered connections only |
| Retained Apps | App-visible tool calls, raw resources, live/reloaded attachment projection, complete `mcp`/`mcp-apps` and origin-aware `mcp-app-tools` markers |

The patch adds 113 lines to one upstream file, with no deletions, through Bun `patchedDependencies`. Both APIs accept JSON-safe versioned requests and return raw envelopes, with the installing Pi instance and optional `AbortSignal` passed out-of-band. The manager stays private. Lifecycle generation and owner checks prevent pending calls from crossing shutdown or session replacement. App visibility and upstream configured tool approval remain enforced. The compatibility bridge adds no model-visible aliases. Native adapter configuration remains operator-owned, while legacy connection maps and membership files retain their existing administration/read paths. The host CLI aligns `PI_CODING_AGENT_DIR` with its selected directory.

## Demonstrated gates

The MCP suite exercises stdio, SDK Streamable HTTP, SSE, cached lazy discovery, reconnect, abort, image and structured results, bearer headers, runtime registration and canonical status values. An adapter-only authenticated host test verifies session attachment, cross-peer rejection, execution after idle eviction/reload, and persisted removal.

OAuth uses a local authorization server and upstream's explicit `PI_MCP_ADAPTER_TEST_AUTH_STORE=memory` hook. The test verifies discovery, S256 PKCE at the token endpoint, rejection of the wrong state before exchange, code exchange, authenticated requests and refresh after a 401. This proves protocol and credential-store mechanics, not production OS keyring availability. A separate unavailable-store test currently observes `auth_start_failed` with `OAuth startup cleanup failed`, which masks the underlying storage cause.

Valid `text/html;profile=mcp-app` content starts upstream's own UI server and returns an HTTP 200 page with browser launching suppressed by `MCP_UI_VIEWER=none`. Invalid `text/plain` content is separately tested for inline fallback. Neither result establishes integration with Pixie's existing Apps iframe.

The unpatched resource proxy returns transformed content and resource identity, not raw `ReadResourceResult.contents`, MIME or `_meta`. The npm registry still reports 2.32.1 as its latest release, with no raw-resource export. The permitted local patch addresses this exact gap through the existing manager. Real SDK HTTP tests verify an unlisted valid App resource, top-level and per-content metadata, MIME, bearer rejection, instance isolation including identical server names with different credentials, invalid request versions, error propagation, pre-abort, in-flight cancellation and shutdown.

App-only tools remain absent from model discovery and uncallable through the model proxy. The authorized `callMcpAppToolV1` API permits App-only and default-visible tools, rejects explicit model-only tools, and preserves metadata and images. Tests cover cross-server/instance rejection, cancellation and upstream approval denial with origin `iframe`. Native upstream-owned configuration also works through the host App APIs without duplicate runtime registration.

Pi tool-result observation stores only trusted attachment metadata in hidden native entries, then adds it to Pixie's presentation copies. It does not modify the model's result. A real fixture model turn verifies live metadata and reopening after idle eviction. Controller tests verify App/project/session/attachment ownership and same-server rejection before selecting the dedicated method, plus compatibility with older hosts. Proxy tool presentation retains actual arguments and objective-question classification. The default alias switch and legacy transport deletion are complete. No runtime deployment, production-keyring validation or interactive browser acceptance is claimed.

The locked Bun typecheck passes. Host/native parity tests report 147 passed, 1 skipped and no failures. The full Bun suite reports 366 passed, 1 skipped and 1 unrelated failure in the untouched `tests/webui/chat/builtin-tools.test.ts` with Svelte `lifecycle_outside_component`, also reproduced in isolation without MCP tests. All Go tests and vet pass with `CGO_ENABLED=0` because this container lacks a C compiler. Race checks and image/browser acceptance are not run. A fresh production-only install from the copied workspace manifests, lockfile and patch succeeds with `--frozen-lockfile`, reporting `{"bun":"1.4.0","apis":["readMcpResourceV1","callMcpAppToolV1"],"freshFrozenInstall":true}`. The harness removes its temporary checkout in `finally`. No commits or deployment are part of this work.

## Reproducible measurements

Run from the repository root:

```sh
bun x bun@1.4.0 pixie/tests/pi-native-parity/mcp-benchmark.ts
bun x bun@1.4.0 pixie/tests/pi-native-parity/mcp-patch-install.ts
bun x bun@1.4.0 test pixie/tests/pi-native-parity/mcp-resource-api.test.ts
bun x bun@1.4.0 test pixie/tests/pi-native-parity/mcp-parity.test.ts pixie/tests/pi-host/server.test.ts
bun x bun@1.4.0 run typecheck
```

The benchmark emits JSON Lines with all 20 warm-call timings per sample. It now runs three fresh adapter processes. Each uses an empty agent directory and a local stdio fixture with 40 tools and `scriptMode: false`. The measured startup interval begins after imports and fixture setup, so it is not process launch time. RSS includes the host process and loaded modules, not the fixture subprocess. Schema bytes are serialized UTF-8 bytes, not model tokens. Temporary fixtures are removed in `finally`. The removed custom engine is not retained or simulated for benchmarking.

Historical pre-deletion samples on this Linux environment with Bun 1.4.0, retained as raw baseline evidence. Imports and implementation have since changed, so these are not a controlled current speedup comparison:

| Engine/sample | MCP tools | Schema bytes | Session create ms | Cold call ms | Create + first call ms | RSS before bytes | RSS after warm bytes | Process peak RSS KiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| custom/0 | 40 | 11151 | 157.063407 | 3.869977 | 160.933384 | 122302464 | 137949184 | 134716 |
| adapter/0 | 1 | 2746 | 171.054202 | 98.773119 | 269.827321 | 123846656 | 150007808 | 146492 |
| adapter/1 | 1 | 2746 | 170.601759 | 91.465301 | 262.067060 | 124125184 | 150085632 | 146568 |
| custom/1 | 40 | 11151 | 171.744858 | 2.748937 | 174.493795 | 122048512 | 138727424 | 135476 |
| custom/2 | 40 | 11151 | 156.883808 | 3.984363 | 160.868171 | 123392000 | 138334208 | 135092 |
| adapter/2 | 1 | 2746 | 170.469228 | 92.464615 | 262.933843 | 122843136 | 150069248 | 146552 |

Across these 60 warm calls per engine, observed individual durations range from 0.116626–5.979842 ms for the custom client and 0.390221–1.461232 ms for the adapter. The adapter reduces serialized schema size in this fixture but has higher cold latency and process RSS. No production latency, token count, browser performance or general speedup is inferred.

Post-switch adapter samples from the current harness:

| Sample | Schema bytes | Session create ms | Cold call ms | Create + first call ms | RSS before bytes | RSS after warm bytes | Peak RSS KiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 2746 | 227.518143 | 74.225872 | 301.744015 | 110927872 | 146378752 | 142948 |
| 1 | 2746 | 233.854368 | 74.799778 | 308.654146 | 110592000 | 145874944 | 142456 |
| 2 | 2746 | 236.527572 | 80.652520 | 317.180092 | 111566848 | 147054592 | 143608 |

The 60 observed post-switch warm calls range from 0.437673–1.279639 ms. The schema remains one proxy for this explicit `scriptMode: false` fixture. Normal profiles honor upstream's optional scripting setting.
