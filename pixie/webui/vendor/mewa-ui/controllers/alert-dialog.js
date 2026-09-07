// -- Alert Dialog ----------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('alert-dialog');

function openAlertDialog(dialog, trigger) {
  if (!dialog || !dialog.isConnected || typeof dialog.showModal !== 'function') return;
  if (dialog.open) return;
  dialog._trigger = trigger;
  try {
    dialog.showModal();
  } catch {
    // Do not surface a native InvalidStateError during SPA replacement.
  }
}

export function enhance(root) {
  const scope = root || (typeof document === 'undefined' ? null : document);
  const documentRoot = scope?.nodeType === 9 ? scope : scope?.ownerDocument;
  const dialogs = queryAll(scope, 'dialog.alert-dialog');
  const triggerScope = dialogs.length && documentRoot ? documentRoot : scope;

  queryAll(triggerScope, '[data-alert-dialog-trigger]').forEach((trigger) => {
    trigger.dataset.init = '';
    if (lifecycle.has(trigger)) return;
    trigger.dataset.mewaAlertDialogInit = '';
    const dialogId = trigger.dataset.alertDialogTrigger;
    const triggerDocument = trigger.ownerDocument;
    if (!triggerDocument.getElementById(dialogId)) {
      delete trigger.dataset.mewaAlertDialogInit;
      return;
    }
    lifecycle.listen(trigger, trigger, 'click', () => {
      openAlertDialog(triggerDocument.getElementById(dialogId), trigger);
    });
  });

  dialogs.forEach((dialog) => {
    dialog.dataset.init = '';
    if (lifecycle.has(dialog)) return;
    dialog.dataset.mewaAlertDialogInit = '';

    lifecycle.listen(dialog, dialog, 'click', (event) => {
      if (event.target.closest('[data-alert-dialog-close]')) dialog.close();
    });

    lifecycle.listen(dialog, dialog, 'close', () => {
      if (dialog._trigger?.isConnected) dialog._trigger.focus();
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'alert-dialog', enhance, destroy };
