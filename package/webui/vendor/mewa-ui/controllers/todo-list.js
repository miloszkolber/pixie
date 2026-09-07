// -- Todo List -------------------------------------------------

import { queryAll } from '../runtime/core.js';


const rootSelector = '.todo-list';

function directItems(root) {
  const list = root.querySelector('.todo-list-items');
  if (!list) return [];
  return Array.from(list.children).filter((item) => item.matches('[data-todo-item], .todo-item'));
}

function updateProgress(root) {
  const items = directItems(root);
  const completed = items.filter((item) => item.dataset.status === 'done').length;
  const output = root.querySelector('[data-todo-progress]');
  if (output) output.textContent = `${completed} of ${items.length} complete`;
  root.dispatchEvent(
    new CustomEvent('todo-list:progress', {
      bubbles: true,
      detail: { completed, total: items.length }
    })
  );
}

export function enhance(root) {
  queryAll(root, rootSelector).forEach((todoList) => {
    if (todoList._todoListObserver) return;
    todoList.dataset.init = '';
    todoList.dataset.mewaTodoListInit = '';
    updateProgress(todoList);

    const list = todoList.querySelector('.todo-list-items');
    if (!list || typeof MutationObserver !== 'function') return;

    const observer = new MutationObserver(() => updateProgress(todoList));
    observer.observe(list, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['data-status']
    });
    todoList._todoListObserver = observer;
  });
}

export function destroy(root) {
  queryAll(root, '.todo-list[data-mewa-todo-list-init]').forEach((todoList) => {
    todoList._todoListObserver?.disconnect();
    delete todoList._todoListObserver;
    todoList.removeAttribute('data-mewa-todo-list-init');
  });
}

export const behavior = { name: 'todo-list', enhance, destroy };
