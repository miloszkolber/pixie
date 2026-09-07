# Security

Pixie is for one trusted user. Pi tools and configured MCP subprocesses run with the host user's permissions. Extensions add capabilities; Pixie does not manage tool permissions or execution policies.

The application mounts admitted project directories read-only. Every file read rechecks resolved paths and size limits. Pi performs host-side edits. Browser receives only its own state and artifacts. Containers run non-root with read-only roots, dropped capabilities and bounded resources.

Chromium runs with `--no-sandbox`. Browser sessions share a UID and filesystem, and host networking permits access to local services. Treat page content as untrusted.

| Boundary | Credential |
| --- | --- |
| Host SDK service | `PIXIE_PI_SECRET_KEY` shared with the application |
| Web UI | Optional `PIXIE_AUTH_ENABLED=true` and `PIXIE_TOKEN` |
| Browser MCP, HTTP and artifacts | `PIXIE_MCP_TOKEN` |
| Goals, questions and schedules MCP | Session-specific bearer token |
| Other MCP servers | Their own headers or subprocess environment |

Use distinct tokens and private environment/configuration files. Provider credentials pass to Pi and are excluded from replay and snapshots. MCP connection summaries omit commands, environment values and secret headers.

Remote Web UI access requires authentication unless explicitly overridden. Use HTTPS and an exact `PIXIE_PUBLIC_ORIGIN`. Remote MCP binding requires authentication and its own `PIXIE_MCP_PUBLIC_ORIGIN`.

Interactive App HTML runs in a nested iframe on the Browser origin with bounded CSP and browser permissions. It receives no service credentials. Tool and resource requests return to Pixie for same-session checks. These iframe policies are separate from Pi tool behavior.

Session, agent-edit and schedule operations verify project ownership. Question replies are single-use. Schedule roots are checked again before dispatch; ambiguous restart claims pause rather than replaying work.
