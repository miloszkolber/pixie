// -- Toggle ---------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('toggle');

export function enhance(root) {
  queryAll(root, '.toggle:not(.toggle-group .toggle)').forEach((toggle) => {
    toggle.dataset.init = '';
    if (lifecycle.has(toggle)) return;
    toggle.dataset.mewaToggleInit = '';
    lifecycle.listen(toggle, toggle, 'click', () => {
      const pressed = toggle.getAttribute('aria-pressed') === 'true';
      toggle.setAttribute('aria-pressed', !pressed);
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'toggle', enhance, destroy };
