// -- Command Palette -----------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('command-palette');

let commandItemId = 0;

function isConnected(element) {
  return Boolean(element && element.isConnected);
}

function isDisabled(item) {
  return item.disabled || item.getAttribute('aria-disabled') === 'true';
}

function getVisibleItems(list) {
  return Array.from(list.querySelectorAll('.command-palette-item')).filter(
    (item) => !item.hidden && !isDisabled(item)
  );
}

function clearHighlight(list, input) {
  list.querySelectorAll('.command-palette-item').forEach((item) => {
    item.removeAttribute('data-highlighted');
    item.setAttribute('aria-selected', 'false');
  });
  if (input) input.removeAttribute('aria-activedescendant');
}

function highlightItem(list, index, input) {
  const visible = getVisibleItems(list);
  clearHighlight(list, input);
  if (visible.length === 0) return -1;

  const clamped = ((index % visible.length) + visible.length) % visible.length;
  const item = visible[clamped];
  item.setAttribute('data-highlighted', '');
  item.setAttribute('aria-selected', 'true');
  if (input && item.id) input.setAttribute('aria-activedescendant', item.id);
  if (typeof item.scrollIntoView === 'function') item.scrollIntoView({ block: 'nearest' });
  return clamped;
}

function showPalette(dialog, trigger) {
  if (!isConnected(dialog) || typeof dialog.showModal !== 'function') return false;
  if (!dialog.open) {
    if (trigger) dialog._trigger = trigger;
    try {
      dialog.showModal();
    } catch {
      return false;
    }
  }
  const input = dialog.querySelector('.command-palette-input');
  if (input) input.focus();
  return true;
}

function installDocumentListener(documentRoot) {
  if (documentRoot.__commandPaletteKeydownInit) return;
  documentRoot.__commandPaletteKeydownInit = true;
  lifecycle.add(documentRoot, () => delete documentRoot.__commandPaletteKeydownInit);
  lifecycle.listen(documentRoot, documentRoot, 'keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      const dialog = documentRoot.querySelector(
        'dialog.command-palette[data-mewa-command-palette-init]'
      );
      if (!isConnected(dialog)) return;
      e.preventDefault();
      if (dialog.open) dialog.close();
      else showPalette(dialog);
    }
  });
}

export function enhance(root) {
  lifecycle.refresh(root);
  const scope = root || (typeof document === 'undefined' ? null : document);
  const documentRoot = scope?.nodeType === 9 ? scope : scope?.ownerDocument;
  if (!documentRoot) return;
  installDocumentListener(documentRoot);

  const dialogs = queryAll(scope, 'dialog.command-palette');
  dialogs.forEach((dialog) => {
    dialog.dataset.init = '';
    if (lifecycle.has(dialog)) return;
    dialog.dataset.mewaCommandPaletteInit = '';
    const input = dialog.querySelector('.command-palette-input');
    const inputWrapper = dialog.querySelector('.command-palette-input-wrapper');
    const list = dialog.querySelector('.command-palette-list');
    const empty = dialog.querySelector('.command-palette-empty');
    if (!input || !list) {
      delete dialog.dataset.mewaCommandPaletteInit;
      return;
    }

    if (!list.id) list.setAttribute('id', `command-palette-list-${++commandItemId}`);
    input.setAttribute('aria-controls', list.id);

    const items = () => Array.from(list.querySelectorAll('.command-palette-item'));
    const prepareItems = () =>
      items().forEach((item) => {
        if (!item.id) item.id = `${list.id}-item-${++commandItemId}`;
        if (!item.hasAttribute('aria-selected')) item.setAttribute('aria-selected', 'false');
      });
    prepareItems();
    lifecycle.listen(
      dialog,
      list,
      'click',
      (event) => {
        const item = event.target.closest('.command-palette-item');
        if (!item || !isDisabled(item)) return;
        event.preventDefault();
        event.stopImmediatePropagation();
      },
      true
    );
    let highlightIndex = -1;

    const filter = (q) => {
      const query = q.toLowerCase();
      prepareItems();
      items().forEach((item) => {
        item.hidden = Boolean(query) && !item.textContent.toLowerCase().includes(query);
      });
      list.querySelectorAll('.command-palette-group').forEach((group) => {
        group.hidden = !Array.from(group.querySelectorAll('.command-palette-item')).some(
          (item) => !item.hidden
        );
      });
      list.querySelectorAll('.command-palette-separator').forEach((separator) => {
        separator.hidden = Boolean(query);
      });
      if (empty) empty.hidden = items().some((item) => !item.hidden);
      highlightIndex = highlightItem(list, 0, input);
    };

    lifecycle.onUpdate(dialog, () => filter(input.value));
    lifecycle.listen(dialog, input, 'input', () => {
      filter(input.value);
    });
    if (inputWrapper)
      lifecycle.listen(dialog, inputWrapper, 'click', () => {
        input.focus();
      });

    lifecycle.listen(dialog, input, 'keydown', (e) => {
      if (e.isComposing) return;
      const visible = getVisibleItems(list);
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        highlightIndex = highlightItem(list, highlightIndex + 1, input);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        highlightIndex = highlightItem(list, highlightIndex - 1, input);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        const item = visible[highlightIndex];
        if (item && !isDisabled(item)) item.click();
      } else if (e.key === 'Home') {
        e.preventDefault();
        highlightIndex = highlightItem(list, 0, input);
      } else if (e.key === 'End') {
        e.preventDefault();
        highlightIndex = highlightItem(list, visible.length - 1, input);
      }
    });

    lifecycle.listen(dialog, dialog, 'click', (e) => {
      if (e.target === dialog) {
        if (dialog.open) dialog.close();
        return;
      }
      const item = e.target.closest('.command-palette-item');
      if (!item) return;
      if (isDisabled(item)) {
        e.preventDefault();
        return;
      }
      if (dialog.open) dialog.close();
    });
    lifecycle.listen(dialog, dialog, 'close', () => {
      input.value = '';
      filter('');
      clearHighlight(list, input);
      highlightIndex = -1;
      if (dialog._trigger?.isConnected) dialog._trigger.focus();
    });

    filter('');
  });

  const triggerScope = dialogs.length ? documentRoot : scope;
  queryAll(triggerScope, '[data-command-palette-trigger]').forEach((trigger) => {
    trigger.dataset.init = '';
    if (lifecycle.has(trigger)) return;
    trigger.dataset.mewaCommandPaletteInit = '';
    const dialogId = trigger.dataset.commandPaletteTrigger;
    const triggerDocument = trigger.ownerDocument;
    const dialog = triggerDocument.getElementById(dialogId);
    if (!dialog) {
      // Leave the trigger eligible for a later SPA insertion of its dialog.
      delete trigger.dataset.mewaCommandPaletteInit;
      return;
    }
    lifecycle.listen(trigger, trigger, 'click', () => {
      const currentDialog = triggerDocument.getElementById(dialogId);
      showPalette(currentDialog, trigger);
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'command-palette', enhance, destroy };
