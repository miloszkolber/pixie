# Security

Pixie is for one trusted user. Pi tools and configured MCP subprocesses run with the host user's permissions. Extensions add capabilities; Pixie does not manage tool permissions or execution policies.

The application mounts admitted project directories read-only. Every file read rechecks resolved paths and size limits. Pi performs host-side edits. Git inspection is read-only and staged-only: helpers run with a sanitized environment (no system/global config, ceiling directories, disabled hooks and external diff), submodule `.git` gitfiles stay contained to admitted metadata, raw worktree comparison is bounded at 4 MiB per file and 64 MiB aggregate with conservative reporting, previews state that clean/process and LFS conversion are not applied, and helpers terminate through process-group TERM/KILL with bounded pipe draining. The Browser service is handed only its own state and artifact directories (a convention, not filesystem isolation under the shared UID; see below). Deployments run non-root with read-only roots, dropped capabilities and bounded resources only when launched with the documented runtime flags; these are defense in depth, not isolation from controller files.

Chromium runs with `--no-sandbox` (`package/internal/browser/config.json:2`, verified-from-code). The merged deployment shares one container and UID 1000 between the controller and the Browser subprocess (`package/Dockerfile:196,120`, `docker-compose.yaml:7`, verified-from-code), and host networking permits access to local services (`docker-compose.yaml:6`, verified-from-code). Treat page content as untrusted.

## Browser security boundary (roadmap item 9, static review; no behavior change)

Deployed filesystem (verified-from-code unless noted): the image creates `/app`, `/home/pixie` and `/var/lib/pixie/browser/{artifacts,state}`, all owned by UID 1000 (`package/Dockerfile:118-120`). The in-process publisher overrides the image-level roots and stores Browser state under the controller data directory (`<dataDir>/browser/{artifacts,state}`, `package/internal/mcpserver/registry.go:400-403`). Browser children start with their working directory set to the session artifact directory (`package/internal/browser/service.go:850`) and a scoped `HOME`/`TMPDIR`/socket dir under the session state directory (`service.go:565-574,471-474`), but nothing enforces that scoping: under the shared UID the Chromium/agent-browser process can read (and where writable, write) any file the controller can, including `/app`, `/home/pixie`, sibling controller files under the data directory, and admitted project mounts. Read-only project mounts and separate state directories are conventions the service honors, not filesystem isolation. Assumed (not verified in CI here): production compose mounts the data directory writable (`docker-compose.yaml:24`) with `read_only: true` as a runtime flag (`docker-compose.yaml:8`); without that flag `/app` is owned and therefore writable by UID 1000.

UID story (verified-from-code): everything runs as `USER 1000:1000` (`package/Dockerfile:196`, `docker-compose.yaml:7`). This protects against accidental root privilege escalation and nothing else. It does not isolate Browser processes from controller files, from each other, or from admitted projects: same-UID processes share full discretionary-access rights and signaling. Operators who need UID-level isolation can run the Browser service separately and point `PIXIE_BROWSER_URL` at it instead of the in-process module (assumed: external-service split is documented intent, not exercised here).

Environment sanitizing (verified-from-code): `exec.Cmd` is given an explicit `Env` (`service.go:636,850`), which replaces the inherited environment wholesale, so controller secrets, provider credentials and `PIXIE_*` tokens are not passed through. What remains is a small scoped set (`service.go:565-574`): `PATH` (agent-browser directory only), `HOME`, `TMPDIR`, `XDG_*` under the session state dir, `AGENT_BROWSER_SOCKET_DIR`, `AGENT_BROWSER_CONTENT_BOUNDARIES=1` and `AGENT_BROWSER_MAX_OUTPUT=20000`, plus the fixed wrapper exports `PATH` and `AGENT_BROWSER_EXECUTABLE_PATH=/usr/bin/chromium` (`package/Dockerfile:129-137`). This is secret hygiene, not a sandbox: it says nothing about what the process can open, and a successful run proves nothing about containment.

Network posture: the Browser controller surface defaults to loopback (`127.0.0.1:8787`, `service.go:30-31`) and refuses non-loopback binds without authentication (`package/internal/browser/mcp.go:50-52`, verified-from-code); non-loopback also requires `PIXIE_BROWSER_PUBLIC_ORIGIN` plus auth for origin checks (`mcp.go:53-61`). The browser itself is not network-sandboxed: `open`/`read`/`a11y`/`vitals` accept any `http(s)` URL without credentials, including loopback addresses (`policy.go:96-105,219-220`, verified-from-code), and the deployment uses host networking (`docker-compose.yaml:6`), so pages and scripts can reach local services. Command restrictions are not a network sandbox (`package/internal/browser/guide.md:30`, verified-from-code).

Artifact access and cleanup (verified-from-code): artifacts are content-addressed only by the returned URL, never invented paths (`guide.md:43`); reads require the browser bearer when auth is enabled (`service.go:1246-1249`), reject symlinks and non-regular files, open with `O_NOFOLLOW` plus `SameFile` recheck, and serve with `no-store`/`nosniff` (`service.go:1123-1172`). `close` deletes the session's state and artifacts (`service.go:1000-1007,665-675`); cancellation, timeout, output overflow and abnormal termination close the affected session (`service.go:787-795,723-726`), state-quota overflow also removes it while artifact-quota rejection need not (`guide.md:47`); startup sweeps stale `.pixie-screenshot-*.tmp` files and orphan `.lock` files (`service.go:405-416,359-403`); only explicitly leased panels (`X-Pixie-Panel-Lease`, `b-<hex>` IDs) expire on a ticker, unmarked MCP/HTTP sessions never self-reclaim (`panel_leases.go:22-23,71-109`). Cancellation propagates: HTTP/MCP context cancellation terminates the process group (`SIGTERM`, then `SIGKILL` after 2 s, `service.go:601-632,875-879`), the MCP handler opts into cancellation propagation and re-links the detached SDK context to the HTTP lifetime (`mcp.go:198-199,217-222`), and `Shutdown` cancels all active commands (`service.go:1292-1304`). Covered deterministically with a fake agent-browser binary (`package/tests/go/browser/http_test.go:174-235`, `cleanup_test.go`, `panel_leases_test.go`).

Untrusted pages and project boundaries (verified-from-code): `file:`, `data:`, `javascript:`, `about:`, `chrome:` and `chrome-extension:` schemes are rejected and only credential-free `http(s)` is allowed (`policy.go:107-118`); page content, snapshots and tool output are documented as untrusted data, never instructions (`mcp.go:160`, `guide.md:5`); screenshots and content boundaries do not validate containment. A successful screenshot is a liveness signal, not a sandbox test. The browser package never receives project roots or Pi credentials (no `project` reference and no environment inheritance anywhere in `package/internal/browser`; working directory and env are always the session-scoped values above), so project-boundary enforcement is total mediation by the controller never handing projects to the module, not anything the module enforces on its own.

Corrected over-claims (this review): "Browser receives only its own state and artifacts" now reads as convention, not enforcement, under the shared UID. "Containers run non-root with read-only roots, dropped capabilities and bounded resources" now reads as properties of the documented runtime flags (`docker-compose.yaml:8`, `package/Dockerfile:161-164`), not of the image; they do not isolate the browser from controller files. No screenshot, env-sanitizing, or read-only-rootfs language is left implying sandbox equivalence.

Residual risks and smallest practical hardening (no privileged machinery): a compromised renderer (expected without the Chromium sandbox) inherits UID-1000 access to controller state, admitted projects (read-only mounts still readable, and data-dir siblings writable), and loopback services including Pi and the controller itself; malicious pages can also probe the host network. Cheapest mitigations within scope: keep `read_only: true` plus tmpfs mounts on every deployment; mount admitted projects read-only at the same absolute path and never add writable project binds; run with authentication enabled and distinct `PIXIE_MCP_TOKEN`/`PIXIE_TOKEN` values; prefer loopback controller binds and set `PIXIE_BROWSER_PUBLIC_ORIGIN` exactly when exposing beyond loopback; close browser sessions promptly and keep per-session/global artifact and state quotas tight; for stronger needs, run the Browser service in a separate container/UID and point `PIXIE_BROWSER_URL` at it.

## Measurements

Existing fixtures: `package/tests/performance/main.go` is a controller comparison harness (project.list, file_1MiB, PNG-over-HTTP workloads with p50/p95 and a 5% budget), not a Browser harness; `package/tests/performance/transcript.ts` explicitly does not measure browser paint or deployment-host latency. There is no Browser task-latency fixture in the tree.

| Configuration | Browser task latency, open+snapshot+close over `POST :7312/mcp/browser` (`tools/call browser_command`) |
| --- | --- |
| Merged `pixie` image, x86-64, Browser module enabled, measured live 2026-09-07 against `https://example.com` (agent-browser 0.34.0 per `package/Dockerfile`, Chromium bundled in image) | open 859 ms, snapshot 19 ms, close 263 ms (single round trip, wall clock, bearer-authenticated loopback) |
| Merged `pixie` image, arm64, Browser module enabled | UNVERIFIED |

Assistant service on the same host (2026-09-07, `pixie-assistant.service`, Pi SDK 0.85.1, `--llama`): `livez` 200 in ~13 ms loopback, authenticated `readyz` capability snapshot 200 in ~15 ms, resident set ~73 MB (peak 99 MB shortly after start).

Fixture commands (run on the live deployment host, then record machine, CPU/RAM, image digest, `agent-browser` version, Chromium version, and controller revision alongside each number):

```sh
# Startup: cold start to healthy, Browser ready.
/usr/bin/time -v docker compose up -d pixie
curl -sf http://127.0.0.1:7312/livez; curl -sf http://127.0.0.1:3284/livez
docker logs pixie --since 10m | grep -i -E 'browser|ready|listen'

# Browser task latency: one bounded open+snapshot+close round trip via the
# in-process module (MCP tools/call, bearer required when auth is enabled).
python3 -c "
import json, time, urllib.request
tok = open('/home/core/docker/pixie/.pixie').read()  # parse PIXIE_MCP_TOKEN
def call(args, i):
    body = json.dumps({'jsonrpc':'2.0','id':i,'method':'tools/call',
        'params':{'name':'browser_command','arguments':args}}).encode()
    req = urllib.request.Request('http://127.0.0.1:7312/mcp/browser', data=body,
        headers={'Content-Type':'application/json','Accept':'application/json, text/event-stream',
                 'Authorization':'Bearer '+tok})
    t = time.time(); urllib.request.urlopen(req, timeout=120).read(); return (time.time()-t)*1000
print('open', call({'session':'bench-1','command':'open','args':['https://example.com']}, 1))
print('snapshot', call({'session':'bench-1','command':'snapshot','args':[]}, 2))
print('close', call({'session':'bench-1','command':'close','args':[]}, 3))
```

| Boundary | Credential |
| --- | --- |
| Host SDK service | `PIXIE_PI_SECRET_KEY` shared with the application |
| Web UI | Optional `PIXIE_AUTH_ENABLED=true` and `PIXIE_TOKEN` |
| Browser MCP, HTTP and artifacts | `PIXIE_MCP_TOKEN` |
| Goals, questions and schedules MCP | Session-specific bearer token |
| Other MCP servers | Their own headers or subprocess environment |

Use distinct tokens and private environment/configuration files. Provider credentials pass to Pi and are excluded from replay and snapshots. MCP connection summaries omit commands, environment values and secret headers.

Remote Web UI access requires authentication unless explicitly overridden for a trusted network with firewall protection. Use HTTPS and an exact `PIXIE_PUBLIC_ORIGIN`. Direct cleartext requests from remote peers are rejected for HTTPS public origins. Controller-owned service calls may use cleartext HTTP only from an actual loopback peer with a literal `localhost`, `127.0.0.1` or `::1` Host at the configured listener port; this transport exception does not bypass route-specific bearer or session authorization. The documented unauthenticated LAN mode leaves `PIXIE_TRUSTED_PROXY_CIDRS` unset. A TLS-terminating proxy may use a cleartext backend only with controller authentication, a peer CIDR explicitly configured in `PIXIE_TRUSTED_PROXY_CIDRS`, a `Host` rewrite to the exact public authority, and `X-Forwarded-Proto: https`; untrusted `Forwarded`/`X-Forwarded-*` headers cannot change authority or transport. Pi always stays on loopback; only its port is configurable.

Interactive App HTML runs in a nested iframe on the Browser origin with bounded CSP and browser permissions. It receives no service credentials. Tool and resource requests return to Pixie for same-session checks. These iframe policies are separate from Pi tool behavior.

Session, agent-edit and schedule operations verify project ownership. Question replies are single-use. Ledger saves publish through validate-first staging with typed outcomes: only installed commits may trigger dependent dispatches, durability-uncertain results keep the mutation identity and reconcile the validated primary without backup restore or replay, and deletions stay fail-closed. Schedule roots are checked again before dispatch; ambiguous restart claims pause rather than replaying work.
