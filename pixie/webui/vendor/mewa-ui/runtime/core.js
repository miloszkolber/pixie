function defaultDocument() {
  return typeof document === 'undefined' ? null : document;
}

export function queryAll(root, selector) {
  const scope = root || defaultDocument();
  if (!scope) return [];

  const matches = [];
  if (scope.nodeType === 1 && scope.matches?.(selector)) matches.push(scope);
  if (typeof scope.querySelectorAll === 'function')
    matches.push(...scope.querySelectorAll(selector));
  return matches;
}

// Explicit ownership keeps document/form listeners attached to their component,
// even when the listener target lives outside the enhanced subtree.
export function createLifecycle(name) {
  const instances = new Map();
  const updates = new WeakMap();
  const marker = `data-mewa-${name}-init`;
  function add(owner, cleanup) {
    let cleanups = instances.get(owner);
    if (!cleanups) instances.set(owner, (cleanups = []));
    cleanups.push(cleanup);
    return cleanup;
  }
  function listen(owner, target, type, listener, options) {
    if (!target) return;
    target.addEventListener(type, listener, options);
    add(owner, () => target.removeEventListener(type, listener, options));
  }
  function destroy(root) {
    const errors = [];
    for (const [owner, cleanups] of instances) {
      if (owner !== root && !root?.contains?.(owner)) continue;
      instances.delete(owner);
      for (const cleanup of cleanups.reverse()) {
        try {
          cleanup();
        } catch (error) {
          errors.push(error);
        }
      }
      owner.removeAttribute?.(marker);
      const markers = owner.getAttributeNames?.() || [];
      if (
        !markers.some((attribute) =>
          /^data-(?:mewa-.+|hover-card|context-menu)-init$/.test(attribute)
        )
      ) {
        owner.removeAttribute?.('data-init');
      }
    }
    if (errors.length) throw new AggregateError(errors, `Failed to dispose ${name}`);
  }
  function reset(owner, form, callback) {
    listen(owner, form, 'reset', (event) => {
      queueMicrotask(() => {
        if (!event.defaultPrevented && instances.has(owner)) callback();
      });
    });
  }
  function onUpdate(owner, callback) {
    updates.set(owner, callback);
    add(owner, () => updates.delete(owner));
  }
  function refresh(root = defaultDocument(), ancestors = true) {
    for (const owner of instances.keys()) {
      if (owner === root || root?.contains?.(owner) || (ancestors && owner.contains?.(root)))
        updates.get(owner)?.();
    }
  }
  return { add, listen, destroy, reset, onUpdate, refresh, has: (owner) => instances.has(owner) };
}

export function createController(behavior, root, options) {
  if (!behavior || typeof behavior.enhance !== 'function') {
    throw new TypeError('A Mewa behavior with an enhance function is required.');
  }
  if (!root) throw new TypeError('A root element is required.');

  const lease = acquireBehavior(behavior, root, options);
  let active = true;

  return {
    element: root,
    update(nextOptions) {
      if (!active) return;
      lease.update(nextOptions);
    },
    destroy() {
      if (!active) return;
      active = false;
      lease.destroy();
    }
  };
}

const leases = new WeakMap();

// Generated dependency compositions share the raw behavior and its lifetime.
export function acquireBehavior(behavior, root, options) {
  let roots = leases.get(behavior);
  if (!roots) leases.set(behavior, (roots = new WeakMap()));
  let entry = roots.get(root);
  if (!entry) {
    entry = { count: 0, options, state: undefined };
    try {
      entry.state = behavior.enhance(root, options);
    } catch (error) {
      try {
        behavior.destroy?.(root);
      } catch (cleanupError) {
        throw new AggregateError([error, cleanupError], 'Behavior setup and rollback failed');
      }
      throw error;
    }
    roots.set(root, entry);
  }
  entry.count += 1;
  let active = true;
  return {
    get state() {
      return entry.state;
    },
    update(nextOptions = entry.options) {
      if (!active) return;
      entry.state = behavior.enhance(root, nextOptions) ?? entry.state;
      entry.options = nextOptions;
    },
    destroy() {
      if (!active) return;
      active = false;
      if (--entry.count) return;
      roots.delete(root);
      behavior.destroy?.(root, entry.state);
    }
  };
}
