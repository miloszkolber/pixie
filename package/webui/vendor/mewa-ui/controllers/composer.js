import { queryAll } from '../runtime/core.js';


const composerInstances = new WeakMap();

function initComposer(root) {
  if (composerInstances.has(root)) return;
  root.dataset.init = '';
  root.dataset.mewaComposerInit = '';

  const input = root.querySelector('.composer-input');
  if (!input || input.tagName !== 'TEXTAREA') {
    root.removeAttribute('data-mewa-composer-init');
    return;
  }

  const requestSubmit = () => {
    if (typeof root.requestSubmit === 'function') {
      root.requestSubmit();
      return;
    }
    root.querySelector('button[type="submit"], input[type="submit"]')?.click();
  };

  const onKeyDown = (event) => {
    if (event.defaultPrevented || event.isComposing || event.key !== 'Enter') return;
    const submitOn = root.dataset.submitOn === 'enter' ? 'enter' : 'mod-enter';
    const modifier = event.metaKey || event.ctrlKey;
    const shouldSubmit = submitOn === 'enter' ? !event.shiftKey : modifier && !event.shiftKey;
    if (!shouldSubmit) return;
    event.preventDefault();
    requestSubmit();
  };

  input.addEventListener('keydown', onKeyDown);

  composerInstances.set(root, {
    destroy() {
      input.removeEventListener('keydown', onKeyDown);
      root.removeAttribute('data-mewa-composer-init');
      composerInstances.delete(root);
    }
  });
}

export function enhance(root) {
  queryAll(root, '.composer').forEach(initComposer);
}

export function destroy(root) {
  queryAll(root, '.composer').forEach((composer) => {
    composerInstances.get(composer)?.destroy();
  });
}

export const behavior = { name: 'composer', enhance, destroy };
