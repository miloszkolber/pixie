# Repository review

Reviewed baseline: `miloszkolber/pixie` at `f63d0d5bcb7f6e342058734b2e741ad2619d8867`, 8 September 2026. Inputs include the supplied UI screenshot/five wireframes, pi-web, Mewa and Pi RPC. [Source appendix](sources.md).

## Verification scope

Source review covered runtime entrypoints, most assistant session implementation, host transport, controller connection/queues, MCP registry, shell/state/styles/adapters, representative acceptance tests, deployment, documentation and CI. It is not an independent line-by-line audit of every file.

The reviewed commit's Actions run `34226327666` reported successful source, browser, amd64/arm64 native-host and image-publication jobs. Those results were inspected; the application/tests were not independently run during the review. The supplied empty-content state was not reproduced. Security observations are source findings, not demonstrated exploits. [CI evidence](sources.md#documentation-and-validation).

P1 means resolve or explicitly accept before the relevant cutover; P2 belongs in foundation work. These are planning priorities, not security severity scores. Confirmed means evident in inspected source; gap means a target requirement is missing; risk requires runtime/compatibility evidence. Recheck each finding against the actual checkout.

## Retain

The Go controller boundary, native session identity/persistence, bounded transport/replay, authenticated loopback connection, scoped UI requests, ambiguous-delivery state, read-only inspection, Mewa integrity and substantial tests are useful foundations. Browser acceptance already checks streaming after view closure, reconnect deduplication, keyboard/narrow layouts and Mewa cleanup. Keep those behaviors while replacing ownership and layout; a green old suite does not establish the new architecture.

## Findings

### F01 — Installed Pi is not the runtime authority

**P1, confirmed architecture mismatch.** The assistant installs pinned SDK dependencies and directly creates SDK sessions; its server reports that embedded version. Sharing the user's agent directory is not selecting their installed executable/runtime. SDK embedding is not inherently wrong, but differs from the requested native-installation/Go direction.

Launch the selected native `pi` and test outside the checkout. Both release variants must use the same implementation and actual installation. [A1–A4](sources.md#assistant-and-native-pi).

### F02 — Production restart has no termination callback

**P1, confirmed defect.** `runtime.restart` closes peers and calls optional `options.onRestart?.()`. Production `main.ts` supplies no callback, and the server installs no default despite its interface comment. The test injects a callback.

An enabled restart can acknowledge/disconnect without exiting; `restartPending` stays set. Use bounded shutdown and executable-level exit/new-boot tests. In the full-host build the composition root must drain and restart the complete service, not exit from a library. [A3–A5](sources.md#assistant-and-native-pi).

### F03 — Failed module persistence changes live enablement

**P1, confirmed defect.** `Registry.SetEnabled` updates `r.enabled` before `persist.Write`, then returns on failure without restoring the map. Catalog/health/routes read that map while disk/runtime may retain prior state.

Persist a candidate before publication and distinguish desired state from readiness. Test failed writes, effective availability and subsequent restart. [E1](sources.md#extensions-and-deployment).

### F04 — Repeated enable restarts Browser

**P1, confirmed defect.** `SetEnabled` always calls `startLocked`; its enabled path constructs a new service and shuts down the previous one. There is no unchanged-value guard.

Make duplicate enable a no-op and Restart explicit. Verify active handles survive retries and shared-storage startup/shutdown order is safe. [E1](sources.md#extensions-and-deployment).

### F05 — Method reference and validation are not an exact contract

**P1, confirmed gap.** The protocol document lists names/families inconsistent with the host dispatcher; actual examples include `session.cancel`, `pi.session.steer` and `session.configure`. The server coerces IDs with `Number(request.id)` before validation, admitting some strings/booleans under a numeric-ID contract.

Inventory browser/controller/host methods separately, validate strict envelopes and generate exact reference details from a shared schema. Preserve forward-compatible native payload fields. [Host](sources.md#assistant-and-native-pi), [documentation](sources.md#documentation-and-validation).

### F06 — One content slot owns both sides of navigation

**P1, confirmed design gap.** `project-work-area.svelte` selects one ContentTab for chat/file/diff/Browser, so a file replaces a conversation. The right area is hard-coded to Files/Changes.

Introduce independent primary/secondary selections and six slots. Remove the mixed-content tab strip as the shell model; styling cannot correct ownership. [U1–U3](sources.md#workspace-and-mewa).

### F07 — Create guard and screenshot diagnosis are separate

**P1, missing guard confirmed; screenshot cause unverified.** The create handler has no local in-flight guard, so repeated activation can issue repeated requests. That does not prove repeated Chat labels in the screenshot are accidental.

The screenshot also shows chat tabs with a project-ready empty panel. `openChatSession` normally activates its result, so “activation is never set” is not a supported diagnosis. Add a guarded action and a fixture capturing route/selected IDs/restoration/request generation. Selected items always resolve to their own loading/error/content state. [U2–U3](sources.md#workspace-and-mewa).

### F08 — Two visual foundation systems coexist

**P1, confirmed ownership gap.** Mewa styles coexist with independent Pixie palette/color/typography generators and rounded/spacing tokens. Each subsystem claims single ownership internally, while the application has both. Some wrappers, including Button, already match Mewa and should stay.

No computed-style audit was run, so do not blame a specific cascade collision without evidence. Map consumers to one Mewa foundation, limit Pixie tokens to product geometry and test composed styles/lifecycle. [U4–U9](sources.md#workspace-and-mewa).

### F09 — Availability replaces the workspace

**P2, confirmed UX gap.** Global provider/connection gating in `shell.svelte` replaces the workspace and Settings is modal. Missing agent functionality should not remove navigation/diagnostics.

Keep shell availability separate from action availability; mark stale readable content and disable live actions appropriately. Move Settings into the primary area model. [U1](sources.md#workspace-and-mewa).

### F10 — Native editor-text parity is missing

**P2, confirmed capability gap.** The custom bridge marks editor/composer APIs unsupported, while inspected native RPC includes `set_editor_text`. Existing dialogs/string widgets remain useful.

Map supported requests with originating session/process and draft-conflict semantics. Native confirmation uses `confirmed`; arbitrary TUI factories are not browser components. [A6–A7](sources.md#assistant-and-native-pi).

### F11 — Module code is Browser-specific

**P2, confirmed design gap.** The backend accepts only Browser in validation, health, lifecycle and catalog, and UI contains Browser-specific integration.

Add one bounded contribution boundary using Browser plus fixtures, then Canvas/Design. Separate generic MCP data from rich local adapters. No remote-code marketplace or growing shell branches by module name. [E1](sources.md#extensions-and-deployment), [U2](sources.md#workspace-and-mewa).

### F12 — Browser shares controller security context

**P1, confirmed exposure; no exploit demonstrated.** Browser/controller share UID 1000 and host networking; Chromium sandbox is disabled; Browser roots sit below controller data. HOME/TMP/environment filtering does not prevent same-UID access to other permitted files or local services.

Document actual boundaries and test stronger worker isolation end to end. Native Pi's intentional user authority is different from untrusted page authority. Full-host mode makes unrestricted worker access especially consequential; do not inherit this posture silently. [E1–E4](sources.md#extensions-and-deployment).

### F13 — Hardening claims exceed supplied Compose flags

**P2, confirmed mismatch.** Compose has non-root/read-only/tmpfs but lacks capability-drop, no-new-privileges, memory and PID limits discussed around it.

Add/test intended flags or remove the claim. Keep host Pi tool behavior separate from Browser restrictions. Runtime flags, not image comments, determine deployed limits. [E2–E4](sources.md#extensions-and-deployment).

### F14 — Routine validation can publish

**P1, confirmed workflow behavior.** Non-PR events including main pushes and scheduled runs publish images and may promote latest, while prose describes exact-source approval. The workflow does not express the same separate approval step; protections outside it were not comprehensively audited.

Separate validation from publication and enforce the approved release set. Every new-pipeline release includes assistant-only and full-host builds on each supported architecture. Keep previous releases immutable. [D1–D2](sources.md#documentation-and-validation).

### F15 — Compatibility jobs do not prove independent-installation parity

**P1, confirmed test gap.** Existing official-Pi jobs install the workspace and test its SDK host on two architectures. This is not an independently installed Pi with different PATH/version/extensions under the final service artifact.

Retain those tests and add independent installations, optional packages absent, custom directories, incompatible versions, separate TUI sessions and final artifacts. Both required deployment variants need the matrix. [D1, D4](sources.md#documentation-and-validation).

### F16 — History is chunked after eager materialization

**P2, confirmed optimization opportunity, not observed memory failure.** Large history is split for transport, but snapshot/history is assembled and the entire message array serialized to test its size before chunking. Large entries and concurrent attaches remain important cases.

Use bounded iteration/incremental encoding while preserving checkpoint ordering. Measure cold/warm attaches with images/custom entries and total Pi-child memory. Do not claim all history is forced into one frame. [A2–A3](sources.md#assistant-and-native-pi).

### F17 — Durable/native queue ownership is a porting hazard

**P1, confirmed dual-boundary concern, not a claim of current duplicate execution.** The controller retains durable follow-ups and uncertainty; native RPC also has steering/follow-up queues. A naive port could admit one item to both or treat acceptance as completion.

One owner per delivery stage. Preserve uncertainty after lost acknowledgment and test exact Stop behavior. Do not promise exactly-once execution across process failure or deployment-mode switching. [A7](sources.md#assistant-and-native-pi), [C2](sources.md#controller).

### F18 — Documentation contains obsolete paths and operational history

**P2, confirmed defects.** Docs/agent instructions reference absent paths, mix completed milestones with remaining work, include host-specific paths/backups and contradictory mount/isolation claims. Deployment describes a Browser mount absent from Compose and uses build for a service without a build definition. Security's benchmark reads an entire dotenv file as a bearer and describes interactive App HTML despite roadmap exclusions.

Rewrite by responsibility; test examples in disposable environments. Keep audit evidence here, not quick-start prose. Update root agent instructions early. The former non-Docker two-process package instructions must become the required full-host single-binary installation when it ships. [D3–D7](sources.md#documentation-and-validation).

## Limitations and sequence

Separate Pi processes do not coordinate same-session writes; a Pixie-only lock cannot compel vanilla TUI cooperation. Native RPC exposes less administration than direct SDK embedding, so every retained feature needs a disposition rather than an automatic syntax port. An empty-shell screenshot or green old suite does not establish the new content-filled design.

Fix F02–F05 and isolate F07 first. In parallel agree contracts and inventory Mewa. Build one independent native conversation and one Chat+File split, then continuity/optional integration and both build variants. Complete deployment/docs before final Canvas and Openfig integration. Preserve one owner per concern throughout.
