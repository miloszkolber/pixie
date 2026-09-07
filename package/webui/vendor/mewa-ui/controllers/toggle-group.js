// -- Toggle Group ---------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('toggle-group');

export function enhance(root) {
  queryAll(root, '.toggle-group').forEach((group) => {
    group.dataset.init = '';
    if (lifecycle.has(group)) return;
    group.dataset.mewaToggleGroupInit = '';
    const type = group.getAttribute('data-type') || 'single';

    const getToggles = () => Array.from(group.querySelectorAll('.toggle:not(:disabled)'));

    const initTabindex = () => {
      const toggles = getToggles();
      if (toggles.length === 0) return;
      const pressed = toggles.find((t) => t.getAttribute('aria-pressed') === 'true');
      const active = pressed || toggles[0];
      toggles.forEach((t) => {
        t.setAttribute('tabindex', t === active ? '0' : '-1');
      });
    };

    initTabindex();

    lifecycle.listen(group, group, 'click', (e) => {
      const toggle = e.target.closest('.toggle');
      if (!toggle || toggle.disabled || group.hasAttribute('data-disabled')) return;

      const toggles = getToggles();
      const pressed = toggle.getAttribute('aria-pressed') === 'true';

      if (type === 'single') {
        toggles.forEach((t) => t.setAttribute('aria-pressed', 'false'));
        if (!pressed) toggle.setAttribute('aria-pressed', 'true');
      } else {
        toggle.setAttribute('aria-pressed', String(!pressed));
      }

      toggles.forEach((t) => t.setAttribute('tabindex', t === toggle ? '0' : '-1'));
    });

    lifecycle.listen(group, group, 'keydown', (e) => {
      const toggle = e.target.closest('.toggle');
      if (!toggle || group.hasAttribute('data-disabled')) return;

      const toggles = getToggles();
      const idx = toggles.indexOf(toggle);
      if (idx === -1) return;

      const vertical = group.getAttribute('data-orientation') === 'vertical';
      const fwd = vertical ? 'ArrowDown' : 'ArrowRight';
      const bwd = vertical ? 'ArrowUp' : 'ArrowLeft';
      let next;

      if (e.key === fwd) {
        e.preventDefault();
        next = (idx + 1) % toggles.length;
      } else if (e.key === bwd) {
        e.preventDefault();
        next = (idx - 1 + toggles.length) % toggles.length;
      } else if (e.key === 'Home') {
        e.preventDefault();
        next = 0;
      } else if (e.key === 'End') {
        e.preventDefault();
        next = toggles.length - 1;
      }

      if (next !== undefined) {
        // A toggle group is a nested composite inside toolbars. Keep the
        // handled arrow/Home/End event from reaching the parent toolbar, which
        // would advance the focus a second time.
        e.stopPropagation();
        toggles[idx].setAttribute('tabindex', '-1');
        toggles[next].setAttribute('tabindex', '0');
        toggles[next].focus();
      }
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'toggle-group', enhance, destroy };
