#!/bin/sh
set -eu

session=pixie-history-measurement
fixture_pid=
browser() { agent-browser --session "$session" "$@"; }
cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [ "$status" -ne 0 ]; then
		browser screenshot /artifacts/history-failure.png >/dev/null 2>&1 || true
		browser snapshot -i > /artifacts/history-failure.txt 2>&1 || true
	fi
	browser close >/dev/null 2>&1 || true
	if [ -n "$fixture_pid" ]; then
		kill -TERM "$fixture_pid" >/dev/null 2>&1 || true
		wait "$fixture_pid" >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p /artifacts
rm -f /tmp/pixie-ui-ready
export PIXIE_UI_HISTORY_ROUNDS="${PIXIE_UI_HISTORY_ROUNDS:-1000}"
/app/pixie-ui-fixture > /artifacts/history-fixture.log 2>&1 &
fixture_pid=$!
attempt=0
while [ ! -s /tmp/pixie-ui-ready ]; do
	attempt=$((attempt + 1))
	if ! kill -0 "$fixture_pid" 2>/dev/null || [ "$attempt" -ge 100 ]; then
		echo "History fixture did not start" >&2
		exit 1
	fi
	sleep 0.1
done
browser set viewport "${HISTORY_VIEWPORT_WIDTH:-1440}" "${HISTORY_VIEWPORT_HEIGHT:-900}" >/dev/null
if [ "${HISTORY_REDUCED_MOTION:-true}" = false ]; then
	browser set media light >/dev/null
else
	browser set media light reduced-motion >/dev/null
fi
browser open 'http://127.0.0.1:7312/#/v1/projects/fixture-project/projectAreas/fixture-project/chats/fixture-1' >/dev/null
browser wait --text 'Loaded answer' >/dev/null
browser eval --stdin < /app/ui-history.js >/dev/null
attempt=0
while [ "$(browser eval 'globalThis.__pixieHistory?.done === true')" != true ]; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 180 ]; then
		echo "History measurement did not finish" >&2
		exit 1
	fi
	sleep 1
done
browser eval '(() => { const state = globalThis.__pixieHistory; if (state.error) throw new Error(state.error); return state.result; })()' > /artifacts/history.json
browser eval --stdin < /app/ui-history-interactions.js > /artifacts/history-interactions.json
browser screenshot /artifacts/history.png >/dev/null
cat /artifacts/history.json
