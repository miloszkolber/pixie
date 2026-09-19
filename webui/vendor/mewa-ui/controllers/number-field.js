// -- Number Field ---------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('number-field');

export function enhance(root) {
  queryAll(root, '.number-field').forEach((wrapper) => {
    wrapper.dataset.init = '';
    if (lifecycle.has(wrapper)) return;
    wrapper.dataset.mewaNumberFieldInit = '';
    const input = wrapper.querySelector('input[type="number"]');
    const decBtn = wrapper.querySelector('[data-action="decrement"]');
    const incBtn = wrapper.querySelector('[data-action="increment"]');
    if (!input) return;

    const update = (direction) => {
      if (input.matches(':disabled') || input.readOnly) return;
      try {
        if (direction > 0) input.stepUp();
        else input.stepDown();
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new Event('change', { bubbles: true }));
      } catch {
        /* min/max boundary */
      }
    };

    if (decBtn)
      lifecycle.listen(wrapper, decBtn, 'click', () => {
        update(-1);
      });
    if (incBtn)
      lifecycle.listen(wrapper, incBtn, 'click', () => {
        update(1);
      });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'number-field', enhance, destroy };
