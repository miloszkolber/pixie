function assertBehavior(behavior) {
  if (!behavior || typeof behavior.enhance !== 'function') {
    throw new TypeError('mewa() requires a Mewa behavior with an enhance() function');
  }
}

/**
 * Create a Svelte attachment for one dependency-aware Mewa behavior.
 *
 * Use the behavior exported from a generated `mewa-ui/components/*.js` entry.
 * Svelte calls the returned cleanup function when the element leaves the DOM.
 */
export function mewa(behavior, options) {
  assertBehavior(behavior);

  return (element) => {
    let state = behavior.enhance(element, options);
    let active = true;

    return () => {
      if (!active) return;
      active = false;
      behavior.destroy?.(element, state);
      state = undefined;
    };
  };
}

export const attachBehavior = mewa;
