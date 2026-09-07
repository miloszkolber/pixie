// -- Tabs -----------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const selectTab = (tab, triggers) => {
  triggers.forEach((t) => {
    t.setAttribute('aria-selected', 'false');
    t.setAttribute('tabindex', '-1');
    const panel = t.ownerDocument.getElementById(t.getAttribute('aria-controls'));
    if (panel) panel.hidden = true;
  });
  tab.setAttribute('aria-selected', 'true');
  tab.removeAttribute('tabindex');
  const panel = tab.ownerDocument.getElementById(tab.getAttribute('aria-controls'));
  if (panel) panel.hidden = false;
};

// Activate a tab inside one tablist. `target` is a tab element, a tab id,
// or the id of the panel a tab controls. Disabled tabs are ignored.
const activateInTablist = (tablist, target) => {
  const triggers = tabsIn(tablist);
  let tab = null;
  if (typeof target === 'string') {
    tab =
      triggers.find((t) => t.id === target) ||
      triggers.find((t) => t.getAttribute('aria-controls') === target);
  } else if (target && triggers.includes(target)) {
    tab = target;
  }
  if (!tab || disabled(tab)) return false;
  selectTab(tab, triggers);
  return true;
};

const lifecycle = createLifecycle('tabs');
const tabsIn = (list) =>
  Array.from(list.querySelectorAll('[role="tab"]')).filter(
    (tab) => tab.closest('[role="tablist"]') === list
  );
const disabled = (tab) =>
  tab.disabled || tab.matches(':disabled') || tab.getAttribute('aria-disabled') === 'true';

export function enhance(root) {
  queryAll(root, '[role="tablist"]').forEach((tablist) => {
    tablist.dataset.init = '';
    if (lifecycle.has(tablist)) return;
    tablist.dataset.mewaTabsInit = '';
    lifecycle.listen(tablist, tablist, 'tabs:activate', (event) => {
      if (event.target === tablist && event.detail) activateInTablist(tablist, event.detail.id);
    });
    lifecycle.listen(tablist, tablist, 'click', (event) => {
      const tab = event.target.closest('[role="tab"]');
      if (tab && tabsIn(tablist).includes(tab) && !disabled(tab)) selectTab(tab, tabsIn(tablist));
    });
    lifecycle.listen(tablist, tablist, 'keydown', (event) => {
      const trigger = event.target.closest('[role="tab"]');
      const triggers = tabsIn(tablist).filter((tab) => !disabled(tab));
      const current = triggers.indexOf(trigger);
      if (current < 0) return;
      const vertical = tablist.getAttribute('aria-orientation') === 'vertical';
      let next;
      if (event.key === (vertical ? 'ArrowDown' : 'ArrowRight'))
        next = triggers[(current + 1) % triggers.length];
      else if (event.key === (vertical ? 'ArrowUp' : 'ArrowLeft'))
        next = triggers[(current + triggers.length - 1) % triggers.length];
      else if (event.key === 'Home') next = triggers[0];
      else if (event.key === 'End') next = triggers.at(-1);
      if (!next) return;
      event.preventDefault();
      selectTab(next, tabsIn(tablist));
      next.focus();
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}
export const behavior = { name: 'tabs', enhance, destroy };
