#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
fixture=$(mktemp -d "${TMPDIR:-/tmp}/pixie-compose.XXXXXX")
trap 'rm -rf "$fixture"' EXIT HUP INT TERM

compose() {
	if [ -n "${COMPOSE_BIN:-}" ]; then "$COMPOSE_BIN" "$@"; else docker compose "$@"; fi
}

export PIXIE_DATA_PATH="$fixture/data"
export PIXIE_PI_SECRET_KEY=ci-pi-secret-0123456789abcdef0123456789
export PIXIE_TOKEN=ci-controller-token-0123456789abcdef0123456789
export PIXIE_AUTH_ENABLED=false PIXIE_CONTROLLER_HOST=127.0.0.1
export PIXIE_ALLOW_UNAUTHENTICATED_REMOTE=false PIXIE_PUBLIC_ORIGIN=
export PIXIE_MCP_TOKEN=ci-mcp-token-0123456789abcdef0123456789

compose --env-file /dev/null -f "$repo_root/docker-compose.yaml" config --format json > "$fixture/compose.json"
# .pixie is the configuration source when present but must stay optional:
# the render above already succeeded without one in the repo root.
test ! -e "$repo_root/.pixie"
grep -q -A2 "env_file:" "$repo_root/docker-compose.yaml"
grep -q "path: .pixie" "$repo_root/docker-compose.yaml"
grep -q "required: false" "$repo_root/docker-compose.yaml"
jq -e --arg root "$repo_root" --arg data "$PIXIE_DATA_PATH" '
  (.services | keys) == ["pixie"] and
  all(.services | to_entries[]; .key as $service | .value |
    .user == "1000:1000" and .read_only == true and .network_mode == "host" and
    (.volumes | length) == 1 and
    (all(.volumes[]; .type == "bind"))
  ) and
  .services.pixie.image == "ghcr.io/miloszkolber/pixie:latest" and
  (.services.mcp == null) and (.services.browser == null) and
  .services.pixie.volumes[0].source == $data and
  .services.pixie.volumes[0].target == "/var/lib/pixie" and
  .services.pixie.environment.PIXIE_PI_SECRET_KEY == env.PIXIE_PI_SECRET_KEY and
  .services.pixie.environment.PIXIE_MCP_TOKEN == env.PIXIE_MCP_TOKEN and
  (.services.pixie.environment.PIXIE_MCP_URL == null) and
  (.services.pixie.environment.PIXIE_MCP_PUBLIC_ORIGIN == null) and
  (.services.pixie.environment.PIXIE_MCP_HOST == null) and
  (.services.pixie.environment.PIXIE_MCP_PORT == null) and
  (.services.pixie.environment.PIXIE_MCP_AUTH == null) and
  (.services.pixie.environment.PIXIE_MCP_MODULES == null) and
  (.services.pixie.environment.PIXIE_BROWSER_TOKEN == null) and
  (.services.pixie.environment.PIXIE_BROWSER_AUTH == null) and
  any(.services.pixie.tmpfs[]; startswith("/dev/shm:size=256m"))
' "$fixture/compose.json" > /dev/null || {
	echo "Compose service isolation checks failed" >&2
	exit 1
}
test ! -e "$PIXIE_DATA_PATH"

if PIXIE_MCP_TOKEN= compose --env-file /dev/null -f "$repo_root/docker-compose.yaml" config --quiet > "$fixture/missing-token.log" 2>&1; then
	echo "Compose accepted a missing MCP credential" >&2
	exit 1
fi
echo "Compose service isolation checks passed"
