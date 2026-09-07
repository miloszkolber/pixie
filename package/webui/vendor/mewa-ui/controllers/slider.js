/* -- Slider component ------------------------------------------- */

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('slider');

function updateSliderValue(el) {
  const min = parseFloat(el.min || 0);
  const max = parseFloat(el.max || 100);
  const value = parseFloat(el.value);
  const percent = max === min ? 0 : ((value - min) / (max - min)) * 100;
  el.style.setProperty('--slider-value', `${percent}%`);
}

export function enhance(root) {
  queryAll(root, '.slider').forEach((el) => {
    updateSliderValue(el);
    if (lifecycle.has(el)) return;
    el.dataset.init = '';
    if (lifecycle.has(el)) return;
    el.dataset.mewaSliderInit = '';
    updateSliderValue(el);
    lifecycle.listen(el, el, 'input', () => updateSliderValue(el));
    lifecycle.reset(el, el.form, () => updateSliderValue(el));
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'slider', enhance, destroy };
