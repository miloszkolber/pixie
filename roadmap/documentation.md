# Documentation

Owner G with the behavior owner. `docs/` describes shipped behavior. `roadmap/` contains implementation requirements and acceptance. Keep a single home per fact and do not present a planned build or module as installed functionality.

## Files and removal

Remove the superseded `docs/roadmap.md`, `docs/mcp-draft-canvas.md` and `docs/mcp-draft-openfig.md`. Do not replace them with redirect stubs or copy them into another archive. Their original source evidence remains accessible through commit-pinned links in sources.md; reviewed requirements live here.

Update root README/AGENTS and current documentation/code-comment references to those files. Search Markdown links, bare paths, relative links and anchors, not only the three filenames at repository root. A historical URL pinned to a commit where the original exists is valid and must not be rewritten to a missing live path.

Current setup/reference documents are not obsolete merely because a roadmap discusses their future implementation. Retain architecture, deployment, Pi integration, extension, MCP, security, development and protocol documentation while the product still needs them. Delete a current-state document only after its actual operational content has a maintained home and all inbound references are repaired.

| File | Responsibility |
| --- | --- |
| Root README | Purpose, current prerequisites and shortest correct setup; roadmap link |
| docs/deployment.md | Released installation choices, private config, diagnosis, service lifecycle, upgrade/remove; migration link |
| docs/architecture.md | Actual process/state owners and two compositions once shipped |
| docs/pi.md | Supported native behavior, installation selection and TUI handoff |
| docs/pi-protocol.md | Exact generated/verified host methods, framing/errors/capabilities; distinguish browser/native protocols |
| docs/pi-extensions.md | Native resources, saved-versus-loaded state and real UI translation limits |
| docs/mcp.md | Module endpoints, configuration, availability and credential scope |
| docs/security.md | Enforced controls, assumptions and residual risks for each actual deployment |
| docs/development.md | Actual build/test commands and reproducible fixtures |
| AGENTS.md | Paths, read order, invariants, test ownership and approval boundaries |
| roadmap/execution.md | Only delivery-status ledger |

## Writing rules

Use short factual paragraphs and direct examples. Describe constraints needed to operate correctly, not obvious statements or a history of previous implementations. Comments explain non-obvious reasons or invariants, not the following line of code. Do not turn a local debugging detail, library workaround or benchmark machine into product direction.

Remove personal machine paths, dated backups, release history, completed migration narratives, one-off timings, verified-from-code annotations and repeated architecture from user guides. Keep useful investigation evidence in the review and test reports, not installation prose.

Retain important limitations: Pi runs with host-user authority; independent TUI/Web writers need separate sessions or idle handoff; MCP is optional; Browser's same-UID setup is not isolation; uncertain dispatch is not automatically repeated; Canvas requires enforced renderer containment; Design has an instance-wide slot; an embedded .fig cover is not frame rendering.

Do not call a TUI workaround feature parity. Explain unsupported installations clearly while the supported managed profile remains covered by compatibility.md. Avoid unqualified claims of any Pi version, arbitrary TUI live attachment, sandboxing or renderer fidelity.

## Build/release documentation

Once implemented, explain exactly two host choices: pixie-assistant for the Docker interface, or one complete host pixie binary/service. Pi itself and optional worker runtimes are separate prerequisites, not a second hidden Pixie installation.

Use only the commit-derived identity in builds-and-releases.md for GitHub Releases, archives, binary versions and GHCR. Examples label release IDs as examples, not existing downloadable releases. Pin tested image digests for unattended setup. Do not retain a contradictory semver, latest or npm-based default for the new builds.

Document configuration precedence once and normalize legacy env compatibility there. Run examples with private synthetic credentials. Do not read an entire dotenv file as an authentication token. Prebuilt Compose examples use pull/up without --build unless the service actually has a build configuration.

Keep current source paths assistant/ and package/; remove obsolete pi/ and nested pixie/ roots. Reconcile actual mounts and Browser state roots. Do not claim that final-image tini, resource flags, shared filesystem isolation or App HTML behavior exists without checking the deployed entrypoint/configuration.

## Acceptance

DOC-02 removes all three superseded plans and repairs live inbound links. Link checks distinguish fixed historical evidence from live paths. Every normal documentation page has a factual owner and short purpose. Both installation/service/configuration examples, mode switching, upgrade/rollback and uninstall are tested in disposable environments after implementation.

Documentation-only verification is link/path/content/diff checking, not a runtime test claim. Native bridge, worker and renderer limitations remain explicit named gates until actual evidence exists. Do not make user documentation say the roadmap is complete simply because its Markdown files are comprehensive.
