// -- Navigation Menu -----------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('navigation-menu');
const targets = new WeakMap();

export function enhance(root) {
  queryAll(root, '.nav-menu').forEach((nav) => {
    nav.dataset.init = '';
    nav.dataset.mewaNavigationMenuInit = '';
    nav.querySelectorAll('.nav-menu-trigger[popovertarget]').forEach((trigger) => {
      const id = trigger.getAttribute('popovertarget');
      const content = nav.ownerDocument.getElementById(id);
      if (targets.get(trigger) === content && content) return;
      lifecycle.destroy(trigger);
      if (!content) return;
      const previousTriggerAnchor = trigger.style.anchorName;
      const previousTargetAnchor = content.style.positionAnchor;
      targets.set(trigger, content);
      lifecycle.add(trigger, () => {
        trigger.style.anchorName = previousTriggerAnchor;
        content.style.positionAnchor = previousTargetAnchor;
        targets.delete(trigger);
      });

      const anchorId = `--nav-menu-${id}`;
      trigger.style.anchorName = anchorId;
      content.style.positionAnchor = anchorId;
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'navigation-menu', enhance, destroy };
