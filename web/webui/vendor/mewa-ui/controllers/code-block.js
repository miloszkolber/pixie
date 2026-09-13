import { queryAll } from '../runtime/core.js';


const codeBlockInstances = new WeakMap();

function codeBlockAtBottom(viewport) {
  return viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop <= 24;
}

function codeBlockText(root, code) {
  const lines = Array.from(root.querySelectorAll('.code-block-line-text'));
  if (lines.length) return lines.map((line) => line.textContent || '').join('\n');
  return code.textContent || '';
}

function initCodeBlock(root) {
  if (codeBlockInstances.has(root)) return;
  root.dataset.init = '';
  root.dataset.mewaCodeBlockInit = '';

  const viewport = root.querySelector('.code-block-viewport');
  const code = root.querySelector('.code-block-code');
  if (!viewport || !code) {
    root.removeAttribute('data-mewa-code-block-init');
    return;
  }

  const copyButton = root.querySelector('[data-code-block-copy]');
  const status = root.querySelector('.code-block-status');
  let copyTimer = null;
  let active = true;
  let streaming = root.hasAttribute('data-streaming');
  let pinned = streaming || codeBlockAtBottom(viewport);

  const scrollToBottom = () => {
    viewport.scrollTop = viewport.scrollHeight;
  };

  const onScroll = () => {
    pinned = codeBlockAtBottom(viewport);
  };

  const onCopy = async () => {
    if (typeof navigator === 'undefined' || !navigator.clipboard?.writeText) return;
    try {
      await navigator.clipboard.writeText(codeBlockText(root, code));
    } catch {
      return;
    }

    if (!active) return;
    copyButton.textContent = 'Copied';
    copyButton.setAttribute('aria-label', 'Copied');
    if (status) status.textContent = 'Copied to clipboard.';
    if (copyTimer !== null) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copyButton.textContent = 'Copy';
      copyButton.setAttribute('aria-label', 'Copy');
      if (status) status.textContent = '';
      copyTimer = null;
    }, 2000);
  };

  viewport.addEventListener('scroll', onScroll, { passive: true });

  if (copyButton) {
    copyButton.hidden = false;
    copyButton.setAttribute('aria-label', 'Copy');
    copyButton.addEventListener('click', onCopy);
  }

  const contentObserver = new MutationObserver(() => {
    if (streaming && pinned) scrollToBottom();
  });
  contentObserver.observe(code, { childList: true, subtree: true, characterData: true });

  const attributeObserver = new MutationObserver(() => {
    const next = root.hasAttribute('data-streaming');
    if (next && !streaming) {
      pinned = true;
      scrollToBottom();
    }
    streaming = next;
  });
  attributeObserver.observe(root, { attributes: true, attributeFilter: ['data-streaming'] });

  const resizeObserver =
    typeof ResizeObserver === 'function'
      ? new ResizeObserver(() => {
          if (streaming && pinned) scrollToBottom();
        })
      : null;
  resizeObserver?.observe(code);

  if (streaming) scrollToBottom();

  codeBlockInstances.set(root, {
    destroy() {
      active = false;
      viewport.removeEventListener('scroll', onScroll);
      copyButton?.removeEventListener('click', onCopy);
      contentObserver.disconnect();
      attributeObserver.disconnect();
      resizeObserver?.disconnect();
      if (copyTimer !== null) clearTimeout(copyTimer);
      if (copyButton) {
        copyButton.hidden = true;
        copyButton.textContent = 'Copy';
        copyButton.removeAttribute('aria-label');
      }
      if (status) status.textContent = '';
      root.removeAttribute('data-mewa-code-block-init');
      codeBlockInstances.delete(root);
    }
  });
}

export function enhance(root) {
  queryAll(root, '.code-block').forEach(initCodeBlock);
}

export function destroy(root) {
  queryAll(root, '.code-block').forEach((codeBlock) => {
    codeBlockInstances.get(codeBlock)?.destroy();
  });
}

export const behavior = { name: 'code-block', enhance, destroy };
