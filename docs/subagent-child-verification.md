# Subagent child launch patch

`@mjakl/pi-subagent@3.0.1` needs an explicit Pi script when its parent runs under Bun. Its upstream runner supplies a script only for an executable named `node`. Under Bun it launches `bun --mode rpc`, which fails with `Script not found "rpc"` before starting Pi. A successful unknown-agent rejection does not exercise this path.

The seven-line local addition in `runner.ts` resolves `@earendil-works/pi-coding-agent/rpc-entry` through the installed SDK's public export and converts its file URL to a filesystem path. Pi 0.85.1 exposes `/rpc-entry`, not `/rpc`. The existing Node and native-executable branches, argument construction, inherited environment, settings discovery, RPC handling and process cancellation stay unchanged. No SDK modification, Pixie entry-point reuse or custom child runtime is involved.

Bun applies [`@mjakl%2Fpi-subagent@3.0.1.patch`](../agent/extensions/local-patches/@mjakl%252Fpi-subagent@3.0.1.patch) through the root `patchedDependencies` and `bun.lock`.

## Verification

Run from `pixie/` with Bun 1.4.0:

```sh
bun install --frozen-lockfile --ignore-scripts
bun test package/tests/pi-native-parity/subagent-child.test.ts package/tests/pi-native-parity/subagent-parity.test.ts
bun package/tests/pi-native-parity/subagent-patch-install.ts
```

The real-child regression discovers a known agent from isolated native agent state and calls the upstream runner. A file extension loaded by the child's `settings.json` registers a deterministic, in-process model provider. It makes no network requests and needs no external credentials. The fixture checks fresh output and usage, named-session history across two distinct child invocations, cancellation after a real provider turn starts, and disappearance of the cancelled PID. A native extension confirmation is cancelled through the upstream headless RPC response path. The child records its actual PID, Bun version, selected agent directory and argv for assertions.

The fresh child's asserted launch vector is:

```text
<process.execPath: bun>
<resolved @earendil-works/pi-coding-agent/rpc-entry: dist/bundle/rpc-entry.js>
--mode rpc --no-session --model child-fixture/echo --no-tools
```

Named children replace `--no-session` with `--session-dir <isolated agentDir>/sessions --session-id <UUID>`, adding `--name topic` on creation only. The child receives `PI_CODING_AGENT_DIR` through the unchanged inherited environment. Prompts use native RPC stdin, not argv.

Local verification on Bun 1.4.0 passes 15 focused tests with 69 assertions, the tests TypeScript check, focused Biome checks and the existing-worktree frozen install. The same known-agent regression against an unpatched installed runner fails with `Script not found "rpc"`. The clean production frozen-install script copies the manifests, lockfile, patches and child fixtures into disposable state and runs the real-child test there. Its install step is currently blocked in the verification container by `ENOSPC`, including with Bun's lower-space symlink backend. Clean-install execution remains a follow-up on a filesystem with sufficient capacity. The script removes its temporary state on success or failure.

## Upstream proposal and support boundary

Propose the same Bun-specific public RPC entry resolution to [mjakl/pi-subagent](https://github.com/mjakl/pi-subagent), with the known-agent child regression rather than an unknown-agent smoke test. No submission is made by this change. Replace the local patch with an upstream release once that release passes the child tests.

The verified combination is pi-subagent 3.0.1, Pi 0.85.1 and Bun 1.4.0 on Linux. The patch recognizes executable names `bun` and `bun.exe`, matching the upstream style of Node detection. Renamed runtime executables, other SDK versions and Windows child termination are not verified. The Node branch still uses the parent's argv entry as upstream does. This patch does not claim to fix embedded Node hosts or change native binary behavior. Selected agent state and extension settings must already be available to the child through normal Pi configuration. These runner-level tests do not certify the Pixie host's selection or propagation of that state, nor the full host/controller subagent parity gate.
