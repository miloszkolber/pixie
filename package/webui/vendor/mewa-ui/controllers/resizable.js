/* -- Resizable component ----------------------------------------- */

import { queryAll } from '../runtime/core.js';


const DEFAULT_MIN = 0;
const DEFAULT_MAX = 100;
const DEFAULT_VALUE = 35;
const DEFAULT_STEP = 1;
const DEFAULT_PAGE_STEP = 10;
const ENHANCED_ATTRIBUTES = [
  'tabindex',
  'aria-label',
  'aria-controls',
  'aria-orientation',
  'aria-valuemin',
  'aria-valuemax',
  'aria-valuenow',
  'aria-valuetext'
];
const instances = new WeakMap();
const initializingRoots = new WeakSet();
const resizeFallbacks = new WeakMap();

function registerResizeFallback(ownerDocument, callback) {
  const view = ownerDocument.defaultView || (typeof window === 'undefined' ? null : window);
  if (!view) return () => {};

  let fallback = resizeFallbacks.get(ownerDocument);
  if (!fallback) {
    const callbacks = new Set();
    const onResize = () => callbacks.forEach((entry) => entry());
    view.addEventListener('resize', onResize);
    fallback = { callbacks, onResize, view };
    resizeFallbacks.set(ownerDocument, fallback);
  }

  fallback.callbacks.add(callback);
  return () => {
    fallback.callbacks.delete(callback);
    if (fallback.callbacks.size) return;
    fallback.view.removeEventListener('resize', fallback.onResize);
    resizeFallbacks.delete(ownerDocument);
  };
}

function readNumber(element, attribute, fallback) {
  if (!element) return fallback;
  const value = Number.parseFloat(element.getAttribute(attribute) || '');
  return Number.isFinite(value) ? value : fallback;
}

function readConfiguredNumber(handle, root, dataAttribute, ariaAttribute, fallback) {
  for (const element of [handle, root]) {
    const dataValue = readNumber(element, dataAttribute, NaN);
    if (Number.isFinite(dataValue)) return dataValue;
  }
  for (const element of [handle, root]) {
    const ariaValue = readNumber(element, ariaAttribute, NaN);
    if (Number.isFinite(ariaValue)) return ariaValue;
  }
  return fallback;
}

function readConfiguredString(handle, root, attribute) {
  return handle.getAttribute(attribute) || root.getAttribute(attribute) || '';
}

function snapshotAttributes(element, attributes) {
  return new Map(attributes.map((attribute) => [attribute, element.getAttribute(attribute)]));
}

function restoreAttributes(element, snapshot) {
  snapshot.forEach((value, attribute) => {
    if (value === null) element.removeAttribute(attribute);
    else element.setAttribute(attribute, value);
  });
}

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value));
}

function roundValue(value) {
  return Math.round(value * 1000) / 1000;
}

function formatValue(value) {
  return String(roundValue(value));
}

function getPanels(group) {
  return Array.from(group.children)
    .filter((child) => child.matches('.resizable-panel'))
    .slice(0, 2);
}

function getOutput(root) {
  return Array.from(root.children).find((child) => child.matches('output')) || null;
}

function createOutput(root) {
  const output = root.ownerDocument.createElement('output');
  output.className = 'resizable-output';
  output.setAttribute('aria-live', 'polite');
  output.setAttribute('aria-atomic', 'true');
  root.append(output);
  return output;
}

function createPointerControls(root, output, valueLabel) {
  const controls = root.ownerDocument.createElement('div');
  controls.className = 'resizable-controls';
  controls.setAttribute('data-resizable-controls', '');
  controls.setAttribute('aria-label', `Resize ${valueLabel}`);

  const decrease = root.ownerDocument.createElement('button');
  decrease.className = 'resizable-step';
  decrease.setAttribute('type', 'button');
  decrease.setAttribute('data-resizable-decrease', '');
  decrease.setAttribute('aria-label', `Decrease ${valueLabel}`);
  decrease.textContent = '−';

  const increase = root.ownerDocument.createElement('button');
  increase.className = 'resizable-step';
  increase.setAttribute('type', 'button');
  increase.setAttribute('data-resizable-increase', '');
  increase.setAttribute('aria-label', `Increase ${valueLabel}`);
  increase.textContent = '+';

  controls.append(decrease, increase);
  root.insertBefore(controls, output);
  return { controls, decrease, increase };
}

export function enhance(scope) {
  const roots = queryAll(scope, '.resizable');
  const ancestor = scope?.nodeType === 1 ? scope.closest?.('.resizable') : null;
  if (ancestor) roots.push(ancestor);

  new Set(roots).forEach((root) => {
    if (instances.has(root) || initializingRoots.has(root)) return;
    initializingRoots.add(root);
    try {
      root.dataset.init = '';
      root.dataset.mewaResizableInit = '';

      const group = root.querySelector('.resizable-group');
      const handle = group?.querySelector('.resizable-handle');
      const panels = group ? getPanels(group) : [];
      if (!group || !handle || panels.length < 2) {
        root.removeAttribute('data-mewa-resizable-init');
        return;
      }

      const handleAttributes = snapshotAttributes(handle, ENHANCED_ATTRIBUTES);
      const groupOrientation = group.getAttribute('data-orientation');
      const panelStyles = {
        flexBasis: panels[0].style.flexBasis,
        flexGrow: panels[0].style.flexGrow,
        flexShrink: panels[0].style.flexShrink
      };

      const requestedOrientation =
        handle.getAttribute('aria-orientation') ||
        group.dataset.orientation ||
        root.dataset.orientation ||
        'vertical';
      const orientation = requestedOrientation === 'horizontal' ? 'horizontal' : 'vertical';
      const isHorizontal = orientation === 'horizontal';
      const dimension = isHorizontal ? 'height' : 'width';
      const coordinate = isHorizontal ? 'clientY' : 'clientX';
      const decreaseKey = isHorizontal ? 'ArrowUp' : 'ArrowLeft';
      const increaseKey = isHorizontal ? 'ArrowDown' : 'ArrowRight';

      handle.setAttribute('aria-orientation', orientation);
      group.dataset.orientation = orientation;
      if (!handle.hasAttribute('tabindex') && handle.tagName !== 'BUTTON') {
        handle.setAttribute('tabindex', '0');
      }

      let minimum = clamp(
        readConfiguredNumber(handle, root, 'data-value-min', 'aria-valuemin', DEFAULT_MIN),
        0,
        100
      );
      let maximum = clamp(
        readConfiguredNumber(handle, root, 'data-value-max', 'aria-valuemax', DEFAULT_MAX),
        0,
        100
      );
      if (maximum < minimum) [minimum, maximum] = [maximum, minimum];
      handle.setAttribute('aria-valuemin', formatValue(minimum));
      handle.setAttribute('aria-valuemax', formatValue(maximum));

      const requestedStep = readNumber(
        handle,
        'data-step',
        readNumber(root, 'data-step', DEFAULT_STEP)
      );
      const step = requestedStep > 0 ? requestedStep : DEFAULT_STEP;
      const requestedPageStep = readNumber(
        handle,
        'data-page-step',
        readNumber(root, 'data-page-step', DEFAULT_PAGE_STEP)
      );
      const pageStep =
        requestedPageStep >= step ? requestedPageStep : Math.max(step, DEFAULT_PAGE_STEP);

      const containerSize = () => group.getBoundingClientRect()[dimension] || 0;
      const panelSize = () => panels[0].getBoundingClientRect()[dimension] || 0;
      const initialValue = readConfiguredNumber(
        handle,
        root,
        'data-value-now',
        'aria-valuenow',
        NaN
      );
      const measuredValue =
        containerSize() > 0 ? (panelSize() / containerSize()) * 100 : DEFAULT_VALUE;
      let value = Number.isFinite(initialValue) ? initialValue : measuredValue;
      const initialLabel = handle.getAttribute('aria-label') || '';
      const valueLabel =
        handle.dataset.valueLabel ||
        root.dataset.valueLabel ||
        initialLabel.replace(/^resize\s+/i, '').trim() ||
        (isHorizontal ? 'Panel height' : 'Panel width');
      const accessibleLabel =
        readConfiguredString(handle, root, 'data-label') || initialLabel || `Resize ${valueLabel}`;
      const controls =
        readConfiguredString(handle, root, 'data-controls') ||
        handle.getAttribute('aria-controls') ||
        panels
          .map((panel) => panel.id)
          .filter(Boolean)
          .join(' ');

      handle.setAttribute('aria-label', accessibleLabel);
      if (controls) handle.setAttribute('aria-controls', controls);
      else handle.removeAttribute('aria-controls');

      const existingOutput = getOutput(root);
      const outputAttributes = existingOutput
        ? snapshotAttributes(existingOutput, ['class', 'aria-live', 'aria-atomic'])
        : null;
      const outputText = existingOutput?.textContent || '';
      const outputValue = existingOutput?.value || '';
      let output = existingOutput;
      if (!output) output = createOutput(root);
      output.classList.add('resizable-output');
      output.setAttribute('aria-live', output.getAttribute('aria-live') || 'polite');
      output.setAttribute('aria-atomic', output.getAttribute('aria-atomic') || 'true');

      const pointerControls = createPointerControls(root, output, valueLabel);

      function quantize(next) {
        const bounded = clamp(Number(next) || 0, minimum, maximum);
        const stepped = minimum + Math.round((bounded - minimum) / step) * step;
        return roundValue(clamp(stepped, minimum, maximum));
      }

      function announcement(next) {
        const text = `${valueLabel}: ${formatValue(next)} percent`;
        handle.setAttribute('aria-valuenow', formatValue(next));
        handle.setAttribute('aria-valuetext', text);
        output.value = text;
        output.textContent = text;
        const interactionDisabled =
          handle.disabled || handle.getAttribute('aria-disabled') === 'true';
        pointerControls.decrease.disabled = interactionDisabled || next <= minimum;
        pointerControls.increase.disabled = interactionDisabled || next >= maximum;
      }

      function setPanelBasis(next) {
        const size = containerSize();
        if (size <= 0) return;
        panels[0].style.flexBasis = `${(size * next) / 100}px`;
        panels[0].style.flexGrow = '0';
        panels[0].style.flexShrink = '0';
      }

      function setValue(next, source = 'programmatic', emit = true) {
        const nextValue = quantize(next);
        const changed = nextValue !== value;
        value = nextValue;
        setPanelBasis(value);
        announcement(value);
        if (changed && emit) {
          root.dispatchEvent(
            new CustomEvent('resizable-change', {
              bubbles: true,
              detail: {
                value,
                percentage: value,
                source,
                orientation,
                panel: panels[0]
              }
            })
          );
        }
        return changed;
      }

      function setPixelSize(size, source) {
        const container = containerSize();
        if (container <= 0) return false;
        const bounded = clamp(size, (container * minimum) / 100, (container * maximum) / 100);
        return setValue((bounded / container) * 100, source);
      }

      setValue(value, 'initial', false);

      function onKeydown(event) {
        if (handle.disabled || handle.getAttribute('aria-disabled') === 'true') return;

        let next;
        if (event.key === decreaseKey) next = value - step;
        if (event.key === increaseKey) next = value + step;
        if (event.key === 'PageDown') next = value - pageStep;
        if (event.key === 'PageUp') next = value + pageStep;
        if (event.key === 'Home') next = minimum;
        if (event.key === 'End') next = maximum;
        if (next === undefined) return;

        event.preventDefault();
        setValue(next, 'keyboard');
      }

      function onDecrease() {
        if (pointerControls.decrease.disabled) return;
        setValue(value - pageStep, 'pointer');
      }

      function onIncrease() {
        if (pointerControls.increase.disabled) return;
        setValue(value + pageStep, 'pointer');
      }

      handle.addEventListener('keydown', onKeydown);
      pointerControls.decrease.addEventListener('click', onDecrease);
      pointerControls.increase.addEventListener('click', onIncrease);

      let drag = null;

      function finishPointer() {
        if (drag) {
          const pointerId = drag.pointerId;
          handle.removeEventListener('pointermove', movePointer);
          handle.removeEventListener('pointerup', finishPointer);
          handle.removeEventListener('pointercancel', finishPointer);
          if (handle.hasPointerCapture?.(pointerId)) {
            try {
              handle.releasePointerCapture(pointerId);
            } catch {
              // Pointer capture can disappear before cleanup runs.
            }
          }
        }
        handle.removeAttribute('data-resizing');
        root.removeAttribute('data-resizing');
        drag = null;
      }

      function movePointer(event) {
        if (!drag || event.pointerId !== drag.pointerId) return;
        const position = Number(event[coordinate]);
        if (!Number.isFinite(position)) return;
        setPixelSize(drag.initialSize + position - drag.start, 'pointer');
      }

      function startPointer(event) {
        if (handle.disabled || handle.getAttribute('aria-disabled') === 'true') return;
        if (event.button !== undefined && event.button !== 0) return;
        if (event.isPrimary === false) return;

        event.preventDefault();
        finishPointer();
        const pointerId = Number.isFinite(event.pointerId) ? event.pointerId : 1;
        drag = {
          pointerId,
          start: Number(event[coordinate]) || 0,
          initialSize: panelSize() || (containerSize() * value) / 100
        };
        handle.setAttribute('data-resizing', '');
        root.setAttribute('data-resizing', '');
        try {
          handle.setPointerCapture?.(pointerId);
        } catch {
          // Pointer capture can fail for synthetic events or an inactive pointer.
        }
        handle.addEventListener('pointermove', movePointer);
        handle.addEventListener('pointerup', finishPointer);
        handle.addEventListener('pointercancel', finishPointer);
      }

      handle.addEventListener('pointerdown', startPointer);

      let active = true;
      const updateLayout = () => {
        if (active) setPanelBasis(value);
      };
      let resizeObserver = null;
      let removeResizeFallback = null;
      const ResizeObserverConstructor =
        root.ownerDocument.defaultView?.ResizeObserver ||
        (typeof ResizeObserver === 'function' ? ResizeObserver : null);
      if (ResizeObserverConstructor) {
        resizeObserver = new ResizeObserverConstructor(updateLayout);
        resizeObserver.observe(group);
      } else {
        removeResizeFallback = registerResizeFallback(root.ownerDocument, updateLayout);
      }

      function cleanup() {
        active = false;
        finishPointer();
        handle.removeEventListener('keydown', onKeydown);
        handle.removeEventListener('pointerdown', startPointer);
        pointerControls.decrease.removeEventListener('click', onDecrease);
        pointerControls.increase.removeEventListener('click', onIncrease);
        pointerControls.controls.remove();
        if (resizeObserver) resizeObserver.disconnect();
        else removeResizeFallback?.();
        restoreAttributes(handle, handleAttributes);
        if (groupOrientation === null) group.removeAttribute('data-orientation');
        else group.setAttribute('data-orientation', groupOrientation);
        panels[0].style.flexBasis = panelStyles.flexBasis;
        panels[0].style.flexGrow = panelStyles.flexGrow;
        panels[0].style.flexShrink = panelStyles.flexShrink;
        if (existingOutput) {
          restoreAttributes(existingOutput, outputAttributes);
          existingOutput.value = outputValue;
          existingOutput.textContent = outputText;
        } else {
          output.remove();
        }
        root.removeAttribute('data-resizing');
        root.removeAttribute('data-mewa-resizable-init');
        instances.delete(root);
      }

      instances.set(root, cleanup);
    } finally {
      initializingRoots.delete(root);
    }
  });
}

export function destroy(root) {
  queryAll(root, '.resizable').forEach((resizable) => {
    instances.get(resizable)?.();
  });
}

export const behavior = { name: 'resizable', enhance, destroy };
