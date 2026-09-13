// -- Sheet ----------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('sheet');

function openSheet(sheet, trigger) {
  if (!sheet || !sheet.isConnected || typeof sheet.showModal !== 'function') return;
  if (sheet.open) return;
  sheet._trigger = trigger;
  try {
    sheet.showModal();
    // Keep initial focus on the surface. showModal() would otherwise focus
    // the first control and select its text.
    if (!sheet.hasAttribute('tabindex')) sheet.setAttribute('tabindex', '-1');
    sheet.focus();
  } catch {
    // A target can be replaced between the click and showModal() in an SPA.
  }
}

function initSheetTrigger(trigger) {
  trigger.dataset.init = '';
  if (lifecycle.has(trigger)) return;
  trigger.dataset.mewaSheetInit = '';
  const sheetId = trigger.dataset.sheetTrigger;
  const ownerDocument = trigger.ownerDocument;
  if (!ownerDocument.getElementById(sheetId)) {
    delete trigger.dataset.mewaSheetInit;
    return;
  }
  lifecycle.listen(trigger, trigger, 'click', () => {
    openSheet(ownerDocument.getElementById(sheetId), trigger);
  });
}

export function enhance(root) {
  const sheets = queryAll(root, 'dialog.sheet');
  queryAll(root, '[data-sheet-trigger]').forEach(initSheetTrigger);

  sheets.forEach((sheet) => {
    sheet.dataset.init = '';
    if (lifecycle.has(sheet)) return;
    sheet.dataset.mewaSheetInit = '';
    lifecycle.listen(sheet, sheet, 'click', (e) => {
      if (e.target === sheet) sheet.close();
    });
    lifecycle.listen(sheet, sheet, 'click', (event) => {
      if (event.target.closest('[data-sheet-close]')) sheet.close();
    });
    lifecycle.listen(sheet, sheet, 'close', () => {
      if (sheet._trigger?.isConnected) sheet._trigger.focus();
    });
  });

  if (sheets.length) {
    queryAll(sheets[0].ownerDocument, '[data-sheet-trigger]').forEach(initSheetTrigger);
  }
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'sheet', enhance, destroy };
