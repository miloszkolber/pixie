// -- Popover --------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('popover');
const targets = new WeakMap();

function initPopoverTrigger(trigger) {
  const previous = targets.get(trigger);
  const current = trigger.ownerDocument.getElementById(trigger.getAttribute('popovertarget'));
  if (previous === current && current) return;
  lifecycle.destroy(trigger);
  trigger.dataset.init = '';
  trigger.dataset.mewaPopoverInit = '';
  const id = trigger.getAttribute('popovertarget');
  const popover = trigger.ownerDocument.getElementById(id);
  if (!popover || !popover.classList.contains('popover')) {
    delete trigger.dataset.mewaPopoverInit;
    return;
  }

  const previousTriggerAnchor = trigger.style.anchorName;
  const previousTargetAnchor = popover.style.positionAnchor;
  targets.set(trigger, popover);
  lifecycle.add(trigger, () => {
    trigger.style.anchorName = previousTriggerAnchor;
    popover.style.positionAnchor = previousTargetAnchor;
    targets.delete(trigger);
  });
  const anchorId = `--popover-${id}`;
  trigger.style.anchorName = anchorId;
  popover.style.positionAnchor = anchorId;
}

export function enhance(root) {
  const popovers = queryAll(root, '.popover[id]');
  queryAll(root, '[popovertarget]').forEach(initPopoverTrigger);
  if (popovers.length) {
    queryAll(popovers[0].ownerDocument, '[popovertarget]').forEach(initPopoverTrigger);
  }
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'popover', enhance, destroy };
