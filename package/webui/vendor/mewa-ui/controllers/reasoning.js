import { queryAll } from '../runtime/core.js';


const reasoningInstances = new WeakMap();

function initReasoning(root) {
  if (reasoningInstances.has(root)) return;
  root.dataset.init = '';
  root.dataset.mewaReasoningInit = '';

  const summary = root.querySelector(':scope > .reasoning-trigger');
  if (!summary || root.tagName !== 'DETAILS') {
    root.removeAttribute('data-mewa-reasoning-init');
    return;
  }

  let streaming = root.hasAttribute('data-streaming');
  let manual = false;

  const onSummaryClick = () => {
    manual = true;
  };

  const syncStreaming = () => {
    const next = root.hasAttribute('data-streaming');
    if (next === streaming) return;
    streaming = next;
    if (streaming) {
      manual = false;
      root.open = true;
    } else if (root.hasAttribute('data-collapse-on-complete') && !manual) {
      root.open = false;
    }
  };

  summary.addEventListener('click', onSummaryClick);

  const observer = new MutationObserver(syncStreaming);
  observer.observe(root, { attributes: true, attributeFilter: ['data-streaming'] });

  if (streaming) root.open = true;

  reasoningInstances.set(root, {
    destroy() {
      summary.removeEventListener('click', onSummaryClick);
      observer.disconnect();
      root.removeAttribute('data-mewa-reasoning-init');
      reasoningInstances.delete(root);
    }
  });
}

export function enhance(root) {
  const disclosures = queryAll(root, '.reasoning');
  const ancestor = root?.nodeType === 1 ? root.closest?.('.reasoning') : null;
  if (ancestor) disclosures.push(ancestor);
  new Set(disclosures).forEach(initReasoning);
}

export function destroy(root) {
  queryAll(root, '.reasoning').forEach((disclosure) => {
    reasoningInstances.get(disclosure)?.destroy();
  });
}

export const behavior = { name: 'reasoning', enhance, destroy };
