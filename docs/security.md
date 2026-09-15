# Security

Pixie is for one trusted user. Pi tools and configured MCP subprocesses run with the host user's permissions. Extensions add capabilities; Pixie does not manage tool permissions or execution policies.

`pixie_web` is controller-only and never contains or starts Pi, Bun or Node. `pixie_cli` and `pixie` bundle the regular native Pi TUI, SDK, pinned Bun `1.4.0` runtime and internal host; `pixie_assistant` is archive-internal only. Pi RPC is excluded and Pi self-update is blocked. Source and image checks do not establish a published release or completed Docker deployment.

**Trust model.** The application mounts admitted project directories read-only. Every file read rechecks resolved paths and size limits. Pi performs host-side edits. Git inspection is read-only and staged-only: helpers run with a sanitized environment (no system/global config, ceiling directories, disabled hooks and external diff), submodule `.git` gitfiles stay contained to admitted metadata, raw worktree comparison is bounded at 4 MiB per file and 64 MiB aggregate with conservative reporting, previews state that clean/process and LFS conversion are not applied, and helpers terminate through process-group TERM/KILL with bounded pipe draining. Deployments run non-root with read-only roots, dropped capabilities and bounded resources only when launched with the documented runtime flags; these are defense in depth, not isolation from controller files.

**Container boundary.** The application image runs as UID 1000 with a read-only root filesystem, no added capabilities and resource limits when Compose supplies them (`web/Dockerfile`, `docker-compose.yaml`, verified-from-code). This is not a sandbox between the controller and its neighbors, and the image definition is not completed Docker deployment evidence. Compose uses host networking, and processes that share a UID retain full discretionary-access rights and signaling, so same-UID processes are not a sandbox for one another and a separate directory or mount is a convention rather than filesystem isolation.

**Browser MCP endpoint.** Pixie hosts no browser. Pi is the MCP client and connects directly to an operator-chosen external browser MCP endpoint. Pixie stores one setting in `config.json`, registers it in Pi's effective `mcpServers` configuration and reports a bounded endpoint probe (`web/internal/controller/mcp_browser.go`, covered by `web/tests/go/controller/browser_mcp_test.go`). Pixie never proxies MCP traffic and never fetches or renders page content. Because the endpoint is operator-chosen, it may be unauthenticated: the deployment owns endpoint hardening, authentication, network isolation, and egress control. The supplied Compose file defines no browser service; the operator may point Pi at a separately managed endpoint. When the setting is disabled, Pi administration is unavailable, or the host call or probe fails, the registration surface fails closed, reports the error, and claims no registration.

**Untrusted page content.** Pixie does not fetch, render or contain pages. Browser tool results reach Pi and the model through the external server; treat page content, snapshots and tool output as untrusted data, never instructions. A successful probe or tool call is a liveness signal, not containment evidence.

## Browser MCP threat model

Pixie stores the browser endpoint as one setting, writes only its own entry into Pi's effective `mcpServers` configuration, and reports a bounded probe. Pi then connects to the endpoint directly. The endpoint, its browser runtime and their host access sit outside Pixie's process and trust boundary, so the threats below follow from that split; each states the operator's control and the limit on what Pixie can do.

**Loopback versus network exposure.** A loopback endpoint is reachable by every local process and by browser pages that can reach loopback; a network-bound endpoint is reachable by every peer that can route to it. Binding `127.0.0.1` or `::1` narrows the reachable set but does not make the endpoint trusted, and binding `0.0.0.0`, a LAN address or a public address exposes the full tool surface. Pixie neither binds the endpoint nor enforces its listen address; only the operator controls this.

**DNS rebinding and origin confusion.** A page can resolve an attacker-controlled name to loopback and send requests to a loopback endpoint, and a plain HTTP MCP endpoint that does not validate `Host` and `Origin` cannot distinguish that page from Pi. The endpoint then acts as a confused deputy that drives browser, file or shell tools for the page. Pixie sends its own probe requests and cannot make the endpoint validate origin, and it cannot inspect the traffic Pi sends. Only the operator's `Host`/`Origin` checks or network placement prevent this.

**Unauthenticated or weakly authenticated endpoints.** The setting accepts any `http(s)` URL without embedded credentials and Pi sends the operator's configured headers to the endpoint; Pixie injects no Pixie credential. An endpoint with no authentication, a shared or default token, or a token visible to pages admits any caller that can reach it. Pixie does not audit, rotate or require endpoint authentication, so the operator owns the credential and its scope.

**Tool-result prompt injection reaching the model.** Page text, DOM and accessibility snapshots, console output, downloads and other tool results travel through the endpoint into Pi's model context. That content is attacker-controlled data and can contain instructions; Pi runs with the host user's authority and can execute tools, so a successful injection can read or change files, reach the network or run commands. Pixie does not intercept, filter or rewrite Pi tools or prompts. The operator must minimize the exposed tools, treat tool output as untrusted data, and review consequential model actions.

**Egress from tools.** Browser navigation, fetch tools and file tools reach arbitrary hosts, including loopback services such as Pi and the controller and other machines on the host network; the endpoint process can also make its own outbound connections. Pixie does not restrict endpoint egress. The operator must network-isolate the endpoint and allow only the destinations the task requires.

**Socket and file access.** The endpoint and its browser runtime hold host filesystem and IPC access: profile and download directories, screenshots and other artifacts, lock files, and DevTools or control sockets that other local processes may connect to. Pixie does not scope, mount or monitor endpoint file or socket access. A separate user, container or host reduces the reachable set, but it is a boundary only when the operator configures and verifies it.

**Crash cleanup.** Pixie does not own or supervise the endpoint process. After a crash or forced stop, locks, sockets, profile directories, downloads and partial files can remain, and the next start can reuse stale state. Pixie reports a failed probe but performs no cleanup. The operator must give the endpoint its own restart, state-directory and cleanup policy.

**External writer racing filesystem replacement.** Pixie writes its own MCP entry through an in-process mutation chain and validate-first atomic replacement, and it refuses to mutate when another configuration layer collides or cannot be inspected. That serializes only Pixie's own writes: another process that edits Pi's `mcp.json` can interleave with Pixie's read-modify-replace, so one writer's change can be lost or a rename can win the race. Pixie cannot coordinate an arbitrary external writer. The operator must keep one writer for that configuration or use an explicit idle handoff with the other owner terminated.

**No sandbox and fail-closed enablement.** Pixie provides no sandbox for the endpoint, the browser process behind it, or the tool results it returns. Enable the Browser contribution only when the operator enforces the whole boundary: bind the endpoint to loopback or an explicitly authenticated network boundary, require a strong credential, validate `Host` and `Origin`, expose only the tools the task needs, restrict egress, and isolate the endpoint from Pixie state, Pi credentials and admitted project mounts with its own crash and cleanup policy. Pixie deliberately cannot enforce any of these controls, and Docker hardening, a read-only root, dropped capabilities, a non-root user and host-local locks are defense in depth, not isolation from another process that shares the host or UID. When that boundary is missing, the Browser contribution should not be enabled.

**Credentials.**

| Boundary | Credential |
| --- | --- |
| Host assistant service | `PIXIE_PI_SECRET_KEY` shared with the application |
| Web UI | Optional `PIXIE_AUTH_ENABLED=true` and `PIXIE_TOKEN` |
| Pixie in-process MCP publisher | `PIXIE_MCP_TOKEN` |
| Goals, questions and schedules MCP | Session-specific bearer token |
| External browser MCP endpoint | The deployment's own credential, if any; Pixie injects no Pixie credential |
| Other MCP servers | Their own headers or subprocess environment |

Use distinct tokens and private environment/configuration files. Provider credentials pass to Pi and are excluded from replay and snapshots. MCP connection summaries omit commands, environment values and secret headers.

## Remote access

Remote Web UI access requires authentication unless explicitly overridden for a trusted network with firewall protection. Use HTTPS and an exact `PIXIE_PUBLIC_ORIGIN`. Direct cleartext requests from remote peers are rejected for HTTPS public origins. Controller-owned service calls may use cleartext HTTP only from an actual loopback peer with a literal `localhost`, `127.0.0.1` or `::1` Host at the configured listener port; this transport exception does not bypass route-specific bearer or session authorization. The documented unauthenticated LAN mode leaves `PIXIE_TRUSTED_PROXY_CIDRS` unset. A TLS-terminating proxy may use a cleartext backend only with controller authentication, a peer CIDR explicitly configured in `PIXIE_TRUSTED_PROXY_CIDRS`, a `Host` rewrite to the exact public authority, and `X-Forwarded-Proto: https`; untrusted `Forwarded`/`X-Forwarded-*` headers cannot change authority or transport. Pi always stays on loopback; only its port is configurable.

**Interactive App HTML.** Interactive App HTML runs in a nested iframe on the application origin with bounded CSP and browser permissions. It receives no service credentials. Tool and resource requests return to Pixie for same-session checks. These iframe policies are separate from Pi tool behavior.

**Session and ledger authorization.** Session, agent-edit and schedule operations verify project ownership. Question replies are single-use. Ledger saves publish through validate-first staging with typed outcomes: only installed commits may trigger dependent dispatches, durability-uncertain results keep the mutation identity and reconcile the validated primary without backup restore or replay, and deletions stay fail-closed. Schedule roots are checked again before dispatch; ambiguous restart claims pause rather than replaying work.

**External writers.** Pixie's host-local locks and mutation identities serialize only the state and assistant service it owns. They cannot coordinate arbitrary external writers, including a separate Pi process that writes the same native session; use separate sessions or an explicit idle handoff with the managed owner terminated.

## Measurements

Existing fixtures: `web/tests/performance/main.go` is a controller comparison harness (project.list, file_1MiB, PNG-over-HTTP workloads with p50/p95 and a 5% budget), not a browser harness; `web/tests/performance/transcript.ts` explicitly does not measure browser paint or deployment-host latency. Pixie hosts no browser, so a browser task-latency fixture is not applicable here; browser performance belongs to the external deployment.

`web/scripts/check-performance.ts` is the PERF-01 evidence checker. A complete record needs repeated fresh-process measurements for the archive-internal Pi host and `pixie_web` on amd64 and arm64, a named artifact/profile and full source commit, complete process-tree RSS, separate decoded/buffer memory, content-filled UI, and worker resource fields. The checker fails when the record is missing, partial, synthetic, or not marked as live; this checkout contains no such live record.

An earlier host measurement does not apply to the archive-internal Bun host: no process-tree RSS or readiness measurement is recorded in this checkout, and `check-performance` fails closed until live evidence is supplied for both binaries and architectures.

Fixture commands (run on the live deployment host, then record machine, CPU/RAM, image digest, controller revision and the measured command alongside each number):

```sh
# Startup: cold start to healthy.
/usr/bin/time -v docker compose up -d pixie
curl -sf http://127.0.0.1:7312/livez; curl -sf http://127.0.0.1:3284/livez
docker compose --profile full logs pixie --since 10m | grep -i -E 'ready|listen'
```
