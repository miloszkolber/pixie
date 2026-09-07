// -- Tree View ------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('tree-view');

function isBranchOpen(details) {
  return typeof details.open === 'boolean' ? details.open : details.hasAttribute('open');
}

function isVisible(item) {
  if (typeof item.checkVisibility === 'function') return item.checkVisibility();
  if (item.hidden || item.getAttribute('aria-hidden') === 'true') return false;

  // checkVisibility() is not available in every supported engine. A closed
  // details ancestor is the important visibility boundary for this tree.
  for (let parent = item.parentElement; parent; parent = parent.parentElement) {
    if (parent.hidden || parent.getAttribute('aria-hidden') === 'true') return false;
    if (parent.matches('details.tree-branch') && !isBranchOpen(parent)) return false;
  }
  return true;
}

function getItems(tree) {
  return Array.from(tree.querySelectorAll('.tree-branch-trigger, .tree-leaf'));
}

function getVisibleItems(tree) {
  return getItems(tree).filter(isVisible);
}

function getTreeItem(item) {
  return item.matches('[role="treeitem"]') ? item : item.closest('li.tree-item[role="treeitem"]');
}

function getBranchDetails(treeitem) {
  if (!treeitem) return null;
  if (treeitem.matches('details.tree-branch')) return treeitem;
  if (treeitem.matches('.tree-branch-trigger')) return treeitem.closest('details.tree-branch');
  return (
    Array.from(treeitem.children).find((child) => child.matches('details.tree-branch')) || null
  );
}

function getBranchTrigger(treeitem) {
  const details = getBranchDetails(treeitem);
  if (treeitem?.matches('.tree-branch-trigger')) return treeitem;
  if (!details) return null;
  return (
    Array.from(details.children).find((child) => child.matches('.tree-branch-trigger')) || null
  );
}

function getBranchGroup(details) {
  if (!details) return null;
  return (
    Array.from(details.children).find((child) => child.getAttribute('role') === 'group') || null
  );
}

function getChildItems(treeitem) {
  const group = getBranchGroup(getBranchDetails(treeitem));
  return group ? Array.from(group.children).map(getItemControl).filter(Boolean) : [];
}

function getParentTreeItem(treeitem) {
  const listItem = treeitem?.closest('li.tree-item');
  const parentDetails = listItem?.parentElement?.closest('details.tree-branch');
  return parentDetails ? getItemControl(parentDetails.parentElement) : null;
}

function getItemControl(treeitem) {
  if (!treeitem) return null;
  if (treeitem.matches('.tree-branch-trigger, .tree-leaf')) return treeitem;
  return (
    getBranchTrigger(treeitem) ||
    Array.from(treeitem.children).find((child) => child.matches('.tree-leaf')) ||
    null
  );
}

function normalizeTreeItems(tree) {
  tree.querySelectorAll('li.tree-item').forEach((listItem) => {
    const details = Array.from(listItem.children).find((child) =>
      child.matches('details.tree-branch')
    );
    const control = details
      ? Array.from(details.children).find((child) => child.matches('.tree-branch-trigger'))
      : Array.from(listItem.children).find((child) => child.matches('.tree-leaf'));
    if (!control) return;

    const expanded =
      listItem.getAttribute('aria-expanded') ?? control.getAttribute('aria-expanded');
    listItem.setAttribute('role', 'treeitem');
    if (expanded !== null && details) listItem.setAttribute('aria-expanded', expanded);
    control.removeAttribute('role');
    control.removeAttribute('aria-expanded');
  });
}

export function enhance(root) {
  lifecycle.refresh(root);
  queryAll(root, '.tree[role="tree"]').forEach((tree) => {
    tree.dataset.init = '';
    if (lifecycle.has(tree)) return;
    tree.dataset.mewaTreeViewInit = '';
    normalizeTreeItems(tree);
    const items = getItems(tree);
    if (!items.length) return;

    const visibleItems = () => getVisibleItems(tree);
    const setRoving = (active) => {
      const visible = visibleItems();
      const next =
        active && visible.includes(active)
          ? active
          : visible.find((item) => item.getAttribute('tabindex') === '0') || visible[0];
      getItems(tree).forEach((item) => {
        item.setAttribute('tabindex', item === next ? '0' : '-1');
      });
      return next;
    };
    const focusItem = (item) => {
      if (!item || !isVisible(item)) return;
      setRoving(item);
      item.focus();
    };
    const syncBranch = (details) => {
      const trigger = Array.from(details.children).find((child) =>
        child.matches('.tree-branch-trigger')
      );
      const treeitem = trigger?.closest('li.tree-item');
      if (!treeitem) return;
      treeitem.setAttribute('aria-expanded', String(isBranchOpen(details)));
    };

    setRoving();

    const refresh = () => {
      normalizeTreeItems(tree);
      tree.querySelectorAll('.tree-branch').forEach(syncBranch);
      setRoving();
    };
    lifecycle.onUpdate(tree, refresh);
    refresh();
    lifecycle.listen(
      tree,
      tree,
      'toggle',
      (event) => {
        const details = event.target;
        if (!details.matches('.tree-branch') || details.closest('[role="tree"]') !== tree) return;
        syncBranch(details);
        if (!isBranchOpen(details)) {
          const trigger = getBranchTrigger(details);
          const active = tree.ownerDocument.activeElement;
          if (trigger && active && details.contains(active) && active !== trigger)
            focusItem(trigger);
        }
      },
      true
    );

    lifecycle.listen(tree, tree, 'focusin', (event) => {
      const target = event.target.closest('.tree-branch-trigger, .tree-leaf');
      if (target && tree.contains(target)) setRoving(target);
    });

    lifecycle.listen(tree, tree, 'keydown', (event) => {
      const target = event.target.closest('.tree-branch-trigger, .tree-leaf');
      if (!target || !tree.contains(target)) return;

      const currentItems = visibleItems();
      const index = currentItems.indexOf(target);
      if (index === -1) return;

      let next = null;
      switch (event.key) {
        case 'ArrowDown':
          next = currentItems[index + 1];
          break;
        case 'ArrowUp':
          next = currentItems[index - 1];
          break;
        case 'Home':
          next = currentItems[0];
          break;
        case 'End':
          next = currentItems[currentItems.length - 1];
          break;
        case 'ArrowRight': {
          const details = target.matches('.tree-branch-trigger')
            ? target.closest('details.tree-branch')
            : null;
          if (details) {
            if (!isBranchOpen(details)) {
              event.preventDefault();
              details.open = true;
              syncBranch(details);
              return;
            }
            const child = getChildItems(getTreeItem(target)).find((item) => {
              const control = getItemControl(item);
              return control && isVisible(control);
            });
            next = getItemControl(child);
          }
          break;
        }
        case 'ArrowLeft': {
          const treeitem = getTreeItem(target);
          const details = target.matches('.tree-branch-trigger')
            ? target.closest('details.tree-branch')
            : null;
          if (details && isBranchOpen(details)) {
            event.preventDefault();
            details.open = false;
            syncBranch(details);
            setRoving(target);
            return;
          }
          next = getItemControl(getParentTreeItem(treeitem));
          break;
        }
        default:
          return;
      }

      event.preventDefault();
      focusItem(next);
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'tree-view', enhance, destroy };
