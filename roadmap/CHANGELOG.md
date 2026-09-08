# Pixie roadmap changelog

This file records roadmap work that has shipped in the checkout. [execution.md](execution.md) remains the status ledger. A completed entry names the implementation commit, observed behavior, verification commands and remaining boundaries.

## 2026-09-08

### FIX-01 — Production assistant restart

- **Implementation:** `6425e70` wires the production assistant entrypoint to the restart lifecycle, adds a fresh `bootId` beside stable `runtimeId`, blocks new work as soon as restart is accepted, and requires an executable termination hook. The entrypoint drains through `host.close()` and exits with status `75` for service-manager restart, with a 25-second bounded escalation. Normal signals retain a clean exit on successful teardown and report forced or failed teardown as an error.
- **Behavior evidence:** the process regression starts the real `assistant/src/main.ts`, restarts it on the same port, verifies the reply-before-exit path, stable runtime identity, changed boot identity, changed PID and persisted native session discovery after the new process starts.
- **Verification:** `bun test tests/pixie-assistant/runtime-restart.test.ts` (4 pass, Bun 1.3.14); `bun test tests/pixie-assistant/protocol-conformance.test.ts` (5 pass, Bun 1.3.14); `bun run typecheck`; `bun run --cwd assistant typecheck`; `CGO_ENABLED=0 go test -count=1 ./...`.
- **Remaining boundary:** combined full-host restart, systemd unit behavior, managed descendant reaping and final-container drain remain `BUILD-05` evidence. This change does not publish artifacts or change deployment state.
