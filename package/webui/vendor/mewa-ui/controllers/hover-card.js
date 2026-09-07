// -- Hover Card ----------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('hover-card');

const HOVER_CARD_OPEN_DELAY = 150;
const HOVER_CARD_CLOSE_DELAY = 100;
const initializedDocuments = new WeakSet();
const triggerStates = new WeakMap();
const initializedTriggers = new Set();

let activeHoverCard = null;

function hoverCardAnchorSupported(element) {
  const css = element.ownerDocument.defaultView?.CSS || (typeof CSS === 'undefined' ? null : CSS);
  return typeof css?.supports === 'function' && css.supports('position-area', 'bottom');
}

function positionHoverCardFallback(card, trigger) {
  card.style.positionArea = 'unset';
  card.style.positionTryFallbacks = 'none';
  card.style.marginTop = '0';
  card.style.marginRight = '0';
  card.style.marginBottom = '0';
  card.style.marginLeft = '0';

  const triggerRect = trigger.getBoundingClientRect();
  const cardRect = card.getBoundingClientRect();
  if (!cardRect.width || !cardRect.height) return;

  const side = card.dataset.side || 'bottom';
  const align = card.dataset.align || 'center';
  const gap = 8;
  const edge = 4;
  let top;
  let left;

  if (side === 'top' || side === 'bottom') {
    top = side === 'top' ? triggerRect.top - cardRect.height - gap : triggerRect.bottom + gap;
    if (align === 'start') left = triggerRect.left;
    else if (align === 'end') left = triggerRect.right - cardRect.width;
    else left = triggerRect.left + (triggerRect.width - cardRect.width) / 2;
  } else {
    left = side === 'left' ? triggerRect.left - cardRect.width - gap : triggerRect.right + gap;
    if (align === 'start') top = triggerRect.top;
    else if (align === 'end') top = triggerRect.bottom - cardRect.height;
    else top = triggerRect.top + (triggerRect.height - cardRect.height) / 2;
  }

  const view = card.ownerDocument.defaultView || (typeof window === 'undefined' ? null : window);
  if (!view) return;
  card.style.top = `${Math.max(edge, Math.min(top, view.innerHeight - cardRect.height - edge))}px`;
  card.style.left = `${Math.max(edge, Math.min(left, view.innerWidth - cardRect.width - edge))}px`;
}

function closeHoverCard(state, { restoreFocus = false, immediate = false } = {}) {
  clearTimeout(state.openTimer);
  clearTimeout(state.closeTimer);
  const card = state.card;
  if (!card) return;

  const close = () => {
    if (state.card !== card) return;
    const activeElement = card.ownerDocument.activeElement;
    const focusWasInside = card.contains(activeElement);
    try {
      if (card.matches(':popover-open')) card.hidePopover();
    } catch {
      // The native popover can already be closed or unsupported.
    }
    if (activeHoverCard === state) activeHoverCard = null;
    if (
      (restoreFocus || focusWasInside) &&
      state.trigger.isConnected &&
      card.ownerDocument.activeElement !== state.trigger
    ) {
      state.suppressFocusOpen = true;
      state.trigger.focus();
    }
  };

  if (immediate) close();
  else state.closeTimer = setTimeout(close, HOVER_CARD_CLOSE_DELAY);
}

function scheduleHoverCardClose(state) {
  if (!state.card || state.pointerOnTrigger || state.pointerOnCard || state.focusWithin) return;
  closeHoverCard(state);
}

function openHoverCard(state, immediate = false) {
  clearTimeout(state.closeTimer);
  clearTimeout(state.openTimer);
  const card = state.card;
  if (!card) return;

  const open = () => {
    if (state.card !== card || !state.trigger.isConnected || !card.isConnected) return;
    if (activeHoverCard && activeHoverCard !== state) {
      closeHoverCard(activeHoverCard, { immediate: true });
    }
    try {
      if (!card.matches(':popover-open')) card.showPopover();
    } catch {
      return;
    }
    activeHoverCard = state;
    if (!hoverCardAnchorSupported(card)) positionHoverCardFallback(card, state.trigger);
  };

  if (immediate) open();
  else state.openTimer = setTimeout(open, HOVER_CARD_OPEN_DELAY);
}

function resolveHoverCard(trigger) {
  const id = trigger.dataset.hoverCardTrigger;
  const card = trigger.ownerDocument.getElementById(id);
  return card?.classList?.contains('hover-card') && typeof card.showPopover === 'function'
    ? card
    : null;
}

function unbindHoverCard(state) {
  clearTimeout(state.openTimer);
  clearTimeout(state.closeTimer);
  state.openTimer = null;
  state.closeTimer = null;
  if (activeHoverCard === state) closeHoverCard(state, { immediate: true });

  const { card, cardListeners } = state;
  if (card && cardListeners) {
    card.removeEventListener('mouseenter', cardListeners.mouseenter);
    card.removeEventListener('mouseleave', cardListeners.mouseleave);
    card.removeEventListener('focusin', cardListeners.focusin);
    card.removeEventListener('focusout', cardListeners.focusout);
    card.removeEventListener('keydown', state.onEscape);
    card.style.positionAnchor = '';
  }
  if (state.describedBy.size) {
    state.trigger.setAttribute('aria-describedby', Array.from(state.describedBy).join(' '));
  } else {
    state.trigger.removeAttribute('aria-describedby');
  }
  state.trigger.style.anchorName = '';
  state.card = null;
  state.cardListeners = null;
  state.pointerOnCard = false;
  state.focusWithin = false;
}

function bindHoverCard(state, card) {
  const { trigger } = state;
  const anchorId = `--hover-card-${card.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
  trigger.style.anchorName = anchorId;
  card.style.positionAnchor = anchorId;
  trigger.setAttribute(
    'aria-describedby',
    Array.from(new Set([...state.describedBy, card.id])).join(' ')
  );
  state.card = card;
  state.focusWithin =
    trigger.ownerDocument.activeElement === trigger ||
    card.contains(trigger.ownerDocument.activeElement);

  state.cardListeners = {
    mouseenter: () => {
      state.pointerOnCard = true;
      clearTimeout(state.closeTimer);
      clearTimeout(state.openTimer);
    },
    mouseleave: (event) => {
      state.pointerOnCard = false;
      if (event.relatedTarget && trigger.contains(event.relatedTarget)) return;
      scheduleHoverCardClose(state);
    },
    focusin: () => {
      state.focusWithin = true;
      clearTimeout(state.closeTimer);
    },
    focusout: (event) => {
      if (
        event.relatedTarget &&
        (card.contains(event.relatedTarget) || trigger.contains(event.relatedTarget))
      )
        return;
      state.focusWithin = false;
      scheduleHoverCardClose(state);
    }
  };

  card.addEventListener('mouseenter', state.cardListeners.mouseenter);
  card.addEventListener('mouseleave', state.cardListeners.mouseleave);
  card.addEventListener('focusin', state.cardListeners.focusin);
  card.addEventListener('focusout', state.cardListeners.focusout);
  card.addEventListener('keydown', state.onEscape);
}

function cleanupHoverCardTrigger(trigger) {
  const state = triggerStates.get(trigger);
  if (!state) return;
  unbindHoverCard(state);
  trigger.removeEventListener('mouseenter', state.triggerListeners.mouseenter);
  trigger.removeEventListener('mouseleave', state.triggerListeners.mouseleave);
  trigger.removeEventListener('focus', state.triggerListeners.focus);
  trigger.removeEventListener('blur', state.triggerListeners.blur);
  trigger.removeEventListener('keydown', state.onEscape);
  delete trigger.dataset.hoverCardInit;
  triggerStates.delete(trigger);
  initializedTriggers.delete(trigger);
}

function rebindHoverCardTargets() {
  initializedTriggers.forEach((trigger) => {
    const state = triggerStates.get(trigger);
    if (!state || !trigger.isConnected) {
      cleanupHoverCardTrigger(trigger);
      return;
    }

    const card = resolveHoverCard(trigger);
    if (state.card === card) return;
    unbindHoverCard(state);
    if (card) bindHoverCard(state, card);
  });
}

function initHoverCard(trigger) {
  if (triggerStates.has(trigger)) return;
  const card = resolveHoverCard(trigger);
  if (!card) return;

  trigger.dataset.hoverCardInit = '';

  const state = {
    trigger,
    describedBy: new Set(
      (trigger.getAttribute('aria-describedby') || '').split(/\s+/).filter(Boolean)
    ),
    card: null,
    cardListeners: null,
    triggerListeners: null,
    onEscape: null,
    openTimer: null,
    closeTimer: null,
    pointerOnTrigger: false,
    pointerOnCard: false,
    focusWithin: false,
    suppressFocusOpen: false
  };

  state.triggerListeners = {
    mouseenter: () => {
      state.pointerOnTrigger = true;
      openHoverCard(state);
    },
    mouseleave: (event) => {
      state.pointerOnTrigger = false;
      if (event.relatedTarget && state.card?.contains(event.relatedTarget)) return;
      scheduleHoverCardClose(state);
    },
    focus: () => {
      state.focusWithin = true;
      if (state.suppressFocusOpen) {
        state.suppressFocusOpen = false;
        return;
      }
      openHoverCard(state, true);
    },
    blur: (event) => {
      if (event.relatedTarget && state.card?.contains(event.relatedTarget)) return;
      state.focusWithin = false;
      scheduleHoverCardClose(state);
    }
  };

  state.onEscape = (event) => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    closeHoverCard(state, { restoreFocus: true, immediate: true });
  };

  trigger.addEventListener('mouseenter', state.triggerListeners.mouseenter);
  trigger.addEventListener('mouseleave', state.triggerListeners.mouseleave);
  trigger.addEventListener('focus', state.triggerListeners.focus);
  trigger.addEventListener('blur', state.triggerListeners.blur);
  trigger.addEventListener('keydown', state.onEscape);
  triggerStates.set(trigger, state);
  initializedTriggers.add(trigger);
  bindHoverCard(state, card);
}

function initHoverCards(root) {
  const triggers = queryAll(root, '[data-hover-card-trigger]');
  const cards = queryAll(root, '.hover-card[id]');
  if (cards.length) {
    triggers.push(...queryAll(cards[0].ownerDocument, '[data-hover-card-trigger]'));
  }
  new Set(triggers).forEach(initHoverCard);
  rebindHoverCardTargets();
}

function installGlobalListeners(ownerDocument) {
  if (initializedDocuments.has(ownerDocument)) return;
  initializedDocuments.add(ownerDocument);
  lifecycle.add(ownerDocument, () => initializedDocuments.delete(ownerDocument));

  lifecycle.listen(ownerDocument, ownerDocument, 'pointerdown', (event) => {
    if (!activeHoverCard) return;
    const { trigger, card } = activeHoverCard;
    if (trigger.contains(event.target) || card.contains(event.target)) return;
    closeHoverCard(activeHoverCard, { immediate: true });
  });

  lifecycle.listen(
    ownerDocument,
    ownerDocument,
    'scroll',
    () => {
      if (activeHoverCard) closeHoverCard(activeHoverCard, { immediate: true });
    },
    { passive: true, capture: true }
  );

  const view = ownerDocument.defaultView || (typeof window === 'undefined' ? null : window);
  lifecycle.listen(ownerDocument, view, 'resize', () => {
    if (!activeHoverCard) return;
    if (hoverCardAnchorSupported(activeHoverCard.card)) return;
    positionHoverCardFallback(activeHoverCard.card, activeHoverCard.trigger);
  });
}

export function enhance(root) {
  const ownerDocument =
    root?.nodeType === 9
      ? root
      : root?.ownerDocument || (typeof document === 'undefined' ? null : document);
  if (!ownerDocument) return;
  installGlobalListeners(ownerDocument);
  initHoverCards(root);
}

export function destroy(root) {
  lifecycle.destroy(root);
  queryAll(root, '[data-hover-card-trigger]').forEach(cleanupHoverCardTrigger);
  rebindHoverCardTargets();
}

export const behavior = { name: 'hover-card', enhance, destroy };
