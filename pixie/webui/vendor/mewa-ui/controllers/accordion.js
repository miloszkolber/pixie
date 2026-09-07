// -- Accordion -----------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('accordion');

export function enhance(root) {
  queryAll(root, '.accordion[data-type="single"]').forEach((accordion) => {
    accordion.dataset.init = '';
    if (lifecycle.has(accordion)) return;
    accordion.dataset.mewaAccordionInit = '';
    lifecycle.listen(
      accordion,
      accordion,
      'toggle',
      (event) => {
        const item = event.target;
        if (!item.matches('.accordion-item') || item.closest('.accordion') !== accordion) return;
        const items = [...accordion.querySelectorAll('.accordion-item')].filter(
          (candidate) => candidate.closest('.accordion') === accordion
        );
        if (item.open) {
          items.forEach((sibling) => {
            if (sibling !== item && sibling.open) sibling.open = false;
          });
        } else if (
          !accordion.hasAttribute('data-collapsible') &&
          !items.some((candidate) => candidate.open)
        ) {
          item.open = true;
        }
      },
      true
    );
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'accordion', enhance, destroy };
