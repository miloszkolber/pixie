// -- Tooltip --------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('tooltip');

const DELAY_DEFAULT = 500;
const GROUP_TIMEOUT = 400;

let groupOpen = false;
let groupTimer = null;
const triggerStates = new WeakMap();
const initializedTriggers = new Set();

function documentView(doc) {
  return doc.defaultView || (typeof window === 'undefined' ? null : window);
}

function supportsAnchorPosition(element) {
  const css = documentView(element.ownerDocument)?.CSS;
  return Boolean(css?.supports && css.supports('position-area', 'top'));
}

function markGroupOpen() {
  groupOpen = true;
  clearTimeout(groupTimer);
}

function scheduleGroupReset() {
  clearTimeout(groupTimer);
  groupTimer = setTimeout(() => {
    groupOpen = false;
  }, GROUP_TIMEOUT);
}

function positionFallback(tip, trigger) {
  tip.style.positionArea = 'unset';
  tip.style.positionTryFallbacks = 'none';
  tip.style.marginTop = '0';
  tip.style.marginRight = '0';
  tip.style.marginBottom = '0';
  tip.style.marginLeft = '0';

  const triggerRect = trigger.getBoundingClientRect();
  const tipRect = tip.getBoundingClientRect();
  if (!tipRect.width || !tipRect.height) return;
  const view = documentView(trigger.ownerDocument);
  if (!view) return;

  const side = tip.dataset.side || 'top';
  const align = tip.dataset.align || 'center';
  const gap = 6;
  let top;
  let left;

  if (side === 'top' || side === 'bottom') {
    top = side === 'top' ? triggerRect.top - tipRect.height - gap : triggerRect.bottom + gap;
    if (align === 'start') left = triggerRect.left;
    else if (align === 'end') left = triggerRect.right - tipRect.width;
    else left = triggerRect.left + (triggerRect.width - tipRect.width) / 2;
  } else {
    left = side === 'left' ? triggerRect.left - tipRect.width - gap : triggerRect.right + gap;
    if (align === 'start') top = triggerRect.top;
    else if (align === 'end') top = triggerRect.bottom - tipRect.height;
    else top = triggerRect.top + (triggerRect.height - tipRect.height) / 2;
  }

  top = Math.max(4, Math.min(top, view.innerHeight - tipRect.height - 4));
  left = Math.max(4, Math.min(left, view.innerWidth - tipRect.width - 4));
  tip.style.top = `${top}px`;
  tip.style.left = `${left}px`;
}

function syncArrowSide(tip, trigger) {
  const triggerRect = trigger.getBoundingClientRect();
  const tipRect = tip.getBoundingClientRect();
  if (!tipRect.width || !tipRect.height) return;

  const side = tip.dataset.side || 'top';
  const gap = 6;
  let arrowSide;

  if (side === 'top' || side === 'bottom') {
    if (tipRect.bottom <= triggerRect.top + gap) arrowSide = 'bottom';
    else if (tipRect.top >= triggerRect.bottom - gap) arrowSide = 'top';
    else arrowSide = 'bottom';
  } else {
    if (tipRect.right <= triggerRect.left + gap) arrowSide = 'right';
    else if (tipRect.left >= triggerRect.right - gap) arrowSide = 'left';
    else arrowSide = 'right';
  }

  tip.style.setProperty('--tooltip-arrow-side', arrowSide);
}

function installScrollListener(doc) {
  if (doc.__tooltipScrollInit) return;
  doc.__tooltipScrollInit = true;
  lifecycle.add(doc, () => {
    delete doc.__tooltipScrollInit;
    clearTimeout(groupTimer);
  });
  lifecycle.listen(
    doc,
    doc,
    'scroll',
    () => {
      doc.querySelectorAll('.tooltip:popover-open').forEach((tip) => {
        try {
          tip.hidePopover();
        } catch {
          // The native popover can already be closed.
        }
      });
      scheduleGroupReset();
    },
    { passive: true, capture: true }
  );
}

function resolveTooltip(trigger) {
  const id = trigger?.dataset?.tooltipTrigger;
  if (!id) return null;
  const tip = trigger.ownerDocument?.getElementById(id);
  return tip?.classList?.contains('tooltip') ? tip : null;
}

function unbindTooltip(state) {
  clearTimeout(state.openTimer);
  clearTimeout(state.closeTimer);
  state.openTimer = null;
  state.closeTimer = null;
  if (state.tip && state.onToggle) {
    state.tip.removeEventListener('toggle', state.onToggle);
    state.tip.removeEventListener('mouseenter', state.show);
    state.tip.removeEventListener('mouseleave', state.hide);
    try {
      state.tip.hidePopover();
    } catch {
      /* Already closed. */
    }
    state.tip.style.positionAnchor = '';
  }
  if (state.describedBy.size) {
    state.trigger.setAttribute('aria-describedby', Array.from(state.describedBy).join(' '));
  } else {
    state.trigger.removeAttribute('aria-describedby');
  }
  state.trigger.style.anchorName = '';
  state.tip = null;
  state.onToggle = null;
}

function bindTooltip(state, tip) {
  const { trigger } = state;
  const anchorId = `--tooltip-${tip.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
  trigger.style.anchorName = anchorId;
  tip.style.positionAnchor = anchorId;
  trigger.setAttribute(
    'aria-describedby',
    Array.from(new Set([...state.describedBy, tip.id])).join(' ')
  );

  state.tip = tip;
  state.onToggle = () => {
    if (state.tip !== tip || !tip.matches(':popover-open')) return;
    const view = documentView(tip.ownerDocument);
    const frame =
      view?.requestAnimationFrame?.bind(view) || ((callback) => setTimeout(callback, 0));
    frame(() =>
      frame(() => {
        if (state.tip === tip) syncArrowSide(tip, trigger);
      })
    );
  };
  tip.addEventListener('toggle', state.onToggle);
  tip.addEventListener('mouseenter', state.show);
  tip.addEventListener('mouseleave', state.hide);
}

function cleanupTooltipTrigger(trigger) {
  const state = triggerStates.get(trigger);
  if (!state) return;
  unbindTooltip(state);
  trigger.removeEventListener('mouseenter', state.show);
  trigger.removeEventListener('mouseleave', state.hide);
  trigger.removeEventListener('focus', state.show);
  trigger.removeEventListener('blur', state.hide);
  delete trigger.dataset.mewaTooltipInit;
  triggerStates.delete(trigger);
  initializedTriggers.delete(trigger);
}

function rebindTooltipTargets() {
  initializedTriggers.forEach((trigger) => {
    const state = triggerStates.get(trigger);
    if (!state || !trigger.isConnected) {
      cleanupTooltipTrigger(trigger);
      return;
    }

    const tip = resolveTooltip(trigger);
    if (state.tip === tip) return;
    unbindTooltip(state);
    if (tip) bindTooltip(state, tip);
  });
}

function initTooltipTrigger(trigger) {
  if (triggerStates.has(trigger)) return;
  const tip = resolveTooltip(trigger);
  if (!tip) return;

  const state = {
    trigger,
    describedBy: new Set(
      (trigger.getAttribute('aria-describedby') || '').split(/\s+/).filter(Boolean)
    ),
    tip: null,
    onToggle: null,
    openTimer: null,
    closeTimer: null,
    show: null,
    hide: null
  };
  const delayDisabled = trigger.dataset.delay === '0';

  state.show = () => {
    clearTimeout(state.closeTimer);
    clearTimeout(state.openTimer);
    const wait = groupOpen || delayDisabled ? 0 : DELAY_DEFAULT;
    state.openTimer = setTimeout(() => {
      const currentTip = state.tip;
      if (!currentTip?.isConnected) return;
      try {
        if (!currentTip.matches(':popover-open')) currentTip.showPopover();
      } catch {
        return;
      }
      if (!supportsAnchorPosition(trigger)) positionFallback(currentTip, trigger);
      markGroupOpen();
    }, wait);
  };

  state.hide = () => {
    clearTimeout(state.openTimer);
    clearTimeout(state.closeTimer);
    state.closeTimer = setTimeout(() => {
      const currentTip = state.tip;
      if (!currentTip) return;
      try {
        currentTip.hidePopover();
      } catch {
        // The native popover can already be closed.
      }
      scheduleGroupReset();
    }, 120);
  };

  trigger.dataset.init = '';
  trigger.dataset.mewaTooltipInit = '';
  trigger.addEventListener('mouseenter', state.show);
  trigger.addEventListener('mouseleave', state.hide);
  trigger.addEventListener('focus', state.show);
  trigger.addEventListener('blur', state.hide);
  triggerStates.set(trigger, state);
  initializedTriggers.add(trigger);
  bindTooltip(state, tip);
}

export function enhance(root) {
  const scope = root || (typeof document === 'undefined' ? null : document);
  const doc = scope?.nodeType === 9 ? scope : scope?.ownerDocument;
  if (doc) installScrollListener(doc);
  if (!scope || !doc) return;

  const tips = queryAll(scope, '.tooltip[id]');
  const triggerScope = tips.length ? doc : scope;
  queryAll(triggerScope, '[data-tooltip-trigger]').forEach(initTooltipTrigger);
  rebindTooltipTargets();
}

export function destroy(root) {
  lifecycle.destroy(root);
  queryAll(root, '[data-tooltip-trigger]').forEach(cleanupTooltipTrigger);
  rebindTooltipTargets();
}

export const behavior = { name: 'tooltip', enhance, destroy };
