// -- Toolbar --------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('toolbar');

export function enhance(root) {
  lifecycle.refresh(root);
  queryAll(root, '.toolbar[role="toolbar"]').forEach((toolbar) => {
    toolbar.dataset.init = '';
    if (lifecycle.has(toolbar)) return;
    toolbar.dataset.mewaToolbarInit = '';
    const getItems = () =>
      Array.from(toolbar.querySelectorAll('button, a[href], [tabindex]')).filter(
        (item) =>
          item.closest('[role="toolbar"]') === toolbar &&
          !item.matches(':disabled') &&
          item.getAttribute('aria-disabled') !== 'true' &&
          !item.closest('[hidden], [inert]')
      );
    lifecycle.onUpdate(toolbar, () => {
      const current = getItems();
      const active =
        current.find((item) => item === toolbar.ownerDocument.activeElement) ||
        current.find((item) => item.getAttribute('tabindex') === '0') ||
        current[0];
      current.forEach((item) => item.setAttribute('tabindex', item === active ? '0' : '-1'));
    });
    const items = getItems();

    items.forEach((item, i) => {
      item.setAttribute('tabindex', i === 0 ? '0' : '-1');
    });

    lifecycle.listen(toolbar, toolbar, 'keydown', (e) => {
      const items = getItems();
      const current = items.indexOf(toolbar.ownerDocument.activeElement);
      if (current === -1) return;

      const vertical = toolbar.getAttribute('aria-orientation') === 'vertical';
      const fwd = vertical ? 'ArrowDown' : 'ArrowRight';
      const bwd = vertical ? 'ArrowUp' : 'ArrowLeft';
      let next;

      if (e.key === fwd) {
        e.preventDefault();
        next = (current + 1) % items.length;
      } else if (e.key === bwd) {
        e.preventDefault();
        next = (current - 1 + items.length) % items.length;
      } else if (e.key === 'Home') {
        e.preventDefault();
        next = 0;
      } else if (e.key === 'End') {
        e.preventDefault();
        next = items.length - 1;
      }

      if (next !== undefined) {
        items[current].setAttribute('tabindex', '-1');
        items[next].setAttribute('tabindex', '0');
        items[next].focus();
      }
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'toolbar', enhance, destroy };
