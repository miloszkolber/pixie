// -- Color Picker -----------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('color-picker');

const HEX_PATTERN = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i;

function normalizeHex(raw) {
  const match = HEX_PATTERN.exec(raw.trim());
  if (!match) return null;

  const digits = match[1];
  const expanded =
    digits.length === 3 ? Array.from(digits, (digit) => `${digit}${digit}`).join('') : digits;

  return `#${expanded.toLowerCase()}`;
}

export function enhance(scope) {
  lifecycle.refresh(scope, false);
  queryAll(scope, '.color-picker').forEach((root) => {
    root.dataset.init = '';
    if (lifecycle.has(root)) return;
    root.dataset.mewaColorPickerInit = '';

    const colorInput = root.querySelector('.color-picker-input[type="color"]');
    const hexInput = root.querySelector('[data-color-picker-hex]');
    if (!colorInput || !hexInput) {
      root.removeAttribute('data-mewa-color-picker-init');
      return;
    }

    let valueAtFocus = colorInput.value;
    let updatingFromHex = false;

    const syncFromColor = () => {
      const normalized = normalizeHex(colorInput.value) || '#000000';
      colorInput.value = normalized;
      hexInput.value = normalized;
      hexInput.removeAttribute('aria-invalid');
    };

    const syncDisabled = () => {
      hexInput.disabled = colorInput.disabled;
    };

    lifecycle.reset(root, colorInput.form, syncFromColor);
    lifecycle.onUpdate(root, () => {
      syncFromColor();
      syncDisabled();
    });
    syncFromColor();
    syncDisabled();
    hexInput.hidden = false;
    root.dataset.enhanced = '';

    lifecycle.listen(root, colorInput, 'input', () => {
      if (!updatingFromHex) syncFromColor();
    });
    lifecycle.listen(root, colorInput, 'change', syncFromColor);

    lifecycle.listen(root, hexInput, 'focus', () => {
      valueAtFocus = colorInput.value;
    });

    lifecycle.listen(root, hexInput, 'input', () => {
      if (colorInput.matches(':disabled') || hexInput.readOnly) return;
      const normalized = normalizeHex(hexInput.value);
      const draftIsInvalid = hexInput.value !== '' && !normalized;
      if (draftIsInvalid) hexInput.setAttribute('aria-invalid', 'true');
      else hexInput.removeAttribute('aria-invalid');

      if (!normalized || normalized === colorInput.value) return;
      colorInput.value = normalized;
      updatingFromHex = true;
      colorInput.dispatchEvent(new Event('input', { bubbles: true }));
      updatingFromHex = false;
    });

    lifecycle.listen(root, hexInput, 'blur', () => {
      const normalized = normalizeHex(hexInput.value);
      if (!normalized) {
        syncFromColor();
        return;
      }

      hexInput.value = normalized;
      hexInput.removeAttribute('aria-invalid');
      if (colorInput.value !== valueAtFocus) {
        colorInput.dispatchEvent(new Event('change', { bubbles: true }));
      }
    });

    lifecycle.listen(root, hexInput, 'keydown', (event) => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      hexInput.blur();
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'color-picker', enhance, destroy };
