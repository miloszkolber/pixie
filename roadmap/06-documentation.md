# 06 — Documentation

Owner: G with the relevant behavior owner. User documentation describes shipped behavior; roadmap files describe implementation requirements. Keep one home per fact.

| File | Responsibility |
| --- | --- |
| README.md | Purpose, prerequisites, a two-build choice and short start instructions |
| docs/deployment.md | Docker interface + assistant, or combined host binary; configure/run/diagnose/upgrade/rollback/remove |
| docs/pi.md | Native ownership, supported behavior, compatibility and TUI handoff |
| docs/pi-protocol.md | Exact schemas/methods/events/capabilities/errors/framing/versioning |
| docs/pi-extensions.md | Native extensions and Web UI translation limits |
| docs/mcp.md | Module endpoints/configuration/availability/scope/trust |
| docs/architecture.md | Shared engine, the two process compositions and state/lifecycle ownership |
| docs/security.md | Enforced controls, assumptions, credential roles and residual risks per deployment |
| roadmap/ | Unfinished outcomes, detailed plans, dependencies and evidence |
| AGENTS.md | Real paths, invariants, commands, ownership and approval boundaries |

Keep docs/roadmap.md and the old MCP draft paths as short links to their canonical plans. Do not duplicate the backlog or leave two competing module implementations.

## Editorial requirements

Use short factual paragraphs and direct examples. Comments explain non-obvious constraints, not the next line's obvious action. Remove operator machine paths, dated backup names, publication history, completed-migration narratives, repeated architecture, one-off measurements, verified-from-code annotations and obsolete paths. Do not move this debris into a new user-facing archive.

Do not remove constraints that prevent mistakes: native same-session concurrent writes are not coordinated; Pi has host-user authority; native MCP is optional; Browser's current isolation is limited; uncertain dispatch must not auto-retry; Canvas rendering requires enforced isolation; Design's slot is instance-wide and a saved thumbnail is not a frame render.

After the native-executable cutover, a suitable README opening is:

> Pixie is a web workspace for Pi. It uses your installed Pi, its configuration, and its native sessions. The interface combines conversations, read-only files and Git views, schedules, and optional workspace tools.
>
> Use pixie-assistant with the Dockerized interface, or run the complete assistant and interface on the host with pixie.

Do not claim any Pi version or live attachment to an arbitrary running TUI. State tested compatibility and useful failure behavior. Do not present planned modules as available features before acceptance.

## Installation examples

Document both released binaries as first-class paths, not “binary plus npm assistant” as the direct-host setup. Assistant-only needs its user service and the Docker controller. Combined needs only pixie.service and the installed native Pi, without a separate assistant service, web assets or frontend runtime.

Explain configuration/state differences, shared native Pi ownership and switching modes. All-in-one core does not mean Chromium, optional parser/render workers or Pi itself are embedded. Name those optional dependencies where enabled and distinguish each deployment's actual security boundary. Link to the one release matrix in [07-build-release.md](07-build-release.md).

## Concrete corrections

Reconcile deployment mounts with Compose and remove --build from prebuilt-only instructions. Replace the broken benchmark that reads a dotenv file as a token with a reproducible fixture outside installation prose. Remove references to absent pi/ and pixie/ source roots, retired App HTML behavior and unsupported sandbox claims. Reconcile legacy npm pack paths while that workflow remains supported. [Evidence](sources.md#documentation-and-validation).

Describe configuration precedence once and use private placeholder environment/config files. Generate exact protocol reference material from the canonical catalog; keep its introduction readable. Put audit findings in the review, not the quick start.

## Validation

Check relative Markdown links, anchors and documented repository paths in CI. Run setup, health, upgrade, topology-switch and rollback examples in disposable environments for both variants, including missing dependencies. Verify optional modules really are optional and example tokens never originate from live credentials.

For each paragraph ask whether it is true now, necessary here and maintained in one place. Update docs with the behavior change. A linter does not replace human review of product wording.
