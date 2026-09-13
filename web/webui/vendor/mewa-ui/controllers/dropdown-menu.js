// -- Dropdown Menu --------------------------------------------

import { queryAll } from '../runtime/core.js';


const triggerStates = new WeakMap();
const initializedTriggers = new Set();

function bindMenu(trigger, state, menu) {
  const anchorId = `--dropdown-menu-${menu.id}`;
  trigger.style.anchorName = anchorId;
  menu.style.positionAnchor = anchorId;

  const itemSelector = '[role="menuitem"], [role="menuitemcheckbox"], [role="menuitemradio"]';
  const isDisabled = (item) => item.disabled || item.getAttribute('aria-disabled') === 'true';
  const getItems = () =>
    Array.from(menu.querySelectorAll(itemSelector)).filter((item) => !isDisabled(item));
  const activateCheckable = (item) => {
    if (!item || isDisabled(item)) return;
    const role = item.getAttribute('role');
    if (role === 'menuitemcheckbox') {
      const checked = item.getAttribute('aria-checked') === 'true';
      item.setAttribute('aria-checked', String(!checked));
    } else if (role === 'menuitemradio') {
      const group = item.closest('[role="group"]');
      const radios = group
        ? group.querySelectorAll('[role="menuitemradio"]')
        : menu.querySelectorAll('[role="menuitemradio"]');
      radios.forEach((radio) => {
        radio.setAttribute('aria-checked', 'false');
      });
      item.setAttribute('aria-checked', 'true');
    }
  };
  const highlight = (item) => {
    getItems().forEach((i) => {
      i.removeAttribute('data-highlighted');
    });
    if (item) {
      item.setAttribute('data-highlighted', '');
      item.focus();
    }
  };
  const onToggle = (e) => {
    if (state.menu !== menu || !trigger.isConnected) return;
    const open = e.newState === 'open';
    trigger.setAttribute('aria-expanded', open);
    if (open) {
      const first = getItems()[0];
      if (first) highlight(first);
    } else {
      getItems().forEach((i) => {
        i.removeAttribute('data-highlighted');
      });
      if (menu.contains(menu.ownerDocument.activeElement)) trigger.focus();
    }
  };
  const onMousemove = (e) => {
    if (state.menu !== menu || !trigger.isConnected) return;
    const item = e.target.closest(itemSelector);
    if (item && !isDisabled(item)) highlight(item);
  };
  const onMouseleave = () => {
    if (state.menu !== menu || !trigger.isConnected) return;
    getItems().forEach((i) => {
      i.removeAttribute('data-highlighted');
    });
  };
  const onClick = (e) => {
    if (state.menu !== menu || !trigger.isConnected) return;
    const item = e.target.closest(itemSelector);
    if (!item || !menu.contains(item)) return;
    if (isDisabled(item)) {
      e.preventDefault();
      return;
    }
    const role = item.getAttribute('role');
    if (role === 'menuitemcheckbox' || role === 'menuitemradio') activateCheckable(item);
    else menu.hidePopover();
  };
  const onKeydown = (e) => {
    if (state.menu !== menu || !trigger.isConnected) return;
    const items = getItems();
    const targetItem = e.target?.closest?.(itemSelector);
    const current = items.indexOf(targetItem || menu.ownerDocument.activeElement);
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        highlight(items[(current + 1) % items.length]);
        break;
      case 'ArrowUp':
        e.preventDefault();
        highlight(items[(current - 1 + items.length) % items.length]);
        break;
      case 'Home':
        e.preventDefault();
        highlight(items[0]);
        break;
      case 'End':
        e.preventDefault();
        highlight(items[items.length - 1]);
        break;
      case 'Escape':
        menu.hidePopover();
        break;
      case 'Enter':
      case ' ':
        e.preventDefault();
        if (targetItem && menu.contains(targetItem) && !isDisabled(targetItem)) {
          const role = targetItem.getAttribute('role');
          if (role === 'menuitemcheckbox' || role === 'menuitemradio')
            activateCheckable(targetItem);
          else {
            targetItem.click();
            menu.hidePopover();
          }
        }
        break;
      default:
        if (e.key.length === 1 && !e.ctrlKey && !e.metaKey) {
          const match = items.find((item) =>
            item.textContent.trim().toLowerCase().startsWith(e.key.toLowerCase())
          );
          if (match) highlight(match);
        }
    }
  };

  menu.addEventListener('toggle', onToggle);
  menu.addEventListener('mousemove', onMousemove);
  menu.addEventListener('mouseleave', onMouseleave);
  menu.addEventListener('click', onClick);
  menu.addEventListener('keydown', onKeydown);
  state.unbindMenu = () => {
    menu.removeEventListener('toggle', onToggle);
    menu.removeEventListener('mousemove', onMousemove);
    menu.removeEventListener('mouseleave', onMouseleave);
    menu.removeEventListener('click', onClick);
    menu.removeEventListener('keydown', onKeydown);
    if (state.menu === menu) state.menu = null;
  };
}

function rebindTargets() {
  initializedTriggers.forEach((trigger) => {
    const state = triggerStates.get(trigger);
    if (!trigger.isConnected) {
      state.unbindMenu?.();
      trigger.removeEventListener('click', state.onTriggerClick);
      delete trigger.dataset.mewaDropdownMenuInit;
      triggerStates.delete(trigger);
      initializedTriggers.delete(trigger);
      return;
    }
    const menu = trigger.ownerDocument.getElementById(trigger.dataset.dropdownMenuTrigger);
    if (!menu) {
      state.unbindMenu?.();
      state.unbindMenu = null;
      state.menu = null;
      return;
    }
    if (state.menu === menu) return;
    state.unbindMenu?.();
    state.menu = menu;
    bindMenu(trigger, state, menu);
  });
}

export function enhance(root) {
  queryAll(root, '[data-dropdown-menu-trigger]').forEach((trigger) => {
    if (triggerStates.has(trigger)) return;
    trigger.dataset.init = '';
    trigger.dataset.mewaDropdownMenuInit = '';
    const state = { menu: null, unbindMenu: null, onTriggerClick: null };
    triggerStates.set(trigger, state);
    initializedTriggers.add(trigger);
    state.onTriggerClick = () => {
      const currentMenu = trigger.ownerDocument.getElementById(trigger.dataset.dropdownMenuTrigger);
      if (
        !currentMenu ||
        !currentMenu.isConnected ||
        typeof currentMenu.togglePopover !== 'function'
      )
        return;
      trigger.focus();
      currentMenu.togglePopover();
    };
    trigger.addEventListener('click', state.onTriggerClick);
  });
  rebindTargets();
}

export function destroy(root) {
  queryAll(root, '[data-dropdown-menu-trigger]').forEach((trigger) => {
    const state = triggerStates.get(trigger);
    if (!state) return;
    state.unbindMenu?.();
    trigger.removeEventListener('click', state.onTriggerClick);
    delete trigger.dataset.mewaDropdownMenuInit;
    triggerStates.delete(trigger);
    initializedTriggers.delete(trigger);
  });
  rebindTargets();
}

export const behavior = { name: 'dropdown-menu', enhance, destroy };
