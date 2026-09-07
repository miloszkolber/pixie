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
export PIXIE_MCP_URL=http://127.0.0.1:8787 PIXIE_MCP_PUBLIC_ORIGIN=
export PIXIE_MCP_AUTH=true PIXIE_MCP_HOST=127.0.0.1 PIXIE_MCP_PORT=8787

compose --env-file /dev/null -f "$repo_root/docker-compose.yaml" config --format json > "$fixture/compose.json"
# Compose versions omit different boolean defaults; compare against the same parser's explicit false.
jq '.services[].volumes[].bind.create_host_path = false' "$fixture/compose.json" |
	compose --env-file /dev/null -f - config --format json > "$fixture/safe-binds.json"
jq -e --arg root "$repo_root" --arg data "$PIXIE_DATA_PATH" --slurpfile safe "$fixture/safe-binds.json" '
  (.services | keys) == ["mcp", "pixie"] and
  all(.services | to_entries[]; .key as $service | .value |
    .user == "1000:1000" and .read_only == true and .network_mode == "host" and
    .cap_drop == ["ALL"] and .security_opt == ["no-new-privileges:true"] and
    (.mem_limit | tonumber) > 0 and (.cpus | tonumber) > 0 and .pids_limit > 0 and
    .build.context == $root and .build.dockerfile == "pixie/Dockerfile" and
    .logging.driver == "local" and .logging.options."max-size" == "10m" and
    .logging.options."max-file" == "3" and
    (has("env_file") | not) and
    (.volumes | length) == 1 and
    .volumes[0].type == "bind" and (.volumes[0].bind | type) == "object" and
    .volumes[0].bind.create_host_path == $safe[0].services[$service].volumes[0].bind.create_host_path
  ) and
  .services.pixie.build.target == "pixie" and
  .services.mcp.build.target == "mcp" and
  .services.mcp.container_name == "pixie-mcp" and
  .services.mcp.image == "ghcr.io/miloszkolber/pixie-mcp:latest" and
  (.services.browser == null) and
  (.services.pixie.mem_limit | tonumber) == 1073741824 and
  (.services.pixie.cpus | tonumber) == 2 and
  .services.pixie.pids_limit == 256 and
  (.services.mcp.mem_limit | tonumber) == 2147483648 and
  (.services.mcp.cpus | tonumber) == 2 and
  .services.mcp.pids_limit == 512 and
  .services.pixie.volumes[0].source == ($data + "/app") and
  .services.pixie.volumes[0].target == "/var/lib/pixie" and
  .services.mcp.volumes[0].source == ($data + "/browser") and
  .services.mcp.volumes[0].target == "/var/lib/pixie-browser" and
  .services.pixie.environment.PIXIE_PI_SECRET_KEY == env.PIXIE_PI_SECRET_KEY and
  .services.pixie.environment.PIXIE_MCP_URL == env.PIXIE_MCP_URL and
  .services.pixie.environment.PIXIE_MCP_TOKEN == env.PIXIE_MCP_TOKEN and
  .services.pixie.environment.PIXIE_MCP_PUBLIC_ORIGIN == env.PIXIE_MCP_PUBLIC_ORIGIN and
  (.services.pixie.environment.PIXIE_BROWSER_TOKEN == null) and
  (.services.pixie.environment.PIXIE_BROWSER_AUTH == null) and
  (.services.mcp.environment | keys) == [
    "PIXIE_MCP_AUTH", "PIXIE_MCP_DISABLED_MODULES", "PIXIE_MCP_HOST",
    "PIXIE_MCP_MODULES", "PIXIE_MCP_PORT", "PIXIE_MCP_PUBLIC_ORIGIN",
    "PIXIE_MCP_TOKEN"
  ] and
  .services.mcp.environment.PIXIE_MCP_AUTH == "true" and
  .services.mcp.environment.PIXIE_MCP_TOKEN == env.PIXIE_MCP_TOKEN and
  any(.services.mcp.tmpfs[]; startswith("/dev/shm:size=256m"))
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
