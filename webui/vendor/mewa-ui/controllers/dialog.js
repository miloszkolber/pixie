// -- Dialog ---------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('dialog');

function openDialog(dialog, trigger) {
  if (!dialog || !dialog.isConnected || typeof dialog.showModal !== 'function') return;
  if (dialog.open) return;
  dialog._trigger = trigger;
  try {
    dialog.showModal();
  } catch {
    // A detached or already-open dialog can race SPA updates.
  }
}

function focusableElements(dialog) {
  const selector = [
    'a[href]',
    'area[href]',
    'button:not([disabled])',
    'input:not([disabled]):not([type="hidden"])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    '[contenteditable="true"]',
    '[tabindex]:not([tabindex="-1"]):not([disabled]):not([type="hidden"])'
  ].join(',');
  return Array.from(dialog.querySelectorAll(selector)).filter(
    (element) =>
      !element.matches(':disabled') &&
      !element.closest('[hidden], [inert]') &&
      element.tabIndex !== -1 &&
      (!element.getClientRects || element.getClientRects().length > 0)
  );
}

function initDialogTrigger(trigger) {
  trigger.dataset.init = '';
  if (lifecycle.has(trigger)) return;
  trigger.dataset.mewaDialogInit = '';
  const dialogId = trigger.dataset.dialogTrigger;
  const ownerDocument = trigger.ownerDocument;
  if (!ownerDocument.getElementById(dialogId)) {
    delete trigger.dataset.mewaDialogInit;
    return;
  }
  lifecycle.listen(trigger, trigger, 'click', () => {
    openDialog(ownerDocument.getElementById(dialogId), trigger);
  });
}

export function enhance(root) {
  const dialogs = queryAll(
    root,
    'dialog:not(.alert-dialog):not(.sheet):not(.command-palette):not(.sidebar-mobile)'
  );
  queryAll(root, '[data-dialog-trigger]').forEach(initDialogTrigger);

  dialogs.forEach((dialog) => {
    dialog.dataset.init = '';
    if (lifecycle.has(dialog)) return;
    dialog.dataset.mewaDialogInit = '';

    lifecycle.listen(dialog, dialog, 'keydown', (event) => {
      if (event.key !== 'Tab' || !dialog.open) return;
      const focusable = focusableElements(dialog);
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && dialog.ownerDocument.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && dialog.ownerDocument.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    });

    lifecycle.listen(dialog, dialog, 'click', (event) => {
      if (event.target === dialog) dialog.close();
    });

    lifecycle.listen(dialog, dialog, 'click', (event) => {
      if (event.target.closest('[data-dialog-close]')) dialog.close();
    });

    lifecycle.listen(dialog, dialog, 'close', () => {
      if (dialog._trigger?.isConnected) dialog._trigger.focus();
    });
  });

  if (dialogs.length) {
    queryAll(dialogs[0].ownerDocument, '[data-dialog-trigger]').forEach(initDialogTrigger);
  }
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'dialog', enhance, destroy };
