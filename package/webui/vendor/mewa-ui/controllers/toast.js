// -- Toast -----------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('toast');
const DURATION = 4000;
const MAX_VISIBLE = 3;

const isMounted = (el) => Boolean(el && (el.parentNode || el.parentElement));
const toastStates = new WeakMap();

function ensureToastState(doc, root = doc) {
  const current = toastStates.get(doc);
  if (current?.container && isMounted(current.container)) return current;

  let toastContainer =
    queryAll(root, '#toast-container')[0] || doc.getElementById('toast-container');
  const createdContainer = !toastContainer;
  if (!toastContainer) {
    toastContainer = doc.createElement('div');
    toastContainer.id = 'toast-container';
    toastContainer.className = 'toast-container';
    toastContainer.setAttribute('role', 'region');
    toastContainer.setAttribute('aria-label', 'Notifications');
    toastContainer.setAttribute('data-position', 'bottom-right');
    doc.body.appendChild(toastContainer);
  }

  const state = {
    container: toastContainer,
    createdContainer,
    adapterInstalled: current?.adapterInstalled || false,
    api: current?.api || null,
    roots: current?.roots || new Set(),
    previousApi: current?.previousApi
  };
  toastStates.set(doc, state);
  return state;
}

function createToastApi(doc) {
  const show = (options) => toastCreate(doc, options);
  return {
    show,
    success: (o) =>
      show(Object.assign({}, typeof o === 'string' ? { title: o } : o, { variant: 'success' })),
    warning: (o) =>
      show(Object.assign({}, typeof o === 'string' ? { title: o } : o, { variant: 'warning' })),
    info: (o) =>
      show(Object.assign({}, typeof o === 'string' ? { title: o } : o, { variant: 'info' })),
    error: (o) =>
      show(Object.assign({}, typeof o === 'string' ? { title: o } : o, { variant: 'destructive' })),
    dismiss: () => {
      ensureToastState(doc)
        .container.querySelectorAll('.toast')
        .forEach((el) => toastDismiss(el));
    }
  };
}

function installToastAdapter(doc, root) {
  const state = ensureToastState(doc, root);
  if (!state.api) state.api = createToastApi(doc);

  const view = doc.defaultView || (typeof window === 'undefined' ? null : window);
  if (view && !state.adapterInstalled) {
    state.previousApi = view.toast;
    view.toast = state.api;
    state.adapterInstalled = true;
  }
  return state;
}

const toastDismiss = (el, callback) => {
  if (!isMounted(el)) return;
  if (typeof el._toastCancelTimer === 'function') el._toastCancelTimer();
  lifecycle.destroy(el);
  try {
    el.hidePopover();
  } catch {
    /* already closed */
  }
  el.remove();
  if (callback) callback();
};

const toastCreate = (doc, options) => {
  const toastContainer = ensureToastState(doc).container;
  const o = typeof options === 'string' ? { title: options } : options;
  const { title, description, variant, action, onDismiss } = o;
  const duration = o.duration != null ? o.duration : DURATION;
  const el = doc.createElement('div');
  el.className = 'toast';
  el.setAttribute('role', variant === 'destructive' ? 'alert' : 'status');
  el.setAttribute('aria-live', variant === 'destructive' ? 'assertive' : 'polite');
  el.setAttribute('aria-atomic', 'true');
  el.setAttribute('popover', 'manual');
  if (variant) el.setAttribute('data-variant', variant);
  const icons = {
    success:
      '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/></svg>',
    warning:
      '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/></svg>',
    info: '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/></svg>',
    destructive:
      '<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/></svg>'
  };
  const contentEl = doc.createElement('div');
  contentEl.className = 'toast-content';
  if (variant && icons[variant]) {
    const tmpl = doc.createElement('template');
    tmpl.innerHTML = icons[variant];
    contentEl.appendChild(tmpl.content);
  }
  const textDiv = doc.createElement('div');
  textDiv.className = 'toast-text';
  if (title) {
    const p = doc.createElement('p');
    p.className = 'toast-title';
    p.textContent = title;
    textDiv.appendChild(p);
  }
  if (description) {
    const p = doc.createElement('p');
    p.className = 'toast-description';
    p.textContent = description;
    textDiv.appendChild(p);
  }
  contentEl.appendChild(textDiv);
  const closeBtn = doc.createElement('button');
  closeBtn.type = 'button';
  closeBtn.className = 'toast-close';
  closeBtn.setAttribute('aria-label', 'Dismiss');
  closeBtn.dataset.toastClose = '';
  closeBtn.innerHTML =
    '<svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg>';
  contentEl.appendChild(closeBtn);
  el.appendChild(contentEl);
  if (action) {
    const actionsDiv = doc.createElement('div');
    actionsDiv.className = 'toast-actions';
    const actionBtn = doc.createElement('button');
    actionBtn.type = 'button';
    actionBtn.className = 'btn';
    actionBtn.setAttribute('data-variant', 'outline');
    actionBtn.setAttribute('data-size', 'sm');
    actionBtn.dataset.toastAction = '';
    actionBtn.textContent = action.label;
    actionsDiv.appendChild(actionBtn);
    el.appendChild(actionsDiv);
  }
  toastContainer.appendChild(el);
  el.showPopover();
  lifecycle.listen(el, closeBtn, 'click', () => {
    toastDismiss(el, onDismiss);
  });
  if (action) {
    lifecycle.listen(el, el.querySelector('[data-toast-action]'), 'click', () => {
      if (action.onClick) action.onClick();
      toastDismiss(el);
    });
  }

  if (duration !== Infinity) {
    let timer = null;
    let startedAt = 0;
    const numericDuration = Number(duration);
    let remaining = Number.isFinite(numericDuration) ? Math.max(0, numericDuration) : 0;
    let paused = false;
    let hovered = false;
    let focused = false;

    const cancelTimer = () => {
      if (timer !== null) clearTimeout(timer);
      timer = null;
    };
    const schedule = () => {
      if (paused || timer !== null || !isMounted(el)) return;
      startedAt = Date.now();
      timer = setTimeout(() => {
        timer = null;
        remaining = 0;
        toastDismiss(el, onDismiss);
      }, remaining);
    };
    const pause = () => {
      if (paused) return;
      paused = true;
      if (timer !== null) {
        remaining = Math.max(0, remaining - (Date.now() - startedAt));
        cancelTimer();
      }
    };
    const resume = () => {
      if (!paused) return;
      paused = false;
      if (remaining <= 0) toastDismiss(el, onDismiss);
      else schedule();
    };
    const syncPause = () => {
      if (hovered || focused) pause();
      else resume();
    };

    el._toastCancelTimer = cancelTimer;
    lifecycle.listen(el, el, 'mouseenter', () => {
      hovered = true;
      syncPause();
    });
    lifecycle.listen(el, el, 'mouseleave', () => {
      hovered = false;
      syncPause();
    });
    lifecycle.listen(el, el, 'focusin', () => {
      focused = true;
      syncPause();
    });
    lifecycle.listen(el, el, 'focusout', (event) => {
      focused = Boolean(event.relatedTarget && el.contains(event.relatedTarget));
      syncPause();
    });
    schedule();
  }
  const toasts = toastContainer.querySelectorAll('.toast');
  if (toasts.length > MAX_VISIBLE) toastDismiss(toasts[0]);
  return el;
};

export function enhance(root) {
  const doc =
    root?.nodeType === 9
      ? root
      : root?.ownerDocument || (typeof document === 'undefined' ? null : document);
  if (!doc?.body) return;
  const state = installToastAdapter(doc, root || doc);
  if (!state.roots.has(doc)) state.roots.add(root || doc);
  return state.api;
}

export function destroy(root) {
  const doc = root?.nodeType === 9 ? root : root?.ownerDocument;
  const state = toastStates.get(doc);
  if (!state) return;
  for (const owner of state.roots) {
    if (owner === root || root?.contains?.(owner)) state.roots.delete(owner);
  }
  if (state.roots.size) return;
  state.container.querySelectorAll('.toast').forEach((element) => toastDismiss(element));
  const view = doc.defaultView;
  if (view?.toast === state.api) {
    if (state.previousApi === undefined) delete view.toast;
    else view.toast = state.previousApi;
  }
  if (state.createdContainer) state.container.remove();
  toastStates.delete(doc);
}

export const behavior = { name: 'toast', enhance, destroy };
