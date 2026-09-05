# Roadmap

- Additional native Pi extension UI interfaces.
- A dedicated schedules interface.
- Deployment-host performance measurements on both supported Linux architectures.
- Pi-native simplification parity gate: remove the custom `plans` (`update_plan`, `plans.read`, `pixie-plan`, `pixie:plan`) and `web` (`web_fetch`) host extensions once the `rpiv-todo`/`rpiv-web` profiles demonstrate equivalent plan display, search/fetch behavior, SSRF posture, session switching, and reload/compaction recovery.
- Pi-native question parity gate: remove Pixie's application-level `ask_user_question` tool once the `rpiv-ask` profile demonstrates equivalent answers, cancellation and error shapes, session-scoped single-use dialogs with no cross-session answers, timeout and abort handling, session reload recovery, and question presentation for option, custom and multi-select answers. Until then the application-level tool stays; enabling `rpiv-ask` before the gate passes surfaces both tools, so use it only for parity evaluation.

Current behavior is documented in the [README](../README.md).
