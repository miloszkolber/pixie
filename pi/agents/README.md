# Agent definitions

The overlay does not install, replace or synchronize agent definitions. Existing user definitions remain in `<agentDir>/agents/*.md`, and project definitions remain in `<project>/.pi/agents/*.md`. Pi and the upstream subagent extension retain ownership of project trust and discovery precedence.

Create definitions through Pixie's agent editor or as Markdown with `name` and `description` frontmatter and task instructions in the body. Optional model and execution frontmatter are described in the [extension contract](../../docs/pi-extensions.md). No provider, model, tool policy or trust decision is supplied by Pixie tooling.

Pi-facing tests stay under [`pixie/tests/`](../../pixie/tests/).
