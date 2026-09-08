# 05 — Reliability, security and measurement

Owner: F with each production code owner. Start now and continue through every gate. Source risks are not proof of exploitation or measured performance failures. [Review](repository-review.md).

## Immediate regressions

FIX-01 launches the real assistant entrypoint with restart enabled and verifies actual exit/new boot identity, repeat requests and bounded shutdown. Carry the same assertion into the Go artifact/systemd fixture.

FIX-02 injects module-store failure before commit and verifies unchanged catalog, authorization, runtime and restart state. Repeated enable must preserve an active Browser session. Test concurrent toggles, persistence revision conflicts and complete-map retention.

FIX-03 guards one unresolved session-create action and separately reproduces selected-chat/project-empty state. Record route, IDs, restore schema and request generation. Test repeated clicks, slow create, navigation before completion, reconnect and deleted routes. Do not mark the screenshot's cause fixed without reproducing it or explicitly documenting that limitation.

FIX-04 rejects wrong-type IDs, malformed envelopes and oversized frames while maintaining documented valid behavior. Generate the method reference from actual schemas rather than removing methods to match stale prose.

## Recovery matrix

| Boundary | Required failures |
| --- | --- |
| Pi process | Startup/interpreter failure, malformed/large stdout, stalled stderr/stdin, exit during tool/dialog, stubborn descendants |
| Delivery | Disconnect before/after acceptance, uncertain acknowledgment, duplicate mutation, native queue plus durable outbox, Stop during continuation |
| Snapshot | Concurrent events, stale child epoch, pagination expiry, partial tools, hidden custom entries, huge image/custom record |
| Workspace | Stale route restoration, project/session switch during fetch, invalid saved selection, missing file, repeated view mount/unmount |
| Persistence | Permission/disk failure, interrupted rename, corrupt state, recovery without stale backup resurrection, retry after partial cleanup |
| Schedules | Concurrent mutation, clock/timezone cases, missed occurrences, no overlap, ambiguous restart claim, run-now/stop and persistence failure |
| Modules | Missing dependency, sibling toggle independence, disable during work, lease expiry, wrong-context read and late completion after deletion |

Retain existing safeguards and realistic tests. Use race tests for Go concurrency and native integration recordings plus live independent Pi fixtures. Do not claim a model's textual statement proves its tool result succeeded.

## Trust boundaries

Pi intentionally runs with its host user's tool permissions. Do not sandbox it accidentally with a service profile that removes required HOME/project access. The Web UI/controller still require service authentication, origin checks and validated resource ownership.

Files/Git remain bounded read-only inspection of admitted roots. Test symlink replacement, path traversal, oversized files, multiple repositories, missing mounts and revalidation before use. Discovery of a native session is not admission of arbitrary project paths.

Current Browser uses a shared UID/filesystem and host networking. Sanitized environment and read-only mounts are useful controls, not isolation. Match Compose flags to documented claims. Test capability-drop, no-new-privileges, memory/PID limits and tmpfs sizes before prescribing them. Do not claim protection from compromised renderer code merely from a screenshot or non-root UID.

Retain private/distinct credentials, loopback assistant transport, browser-Origin rejection, session-bound mutation checks, safe image MIME handling, no-follow artifact reads and redacted diagnostics. Test unauthenticated/wrong-role calls before expensive processing.

## Optional untrusted workers

Canvas renderer and Design parser are application-side workers, not host Pi processes. They need a real filesystem, egress, memory, CPU/PID/output and wall-time boundary before accepting untrusted payloads. A same-UID child or Node heap limit alone does not establish this.

Implement a narrow job transport: immutable bounded input, isolated output, job/generation identity, cancellation and typed bounded results. No arbitrary command/path/job-code API. Keep trusted supervisor and untrusted worker authority separate. Verify no access to controller state, native Pi data, project mounts, host services or sibling jobs.

Preferred Linux deployment is a separately provisioned unprivileged worker enclosure with no external network and only operation-scoped data access; use a validated OS sandbox or isolated worker container. Do not mount a Docker socket into the controller or make privileged container creation a model capability. An operator-provisioned worker endpoint must authenticate jobs and enforce the same limits. Pin the actual enforcement implementation during the feature spike; test its failures rather than assuming labels imply containment.

Core chat must not require this worker stack. If required isolation is unavailable, Canvas/Design processing is unavailable with a useful diagnostic; never silently fall back to unrestricted same-UID execution. Pure stored metadata and authenticated deletion remain accessible. Each module's detailed policy is in stages 08/09.

## Performance

Create repeatable fixtures for startup, catalog, cold/warm large-history attach, simultaneous sessions, streamed rendering, file/Git views and Browser open/snapshot/close. Record machine/architecture, OS, Pi/runtime versions, image digest/revision, payload and repeated p50/p95 samples.

Measure controller, Go supervisor, all Pi children and Browser/optional workers separately and in total. Record idle/active RSS, peak memory, CPU, artifact/image footprint and response bytes. Existing single-run measurements are smoke observations, not budgets or arm64 results. Avoid unsubstantiated claims that a Go supervisor necessarily lowers total cost.

Use profiling to justify polling consolidation, history iteration, projection eviction, lazy viewers and highlight caching. Keep active work/pending dialogs out of idle eviction. Set regression budgets from a reproducible baseline, not arbitrary percentages copied without equivalent fixtures.

## Done

K5 requires the core recovery matrix, artifact tests and truthful deployment documentation. A stronger Browser profile is optional until implemented/tested; mandatory worker isolation for Canvas/Design is not waived by that core limitation. K6/K7 add hostile render/parse, output and deletion tests. Report tests actually executed separately from remaining test cases.
