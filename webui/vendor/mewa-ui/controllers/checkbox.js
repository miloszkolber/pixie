// -- Checkbox group enhancement ---------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('checkbox');

const CHECKBOX_GROUP_SELECTOR = '.checkbox-group, [data-checkbox-group]';

export function enhance(root) {
  lifecycle.refresh(root);
  queryAll(root, CHECKBOX_GROUP_SELECTOR).forEach((group) => {
    group.dataset.init = '';
    if (lifecycle.has(group)) return;
    group.dataset.mewaCheckboxInit = '';

    const selectAll = group.querySelector(
      '[data-checkbox-all], .checkbox-group-select-all input[type="checkbox"]'
    );
    const items = () =>
      Array.from(
        group.querySelectorAll('[data-checkbox-item], .checkbox-group-items input[type="checkbox"]')
      ).filter((item) => item !== selectAll);
    const status = group.querySelector('[data-checkbox-status], .checkbox-group-status');
    if (!selectAll || items().length === 0) {
      group.removeAttribute('data-mewa-checkbox-init');
      return;
    }

    const update = (source = 'sync', emit = true) => {
      const enabledItems = items().filter((item) => !item.disabled && !item.matches(':disabled'));
      const checkedItems = enabledItems.filter((item) => item.checked);
      const allChecked = enabledItems.length > 0 && checkedItems.length === enabledItems.length;
      const partiallyChecked = checkedItems.length > 0 && !allChecked;

      selectAll.checked = allChecked;
      selectAll.indeterminate = partiallyChecked;
      group.dataset.state = partiallyChecked ? 'partial' : allChecked ? 'complete' : 'empty';

      if (status) {
        status.textContent =
          enabledItems.length === 0
            ? 'No options available.'
            : `${checkedItems.length} of ${enabledItems.length} options selected.`;
      }

      if (emit) {
        group.dispatchEvent(
          new CustomEvent('checkbox-group:change', {
            bubbles: true,
            detail: {
              values: checkedItems.map((item) => item.value),
              selected: checkedItems.length,
              total: enabledItems.length,
              source
            }
          })
        );
      }
    };

    lifecycle.listen(group, selectAll, 'change', () => {
      items().forEach((item) => {
        if (!item.disabled && !item.matches(':disabled')) item.checked = selectAll.checked;
      });
      update('select-all');
    });

    lifecycle.listen(group, group, 'change', (event) => {
      if (items().includes(event.target)) update('item');
    });
    lifecycle.reset(group, selectAll.form || items()[0]?.form, () => update('reset', false));

    lifecycle.onUpdate(group, () => update('update', false));
    update('initial', false);
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'checkbox', enhance, destroy };
