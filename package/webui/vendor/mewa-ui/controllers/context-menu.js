// -- Context Menu --------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('context-menu');

const TRIGGER_SELECTOR = '[data-context-menu-trigger]';
const ITEM_SELECTOR = '[role="menuitem"], [role="menuitemcheckbox"], [role="menuitemradio"]';

let activeContextMenu = null;

function resolveMenu(trigger) {
  const id = trigger?.dataset?.contextMenuTrigger;
  if (!id) return null;
  const menu = trigger.ownerDocument?.getElementById(id);
  return menu?.classList?.contains('context-menu-content') ? menu : null;
}

function isDisabled(item) {
  return Boolean(item?.disabled) || item?.getAttribute?.('aria-disabled') === 'true';
}

function getItems(menu) {
  return Array.from(menu.querySelectorAll(ITEM_SELECTOR)).filter((item) => !isDisabled(item));
}

function clearHighlight(menu) {
  getItems(menu).forEach((item) => item.removeAttribute('data-highlighted'));
}

function highlight(menu, item) {
  clearHighlight(menu);
  if (!item || isDisabled(item)) return;
  item.setAttribute('data-highlighted', '');
  item.focus();
}

function activateCheckable(menu, item) {
  const role = item.getAttribute('role');
  if (role === 'menuitemcheckbox') {
    item.setAttribute('aria-checked', String(item.getAttribute('aria-checked') !== 'true'));
    return true;
  }
  if (role !== 'menuitemradio') return false;

  const group = item.closest('[role="group"]');
  const radios = group
    ? group.querySelectorAll('[role="menuitemradio"]')
    : menu.querySelectorAll('[role="menuitemradio"]');
  radios.forEach((radio) => radio.setAttribute('aria-checked', 'false'));
  item.setAttribute('aria-checked', 'true');
  return true;
}

function closeContextMenu({ restoreFocus = false } = {}) {
  if (!activeContextMenu) return;
  const { trigger, menu } = activeContextMenu;
  const focusWasInside = menu.contains(menu.ownerDocument.activeElement);
  activeContextMenu = null;
  trigger.setAttribute('aria-expanded', 'false');
  clearHighlight(menu);
  try {
    if (menu.matches(':popover-open')) menu.hidePopover();
  } catch {
    // The native popover can already be closed or unsupported.
  }
  if ((restoreFocus || focusWasInside) && trigger.isConnected) trigger.focus();
}

function clampPosition(menu, inline, block) {
  const edge = 4;
  const rect = menu.getBoundingClientRect();
  const view = menu.ownerDocument.defaultView || (typeof window === 'undefined' ? null : window);
  const left = Math.max(edge, Math.min(inline, (view?.innerWidth ?? inline) - rect.width - edge));
  const top = Math.max(edge, Math.min(block, (view?.innerHeight ?? block) - rect.height - edge));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
}

function openContextMenu(trigger, inline, block) {
  const menu = resolveMenu(trigger);
  if (!menu || typeof menu.showPopover !== 'function') return false;

  if (activeContextMenu && activeContextMenu.menu !== menu) closeContextMenu();
  trigger.setAttribute('aria-expanded', 'true');
  menu.style.left = '0px';
  menu.style.top = '0px';

  try {
    if (!menu.matches(':popover-open')) menu.showPopover();
  } catch {
    trigger.setAttribute('aria-expanded', 'false');
    return false;
  }

  activeContextMenu = { trigger, menu };
  clampPosition(menu, inline, block);
  const first = getItems(menu)[0];
  if (first) highlight(menu, first);
  return true;
}

function initContextMenuTriggers(root) {
  queryAll(root, `${TRIGGER_SELECTOR}:not([data-context-menu-init])`).forEach((trigger) => {
    const menu = resolveMenu(trigger);
    if (!menu) return;
    trigger.dataset.contextMenuInit = '';
    trigger.setAttribute('aria-haspopup', 'menu');
    trigger.setAttribute('aria-controls', menu.id);
    trigger.setAttribute('aria-expanded', 'false');
  });
}

function installGlobalListeners(documentRoot) {
  if (documentRoot.__mewaContextMenuInit) return;
  documentRoot.__mewaContextMenuInit = true;
  lifecycle.add(documentRoot, () => delete documentRoot.__mewaContextMenuInit);

  lifecycle.listen(documentRoot, documentRoot, 'contextmenu', (event) => {
    const trigger = event.target?.closest?.(TRIGGER_SELECTOR);
    if (!trigger?.hasAttribute('data-context-menu-init')) return;
    const menu = resolveMenu(trigger);
    if (!trigger || !menu || typeof menu.showPopover !== 'function') return;
    event.preventDefault();
    openContextMenu(trigger, event.clientX, event.clientY, false);
  });

  lifecycle.listen(documentRoot, documentRoot, 'keydown', (event) => {
    const trigger = event.target?.closest?.(TRIGGER_SELECTOR);
    const requestsContextMenu =
      event.key === 'ContextMenu' ||
      event.key === 'Apps' ||
      (event.shiftKey && event.key === 'F10');

    if (trigger && requestsContextMenu) {
      if (!trigger?.hasAttribute('data-context-menu-init')) return;
      const menu = resolveMenu(trigger);
      if (!menu || typeof menu.showPopover !== 'function') return;
      event.preventDefault();
      const rect = trigger.getBoundingClientRect();
      openContextMenu(trigger, rect.left, rect.bottom, true);
      return;
    }

    if (!activeContextMenu) return;
    const { menu } = activeContextMenu;

    if (event.key === 'Escape') {
      event.preventDefault();
      closeContextMenu({ restoreFocus: true });
      return;
    }

    if (event.key === 'Tab') {
      closeContextMenu();
      return;
    }

    const items = getItems(menu);
    if (!items.length || !menu.contains(event.target)) return;
    const targetItem = event.target.closest?.(ITEM_SELECTOR);
    const current = Math.max(0, items.indexOf(targetItem));

    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        highlight(menu, items[(current + 1) % items.length]);
        break;
      case 'ArrowUp':
        event.preventDefault();
        highlight(menu, items[(current - 1 + items.length) % items.length]);
        break;
      case 'Home':
        event.preventDefault();
        highlight(menu, items[0]);
        break;
      case 'End':
        event.preventDefault();
        highlight(menu, items[items.length - 1]);
        break;
      case 'Enter':
      case ' ':
        if (!targetItem || isDisabled(targetItem)) break;
        event.preventDefault();
        targetItem.click();
        break;
      default:
        if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
          const query = event.key.toLowerCase();
          const match = items.find((item) =>
            item.textContent.trim().toLowerCase().startsWith(query)
          );
          if (match) {
            event.preventDefault();
            highlight(menu, match);
          }
        }
    }
  });

  lifecycle.listen(documentRoot, documentRoot, 'mousemove', (event) => {
    if (!activeContextMenu) return;
    const { menu } = activeContextMenu;
    const item = event.target?.closest?.(ITEM_SELECTOR);
    if (item && menu.contains(item) && !isDisabled(item)) highlight(menu, item);
  });

  lifecycle.listen(documentRoot, documentRoot, 'click', (event) => {
    if (!activeContextMenu) return;
    const { menu } = activeContextMenu;
    const item = event.target?.closest?.(ITEM_SELECTOR);
    if (!item || !menu.contains(item) || isDisabled(item)) return;
    if (!activateCheckable(menu, item)) closeContextMenu();
  });

  lifecycle.listen(documentRoot, documentRoot, 'pointerdown', (event) => {
    if (!activeContextMenu) return;
    const { trigger, menu } = activeContextMenu;
    if (menu.contains(event.target) || trigger.contains(event.target)) return;
    closeContextMenu();
  });

  lifecycle.listen(documentRoot, documentRoot, 'scroll', () => closeContextMenu(), {
    passive: true,
    capture: true
  });
  const view = documentRoot.defaultView || (typeof window === 'undefined' ? null : window);
  lifecycle.listen(documentRoot, view, 'resize', () => closeContextMenu());
}

export function enhance(root) {
  const scope = root || (typeof document === 'undefined' ? null : document);
  const documentRoot = scope?.nodeType === 9 ? scope : scope?.ownerDocument;
  if (!documentRoot) return;
  installGlobalListeners(documentRoot);

  const triggerScope = queryAll(scope, '.context-menu-content').length ? documentRoot : scope;
  initContextMenuTriggers(triggerScope);

  if (
    activeContextMenu &&
    (!activeContextMenu.trigger.isConnected || !activeContextMenu.menu.isConnected)
  ) {
    activeContextMenu = null;
  }
}

export function destroy(root) {
  lifecycle.destroy(root);
  queryAll(root, TRIGGER_SELECTOR).forEach((trigger) => {
    delete trigger.dataset.contextMenuInit;
    trigger.setAttribute('aria-expanded', 'false');
  });
  const removedActiveMenu =
    activeContextMenu &&
    (root === activeContextMenu.trigger ||
      root === activeContextMenu.menu ||
      root?.contains?.(activeContextMenu.trigger) ||
      root?.contains?.(activeContextMenu.menu));
  if (
    removedActiveMenu ||
    (activeContextMenu &&
      (!activeContextMenu.trigger.isConnected || !activeContextMenu.menu.isConnected))
  ) {
    closeContextMenu();
  }
}

export const behavior = { name: 'context-menu', enhance, destroy };
