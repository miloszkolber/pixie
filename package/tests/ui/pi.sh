#!/bin/sh
set -eu
session=pixie-pi-acceptance
fixture_pid=
browser() { agent-browser --session "$session" "$@"; }
assert_eval() { browser eval "(() => { if (!($1)) throw new Error('Pi UI acceptance assertion failed: ' + JSON.stringify('$2')); return true; })()" >/dev/null; }
settings_tab() {
 browser eval "(() => { const row = Array.from(document.querySelectorAll('[data-testid=settings-section-row]')).find(candidate => candidate.textContent?.trim() === '$1'); if (!(row instanceof HTMLElement)) throw new Error('Settings section is unavailable: $1'); row.click(); return true; })()" >/dev/null
}
open_settings() {
 browser eval "(() => { const button = Array.from(document.querySelectorAll('[data-testid=open-settings]')).find(candidate => candidate instanceof HTMLElement && candidate.getClientRects().length > 0); if (!(button instanceof HTMLElement)) throw new Error('Settings control is unavailable'); button.click(); return true; })()" >/dev/null
}
open_chats() {
 browser eval '(() => { const projects = document.querySelector("[data-testid=mobile-projects]"); if (projects instanceof HTMLElement && projects.getClientRects().length > 0) projects.click(); return true; })()' >/dev/null
 browser wait --timeout 60000 --fn 'Array.from(document.querySelectorAll("[data-testid=mobile-area-chats], [data-testid=rail-chats]")).some(candidate => candidate instanceof HTMLElement && candidate.getClientRects().length > 0)' >/dev/null
 browser eval "(() => { const button = Array.from(document.querySelectorAll('[data-testid=mobile-area-chats], [data-testid=rail-chats]')).find(candidate => candidate instanceof HTMLElement && candidate.getClientRects().length > 0); if (!(button instanceof HTMLElement)) throw new Error('Chats control is unavailable'); button.click(); return true; })()" >/dev/null
}
cleanup() {
 status=$?
 trap - EXIT INT TERM
 if [ "$status" -ne 0 ]; then
  browser screenshot /artifacts/pi-failure.png >/dev/null 2>&1 || true
  browser get text body > /artifacts/pi-failure.txt 2>&1 || true
  cat /artifacts/pi-failure.txt >&2 || true
 fi
 browser close >/dev/null 2>&1 || true
 if [ -n "$fixture_pid" ]; then kill -TERM "$fixture_pid" >/dev/null 2>&1 || true; wait "$fixture_pid" >/dev/null 2>&1 || true; fi
 exit "$status"
}
trap cleanup EXIT INT TERM
rm -f /tmp/pixie-ui-ready
PIXIE_UI_FIXTURE_PI=1 /app/pixie-ui-fixture > /artifacts/pi-fixture.log 2>&1 &
fixture_pid=$!
attempt=0
while [ ! -s /tmp/pixie-ui-ready ]; do
 attempt=$((attempt + 1))
 if ! kill -0 "$fixture_pid" 2>/dev/null || [ "$attempt" -ge 100 ]; then echo 'Pi fixture did not start' >&2; exit 1; fi
 sleep 0.1
done
browser set viewport 1440 900 >/dev/null
browser open 'http://127.0.0.1:7312/#/v1/projects/fixture-project/projectAreas/fixture-project/chats/fixture-1' >/dev/null
browser wait --timeout 60000 --text 'Loaded answer' >/dev/null
browser find testid open-settings click >/dev/null
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=settings-detail]") !== null' >/dev/null
settings_tab Providers
browser wait --timeout 60000 --fn 'document.querySelector("[data-provider=atomic_chat]") !== null' >/dev/null
assert_eval "document.querySelector('[data-testid=provider-row][data-provider=atomic_chat]')?.dataset.configured === 'false' && document.querySelector('[data-testid=provider-row][data-provider=cursor-agent]')?.dataset.configured === 'false'" 'default connections are not configured'
assert_eval "!document.querySelector('[data-testid=provider-row][data-provider=claude-code]') && document.querySelector('[data-testid=provider-row][data-provider=anthropic]')?.dataset.configured === 'true'" 'legacy filtering preserves configured providers'
browser find role checkbox check --name 'Show legacy providers' >/dev/null
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=provider-row][data-provider=claude-code]") !== null' >/dev/null
assert_eval "document.querySelector('[data-testid=provider-row][data-provider=claude-code]')?.dataset.configured === 'false' && document.querySelector('[data-testid=provider-row][data-provider=gemini-cli]')?.dataset.configured === 'false'" 'legacy default connections remain unconfigured'

# Check content geometry, not only the outer document width.
for theme in light dark; do
 browser set media "$theme" reduced-motion >/dev/null
 for width in 320 390 768 1024 1440; do
  browser set viewport "$width" 900 >/dev/null
  browser eval 'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))' >/dev/null
  for tab in Providers Models Pi Tools System; do
   settings_tab "$tab"
   browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=settings-detail] [role=tabpanel]:not([hidden])") !== null && !document.querySelector("[data-testid=settings-detail] [role=tabpanel]:not([hidden])")?.textContent?.includes("Loading settings")' >/dev/null
   browser eval 'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))' >/dev/null
   assert_eval "(() => { const detail = document.querySelector('[data-testid=settings-detail]'); return detail && [...detail.querySelectorAll('[role=tabpanel]')].every(e => e.scrollWidth <= e.clientWidth + 1) && detail.getBoundingClientRect().right <= innerWidth + 1; })()" "$theme $width $tab overflow"
   browser screenshot "/artifacts/pi-${theme}-${width}-${tab}.png" >/dev/null
  done
 echo "Pi settings checked: $theme ${width}px"
 done
done
browser set viewport 1024 900 >/dev/null
settings_tab Models
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=settings-section-row][aria-selected=true]")?.textContent?.trim() === "Models"' >/dev/null
browser focus '[data-testid=settings-section-row][aria-controls=settings-panel-pi]' >/dev/null
browser press ArrowRight >/dev/null
assert_eval "document.activeElement?.getAttribute('aria-controls') === 'settings-panel-providers'" 'Settings section arrow navigation'
browser press Enter >/dev/null
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=settings-section-row][aria-controls=settings-panel-providers]")?.getAttribute("aria-selected") === "true"' >/dev/null
browser set viewport 320 520 >/dev/null
browser eval 'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))' >/dev/null
assert_eval "document.querySelector('[data-testid=settings-detail]')?.getBoundingClientRect().bottom <= innerHeight + 1" 'short settings view remains reachable'
browser screenshot /artifacts/pi-short-settings.png >/dev/null
open_chats
browser wait --timeout 60000 --fn 'Array.from(document.querySelectorAll("[data-testid=mobile-area-chats], [data-testid=rail-chats]")).some(button => button.getAttribute("aria-current") === "page")' >/dev/null
browser eval 'document.querySelector("[data-testid=mobile-primary]")?.click(); true' >/dev/null
browser wait --timeout 60000 --fn '(() => { const input = document.querySelector("[data-testid=chat-input]"); return input?.getClientRects().length && input.closest("[aria-hidden=true]") === null; })()' >/dev/null
for dimensions in '320 400' '390 480' '1024 500'; do
 browser set viewport $dimensions >/dev/null
 browser eval 'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))' >/dev/null
 assert_eval "document.querySelector('[data-testid=chat-input]').getBoundingClientRect().bottom <= innerHeight && document.querySelector('[data-testid=chat-send]').getBoundingClientRect().bottom <= innerHeight && document.querySelector('[data-testid=chat-scroll]').getBoundingClientRect().height >= 64" 'short viewport keeps conversation and composer reachable'
 browser screenshot "/artifacts/pi-short-${dimensions% *}.png" >/dev/null
done
browser set viewport 1440 900 >/dev/null
open_settings
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=settings-detail]") !== null' >/dev/null
browser set viewport 390 844 >/dev/null
browser eval 'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))' >/dev/null
settings_tab Pi
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=auto-compact-threshold]")?.disabled === false' >/dev/null
browser fill '[data-testid=auto-compact-threshold]' '42' >/dev/null
settings_tab Models
browser wait --timeout 60000 --fn 'document.querySelector("#settings-panel-models")?.hidden === false' >/dev/null
settings_tab Pi
browser wait --timeout 60000 --fn 'document.querySelector("#settings-panel-pi")?.hidden === false' >/dev/null
assert_eval "document.querySelector('[data-testid=auto-compact-threshold]')?.value === '42'" 'settings draft survives tab switch'
settings_tab Providers
browser eval "$(cat /app/ui-faults.js)" >/dev/null
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=provider-apikey][data-provider=openai]") !== null' >/dev/null
browser click '[data-testid=provider-apikey][data-provider=openai]' >/dev/null
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=login-input]") !== null' >/dev/null
browser fill '[data-testid=login-input]' 'synthetic-review-key' >/dev/null
browser eval "window.reviewFailures.add('provider.loginReply'); true" >/dev/null
browser eval "document.querySelector('[data-testid=login-input]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}));true" >/dev/null
assert_eval "!window.reviewFrames.some(frame => frame.method === 'provider.loginReply')" 'IME Enter does not submit credentials'
browser find testid login-submit click >/dev/null
browser wait --timeout 60000 --text 'Synthetic review: connection failed before acceptance' >/dev/null
assert_eval "document.querySelector('[data-testid=login-input]')?.value === 'synthetic-review-key' && document.querySelector('[data-testid=login-dialog]')?.open" 'rejected credential reply preserves input'
browser screenshot /artifacts/pi-login-recovery.png >/dev/null
browser find testid login-cancel click >/dev/null
browser wait --timeout 60000 --fn 'document.querySelector("[data-testid=login-dialog]")?.open !== true' >/dev/null
open_chats
browser wait --timeout 60000 --fn 'Array.from(document.querySelectorAll("[data-testid=mobile-area-chats], [data-testid=rail-chats]")).some(button => button.getAttribute("aria-current") === "page")' >/dev/null
browser eval 'document.querySelector("[data-testid=mobile-primary]")?.click(); true' >/dev/null
browser wait --timeout 60000 --fn '(() => { const input = document.querySelector("[data-testid=chat-input]"); return input?.getClientRects().length && input.closest("[aria-hidden=true]") === null; })()' >/dev/null
browser eval "window.reviewFailures.clear();window.reviewFailures.add('session.prompt');const data=new DataTransfer();data.items.add(new File([Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aDaYAAAAASUVORK5CYII='),c=>c.charCodeAt(0))],'retained.png',{type:'image/png'}));const input=document.querySelector('input[type=file]');input.files=data.files;input.dispatchEvent(new Event('change',{bubbles:true}));true" >/dev/null
browser wait --timeout 60000 --text 'retained.png' >/dev/null
browser fill '[data-testid=chat-input]' 'Preserve the complete rejected message' >/dev/null
browser find testid chat-send click >/dev/null
browser wait --timeout 60000 --fn 'document.querySelector("textarea[aria-label=\"Retained message\"]") !== null' >/dev/null
assert_eval "document.querySelector('textarea[aria-label=\"Retained message\"]')?.value === 'Preserve the complete rejected message' && document.body.innerText.includes('Attachments: retained.png')" 'failed submission retains text and image'
browser screenshot /artifacts/pi-prompt-recovery.png >/dev/null
echo 'Pi UI acceptance passed'
