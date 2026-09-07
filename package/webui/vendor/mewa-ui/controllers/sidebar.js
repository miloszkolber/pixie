// -- Sidebar --------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('sidebar');

function documentView(doc) {
  return doc.defaultView || (typeof window === 'undefined' ? null : window);
}

function triggerMatchesSidebar(trigger, sidebar) {
  const targetId = trigger.dataset.sidebarTrigger;
  return targetId ? targetId === sidebar.id : !sidebar.id;
}

function syncTriggers(sidebar) {
  const collapsed = sidebar.dataset.state === 'collapsed';
  const label = collapsed ? 'Show menu' : 'Hide menu';
  sidebar.ownerDocument.querySelectorAll('.sidebar-trigger').forEach((trigger) => {
    if (!triggerMatchesSidebar(trigger, sidebar)) return;
    trigger.setAttribute('aria-expanded', String(!collapsed));
    trigger.setAttribute('aria-label', label);
    const visibleLabel = trigger.querySelector('.sidebar-trigger-label');
    if (visibleLabel && visibleLabel.textContent !== label) visibleLabel.textContent = label;
  });
}

function toggleSidebar(sidebar) {
  sidebar.dataset.state = sidebar.dataset.state === 'collapsed' ? 'expanded' : 'collapsed';
  syncTriggers(sidebar);
}

function mobileTriggerFor(dialog) {
  return Array.from(dialog.ownerDocument.querySelectorAll('[data-sidebar-mobile]')).find(
    (trigger) => trigger.dataset.sidebarMobile === dialog.id
  );
}

function closeMobileDialog(dialog) {
  if (!dialog.open) return;
  const trigger = mobileTriggerFor(dialog);
  dialog.close();
  trigger?.setAttribute('aria-expanded', 'false');
  const doc = dialog.ownerDocument;
  const desktopTrigger = doc.querySelector('.app-sidebar .sidebar-trigger');
  const desktop = documentView(doc)?.matchMedia?.('(width > 48rem)').matches;
  const focusTarget = desktop ? desktopTrigger : trigger;
  focusTarget?.focus({ preventScroll: true });
}

function openMobileDialog(dialog, trigger) {
  if (!dialog || !dialog.isConnected || typeof dialog.showModal !== 'function' || dialog.open)
    return;
  try {
    dialog.showModal();
    trigger.setAttribute('aria-expanded', 'true');
  } catch {
    // A detached or already-open dialog can race an SPA update.
  }
}

function sidebarForTrigger(trigger) {
  return Array.from(trigger.ownerDocument.querySelectorAll('.app-sidebar')).find((sidebar) =>
    triggerMatchesSidebar(trigger, sidebar)
  );
}

function installGlobalListeners(doc) {
  if (!doc.__sidebarKbInit) {
    doc.__sidebarKbInit = true;
    lifecycle.add(doc, () => delete doc.__sidebarKbInit);
    lifecycle.listen(doc, doc, 'keydown', (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'b') {
        event.preventDefault();
        const sidebar = doc.querySelector('.app-sidebar[data-mewa-sidebar-init]');
        if (sidebar) toggleSidebar(sidebar);
      }
    });
  }

  if (!doc.__sidebarViewportInit) {
    const view = documentView(doc);
    if (!view?.matchMedia) return;
    doc.__sidebarViewportInit = true;
    lifecycle.add(doc, () => delete doc.__sidebarViewportInit);
    const desktopViewport = view.matchMedia('(width > 48rem)');
    lifecycle.listen(doc, desktopViewport, 'change', (event) => {
      if (!event.matches) return;
      doc.querySelectorAll('.sidebar-mobile[open]').forEach((dialog) => closeMobileDialog(dialog));
    });
  }
}

export function enhance(root) {
  const doc =
    root?.nodeType === 9
      ? root
      : root?.ownerDocument || (typeof document === 'undefined' ? null : document);
  if (doc) installGlobalListeners(doc);

  queryAll(root, '.app-sidebar').forEach((sidebar) => {
    sidebar.dataset.init = '';
    if (lifecycle.has(sidebar)) return;
    sidebar.dataset.mewaSidebarInit = '';
    lifecycle.add(sidebar, () => {});
  });

  queryAll(root, '.sidebar-trigger').forEach((trigger) => {
    trigger.dataset.init = '';
    if (lifecycle.has(trigger)) return;
    trigger.dataset.mewaSidebarInit = '';
    const sidebar = sidebarForTrigger(trigger);
    if (!sidebar) {
      delete trigger.dataset.mewaSidebarInit;
      return;
    }
    lifecycle.listen(trigger, trigger, 'click', () => {
      const current = sidebarForTrigger(trigger);
      if (current) toggleSidebar(current);
    });
    syncTriggers(sidebar);
  });

  queryAll(root, '.app-sidebar').forEach((sidebar) => syncTriggers(sidebar));

  queryAll(root, '[data-sidebar-mobile]').forEach((trigger) => {
    trigger.dataset.init = '';
    if (lifecycle.has(trigger)) return;
    trigger.dataset.mewaSidebarInit = '';
    const dialog = trigger.ownerDocument.getElementById(trigger.dataset.sidebarMobile);
    if (!dialog) {
      delete trigger.dataset.mewaSidebarInit;
      return;
    }

    if (!trigger.hasAttribute('aria-expanded')) trigger.setAttribute('aria-expanded', 'false');
    lifecycle.listen(trigger, trigger, 'click', () => {
      openMobileDialog(
        trigger.ownerDocument.getElementById(trigger.dataset.sidebarMobile),
        trigger
      );
    });
  });

  queryAll(root, '.sidebar-mobile').forEach((dialog) => {
    dialog.dataset.init = '';
    if (lifecycle.has(dialog)) return;
    dialog.dataset.mewaSidebarInit = '';

    lifecycle.listen(dialog, dialog, 'click', (event) => {
      if (event.target.closest('.sidebar-mobile-close, .sidebar-link')) closeMobileDialog(dialog);
    });

    lifecycle.listen(dialog, dialog, 'close', () => {
      const trigger = mobileTriggerFor(dialog);
      trigger?.setAttribute('aria-expanded', 'false');
      const doc = dialog.ownerDocument;
      if (dialog.contains(doc.activeElement) || doc.activeElement === doc.body) {
        trigger?.focus({ preventScroll: true });
      }
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'sidebar', enhance, destroy };
