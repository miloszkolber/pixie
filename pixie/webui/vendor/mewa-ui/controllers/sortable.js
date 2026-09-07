// -- Sortable -------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('sortable');

export function enhance(root) {
  lifecycle.refresh(root);
  queryAll(root, '.sortable').forEach((list) => {
    list.dataset.init = '';
    if (lifecycle.has(list)) return;
    list.dataset.mewaSortableInit = '';
    const doc = list.ownerDocument;

    const isHorizontal = list.dataset.orientation === 'horizontal';
    const NEXT_KEY = isHorizontal ? 'ArrowRight' : 'ArrowDown';
    const PREV_KEY = isHorizontal ? 'ArrowLeft' : 'ArrowUp';

    let liveRegion = list.nextElementSibling?.matches('.sortable-live')
      ? list.nextElementSibling
      : null;
    if (!liveRegion) {
      liveRegion = doc.createElement('span');
      liveRegion.className = 'sortable-live';
      liveRegion.setAttribute('aria-live', 'polite');
      liveRegion.setAttribute('role', 'status');
      if (list.parentElement) list.parentElement.insertBefore(liveRegion, list.nextSibling);
      else list.after(liveRegion);
      lifecycle.add(list, () => liveRegion.remove());
    }

    function announce(message) {
      liveRegion.textContent = message;
    }

    function getAllItems() {
      return Array.from(list.querySelectorAll(':scope > .sortable-item'));
    }

    function getItems() {
      return getAllItems().filter((item) => item.getAttribute('aria-disabled') !== 'true');
    }

    function getActiveItem() {
      return list.querySelector(':scope > .sortable-item[data-active]');
    }

    function markActive(item, { focus = true } = {}) {
      getAllItems().forEach((candidate) => {
        candidate.removeAttribute('data-active');
        candidate.setAttribute('tabindex', '-1');
      });
      if (!item) return;
      item.setAttribute('data-active', '');
      item.setAttribute('tabindex', '0');
      if (focus) item.focus();
    }

    function getItemLabel(item) {
      const clone = item.cloneNode(true);
      clone.querySelector('.sortable-handle')?.remove();
      clone.querySelector('.sortable-actions')?.remove();
      return clone.textContent.trim();
    }

    function dispatchChange(item, source) {
      const items = getItems();
      const index = items.indexOf(item);
      announce(`${getItemLabel(item)}, moved to position ${index + 1} of ${items.length}.`);
      list.dispatchEvent(
        new CustomEvent('sortable-change', {
          bubbles: true,
          detail: { item, index, source }
        })
      );
    }

    function moveItem(item, direction, source) {
      const items = getItems();
      const index = items.indexOf(item);
      if (index < 0) return false;
      if (direction < 0 && index === 0) return false;
      if (direction > 0 && index === items.length - 1) return false;

      if (direction < 0) {
        list.insertBefore(item, items[index - 1]);
      } else {
        const next = items[index + 1];
        list.insertBefore(item, next.nextSibling);
      }

      updateStepControls();
      dispatchChange(item, source);
      return true;
    }

    function createStepControls(item) {
      if (item.getAttribute('aria-disabled') === 'true') return;
      if (item.querySelector(':scope > .sortable-actions')) return;

      const actions = doc.createElement('span');
      actions.className = 'sortable-actions';

      const previous = doc.createElement('button');
      previous.type = 'button';
      previous.className = 'sortable-step';
      previous.setAttribute('data-sortable-decrease', '');
      previous.textContent = isHorizontal ? '←' : '↑';

      const next = doc.createElement('button');
      next.type = 'button';
      next.className = 'sortable-step';
      next.setAttribute('data-sortable-increase', '');
      next.textContent = isHorizontal ? '→' : '↓';

      actions.append(previous, next);
      item.append(actions);
      lifecycle.add(list, () => actions.remove());
    }

    function updateStepControls() {
      const items = getItems();
      items.forEach((item, index) => {
        const label = getItemLabel(item);
        const previous = item.querySelector('[data-sortable-decrease]');
        const next = item.querySelector('[data-sortable-increase]');
        if (previous) {
          previous.disabled = index === 0;
          previous.setAttribute('aria-label', `Move ${label} ${isHorizontal ? 'left' : 'up'}`);
        }
        if (next) {
          next.disabled = index === items.length - 1;
          next.setAttribute('aria-label', `Move ${label} ${isHorizontal ? 'right' : 'down'}`);
        }
      });
    }

    getAllItems().forEach((item) => {
      item.setAttribute('tabindex', '-1');
      createStepControls(item);
    });
    markActive(getItems()[0], { focus: false });
    updateStepControls();

    lifecycle.onUpdate(list, () => {
      getAllItems().forEach(createStepControls);
      const active = getActiveItem();
      markActive(active || getItems()[0], { focus: false });
      updateStepControls();
    });
    let dragged = null;

    lifecycle.listen(list, list, 'dragstart', (event) => {
      const item = event.target.closest('.sortable-item');
      if (!item || item.parentElement !== list || item.getAttribute('aria-disabled') === 'true')
        return;
      dragged = item;
      item.setAttribute('data-dragging', '');
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/plain', '');
    });

    lifecycle.listen(list, list, 'dragend', (event) => {
      const item = event.target.closest('.sortable-item');
      if (!item || item.parentElement !== list || item.getAttribute('aria-disabled') === 'true')
        return;
      item.removeAttribute('data-dragging');
      list
        .querySelectorAll('[data-over]')
        .forEach((candidate) => candidate.removeAttribute('data-over'));
      dragged = null;
    });

    lifecycle.listen(list, list, 'dragover', (event) => {
      const item = event.target.closest('.sortable-item');
      if (!item || item.parentElement !== list || item.getAttribute('aria-disabled') === 'true')
        return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      if (!dragged || dragged === item) return;
      const rect = item.getBoundingClientRect();
      const midpoint = isHorizontal ? rect.left + rect.width / 2 : rect.top + rect.height / 2;
      const pointer = isHorizontal ? event.clientX : event.clientY;
      list.querySelectorAll('[data-over]').forEach((candidate) => {
        if (candidate !== item) candidate.removeAttribute('data-over');
      });
      item.setAttribute('data-over', pointer < midpoint ? 'before' : 'after');
    });

    lifecycle.listen(list, list, 'dragleave', (event) => {
      const item = event.target.closest('.sortable-item');
      if (!item || item.parentElement !== list || item.getAttribute('aria-disabled') === 'true')
        return;
      item.removeAttribute('data-over');
    });

    lifecycle.listen(list, list, 'drop', (event) => {
      const item = event.target.closest('.sortable-item');
      if (!item || item.parentElement !== list || item.getAttribute('aria-disabled') === 'true')
        return;
      event.preventDefault();
      const position = item.getAttribute('data-over');
      item.removeAttribute('data-over');
      if (!dragged || dragged === item) return;

      if (position === 'before') list.insertBefore(dragged, item);
      else list.insertBefore(dragged, item.nextSibling);

      updateStepControls();
      markActive(dragged);
      dispatchChange(dragged, 'drag');
    });

    lifecycle.listen(list, list, 'click', (event) => {
      const control = event.target.closest('.sortable-step');
      if (!control || control.disabled) return;
      const item = control.closest('.sortable-item');
      if (!item || item.getAttribute('aria-disabled') === 'true') return;
      const direction = control.hasAttribute('data-sortable-decrease') ? -1 : 1;
      if (moveItem(item, direction, 'pointer')) {
        markActive(item, { focus: false });
        control.focus();
      }
    });

    lifecycle.listen(list, list, 'keydown', (event) => {
      if (event.target.closest('.sortable-step')) return;
      const active = getActiveItem() || list.querySelector(':scope > .sortable-item[tabindex="0"]');
      if (!active) return;
      const items = getItems();
      const index = items.indexOf(active);

      if (event.key === NEXT_KEY && !event.altKey) {
        event.preventDefault();
        if (items[index + 1]) markActive(items[index + 1]);
      } else if (event.key === PREV_KEY && !event.altKey) {
        event.preventDefault();
        if (items[index - 1]) markActive(items[index - 1]);
      } else if (event.key === 'Home') {
        event.preventDefault();
        if (items.length) markActive(items[0]);
      } else if (event.key === 'End') {
        event.preventDefault();
        if (items.length) markActive(items[items.length - 1]);
      } else if (event.key === NEXT_KEY && event.altKey) {
        event.preventDefault();
        if (moveItem(active, 1, 'keyboard')) markActive(active);
      } else if (event.key === PREV_KEY && event.altKey) {
        event.preventDefault();
        if (moveItem(active, -1, 'keyboard')) markActive(active);
      }
    });

    lifecycle.listen(list, list, 'focusin', (event) => {
      const item = event.target.closest('.sortable-item');
      if (!item || !list.contains(item)) return;
      if (event.target.closest('.sortable-step')) markActive(item, { focus: false });
      else if (event.target === item) markActive(item, { focus: false });
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'sortable', enhance, destroy };
