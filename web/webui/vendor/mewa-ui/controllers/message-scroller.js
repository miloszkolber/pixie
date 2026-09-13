import { queryAll } from '../runtime/core.js';


const messageScrollerInstances = new WeakMap();

function messageScrollerThreshold(root) {
  const configured = Number.parseFloat(root.dataset.threshold || '');
  return Number.isFinite(configured) && configured > 0 ? configured : 24;
}

function initMessageScroller(root) {
  if (messageScrollerInstances.has(root)) return;
  root.dataset.init = '';
  root.dataset.mewaMessageScrollerInit = '';

  const viewport = root.querySelector('.message-scroller-viewport');
  const content = root.querySelector('.message-scroller-content');
  const jump = root.querySelector('[data-message-scroller-jump]');
  if (!viewport || !content) {
    root.removeAttribute('data-mewa-message-scroller-init');
    return;
  }

  const threshold = messageScrollerThreshold(root);
  let pinned = root.dataset.defaultPinned !== 'false';
  let conversationKey = root.dataset.conversationKey || '';

  const atBottom = () =>
    viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop <= threshold;

  const syncJump = () => {
    if (jump) jump.hidden = pinned;
  };

  const setPinned = (next, emit = true) => {
    const changed = pinned !== next;
    pinned = next;
    root.dataset.pinned = pinned ? 'true' : 'false';
    syncJump();
    if (changed && emit) {
      root.dispatchEvent(
        new CustomEvent('message-scroller:pinned-change', {
          bubbles: true,
          detail: { pinned }
        })
      );
    }
  };

  const scrollToBottom = (emit = true) => {
    viewport.scrollTop = viewport.scrollHeight;
    setPinned(true, emit);
  };

  const onScroll = () => setPinned(atBottom());
  const onJump = () => {
    if (root.ownerDocument.activeElement === jump) viewport.focus({ preventScroll: true });
    scrollToBottom();
  };

  viewport.addEventListener('scroll', onScroll, { passive: true });
  jump?.addEventListener('click', onJump);

  const contentObserver = new MutationObserver(() => {
    if (pinned) scrollToBottom(false);
  });
  contentObserver.observe(content, { childList: true, subtree: true, characterData: true });

  const attributeObserver = new MutationObserver(() => {
    const nextKey = root.dataset.conversationKey || '';
    if (nextKey === conversationKey) return;
    conversationKey = nextKey;
    scrollToBottom();
  });
  attributeObserver.observe(root, { attributes: true, attributeFilter: ['data-conversation-key'] });

  const resizeObserver =
    typeof ResizeObserver === 'function'
      ? new ResizeObserver(() => {
          if (pinned) scrollToBottom(false);
        })
      : null;
  resizeObserver?.observe(viewport);
  resizeObserver?.observe(content);

  if (pinned) scrollToBottom(false);
  else setPinned(false, false);

  messageScrollerInstances.set(root, {
    destroy() {
      viewport.removeEventListener('scroll', onScroll);
      jump?.removeEventListener('click', onJump);
      contentObserver.disconnect();
      attributeObserver.disconnect();
      resizeObserver?.disconnect();
      if (jump) jump.hidden = true;
      root.removeAttribute('data-pinned');
      root.removeAttribute('data-mewa-message-scroller-init');
      messageScrollerInstances.delete(root);
    }
  });
}

export function enhance(root) {
  const scrollers = queryAll(root, '.message-scroller');
  const ancestor = root?.nodeType === 1 ? root.closest?.('.message-scroller') : null;
  if (ancestor) scrollers.push(ancestor);
  new Set(scrollers).forEach(initMessageScroller);
}

export function destroy(root) {
  queryAll(root, '.message-scroller').forEach((scroller) => {
    messageScrollerInstances.get(scroller)?.destroy();
  });
}

export const behavior = { name: 'message-scroller', enhance, destroy };
