// -- Time Field -------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('time-field');

const TIME_PARTS = {
  hour: { minimum: 1, maximum: 12, fallback: 12, label: 'Hour' },
  minute: { minimum: 0, maximum: 59, fallback: 0, label: 'Minute' }
};

function findPart(root, name) {
  return root.querySelector(`[data-time-part="${name}"], .time-field-${name}`);
}

function numericValue(field, fallback) {
  const digits = String(field.value || '').replace(/[^0-9]/g, '');
  if (!digits) return fallback;
  const value = Number.parseInt(digits, 10);
  return Number.isFinite(value) ? value : fallback;
}

function clamp(value, minimum, maximum) {
  return Math.max(minimum, Math.min(maximum, value));
}

function padded(value) {
  return String(value).padStart(2, '0');
}

function filterNumeric(field) {
  const next = String(field.value || '')
    .replace(/[^0-9]/g, '')
    .slice(0, 2);
  if (field.value !== next) field.value = next;
  return next;
}

function normalize(field, settings) {
  const digits = filterNumeric(field);
  if (!digits) {
    field.value = '';
    return null;
  }
  const value = clamp(Number.parseInt(digits, 10), settings.minimum, settings.maximum);
  field.value = padded(value);
  return value;
}

function segmentValue(field, settings) {
  const digits = String(field.value || '').replace(/[^0-9]/g, '');
  if (!digits) return { value: null, empty: true, valid: true };
  const value = Number.parseInt(digits, 10);
  const valid = Number.isFinite(value) && value >= settings.minimum && value <= settings.maximum;
  return { value: valid ? value : null, empty: false, valid };
}

const segmentMessages = new WeakMap();

function setSegmentValidity(field, state, settings) {
  if (typeof field.setCustomValidity !== 'function') return;
  if (field.validity?.customError && field.validationMessage !== segmentMessages.get(field)) return;
  const message =
    state.valid || state.empty
      ? ''
      : `${settings.label} must be between ${settings.minimum} and ${settings.maximum}.`;
  field.setCustomValidity(message);
  segmentMessages.set(field, message);
}

export function enhance(root) {
  queryAll(root, '.time-field').forEach((timeField) => {
    timeField.dataset.init = '';
    if (lifecycle.has(timeField)) return;
    timeField.dataset.mewaTimeFieldInit = '';

    const hour = findPart(timeField, 'hour');
    const minute = findPart(timeField, 'minute');
    const period = findPart(timeField, 'period');
    const submitted = findPart(timeField, 'value');
    const status = findPart(timeField, 'status');
    if (!hour || !minute || !period) {
      timeField.removeAttribute('data-mewa-time-field-init');
      return;
    }

    if (submitted) submitted.disabled = false;
    lifecycle.add(timeField, () => {
      for (const field of [hour, minute]) {
        if (segmentMessages.has(field) && field.validationMessage === segmentMessages.get(field))
          field.setCustomValidity('');
        segmentMessages.delete(field);
      }
    });

    const announce = (source, emit = true) => {
      const hourState = segmentValue(hour, TIME_PARTS.hour);
      const minuteState = segmentValue(minute, TIME_PARTS.minute);
      const hourValue = hourState.value;
      const minuteValue = minuteState.value;
      const periodValue = period.value === 'PM' ? 'PM' : 'AM';
      if (period.value !== periodValue) period.value = periodValue;
      setSegmentValidity(hour, hourState, TIME_PARTS.hour);
      setSegmentValidity(minute, minuteState, TIME_PARTS.minute);

      const complete = hourValue !== null && minuteValue !== null;
      const valid = hourState.valid && minuteState.valid;
      const offset = periodValue === 'PM' ? 12 : 0;
      const hour24 = complete ? (hourValue % 12) + offset : null;
      const serialized = complete ? `${padded(hour24)}:${padded(minuteValue)}` : '';
      const display = !valid
        ? 'Enter a valid hour and minute.'
        : complete
          ? `${padded(hourValue)}:${padded(minuteValue)} ${periodValue}`
          : 'Enter an hour and minute.';

      if (submitted) submitted.value = serialized;
      if (status) {
        status.value = display;
        status.textContent = display;
      }

      if (emit) {
        timeField.dispatchEvent(
          new CustomEvent('time-field:change', {
            bubbles: true,
            detail: {
              value: serialized,
              hour: hourValue === null ? '' : padded(hourValue),
              minute: minuteValue === null ? '' : padded(minuteValue),
              period: periodValue,
              source
            }
          })
        );
      }
      return { hourValue, minuteValue, periodValue, serialized, display };
    };

    const handleInput = (field, name) => {
      const digits = filterNumeric(field);
      if (name === 'hour' && digits.length === 2) minute.focus();
      announce('input');
    };

    const handleChange = (field, name) => {
      if (name) normalize(field, TIME_PARTS[name]);
      announce('change');
    };

    const handleBlur = (field, name) => {
      normalize(field, TIME_PARTS[name]);
      announce('blur');
    };

    const step = (field, name, amount) => {
      const settings = TIME_PARTS[name];
      const current = clamp(
        numericValue(field, settings.fallback),
        settings.minimum,
        settings.maximum
      );
      const range = settings.maximum - settings.minimum + 1;
      const next =
        ((((current - settings.minimum + amount) % range) + range) % range) + settings.minimum;
      field.value = padded(next);
      announce('keyboard');
    };

    const handleKeydown = (event, field, name) => {
      if (event.isComposing || field.matches(':disabled') || field.readOnly) return;
      if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
      event.preventDefault();
      step(field, name, event.key === 'ArrowUp' ? 1 : -1);
    };

    [
      [hour, 'hour'],
      [minute, 'minute']
    ].forEach(([field, name]) => {
      lifecycle.listen(timeField, field, 'input', (event) => {
        if (event.isComposing || field.matches(':disabled') || field.readOnly) return;
        handleInput(field, name);
      });
      lifecycle.listen(timeField, field, 'change', () => {
        if (!field.matches(':disabled') && !field.readOnly) handleChange(field, name);
      });
      lifecycle.listen(timeField, field, 'blur', () => {
        if (!field.matches(':disabled') && !field.readOnly) handleBlur(field, name);
      });
      lifecycle.listen(timeField, field, 'keydown', (event) => handleKeydown(event, field, name));
    });

    lifecycle.listen(timeField, period, 'input', () => handleChange(period, ''));
    lifecycle.listen(timeField, period, 'change', () => handleChange(period, ''));

    const form = hour.form;
    if (form) {
      lifecycle.reset(timeField, form, () => {
        normalize(hour, TIME_PARTS.hour);
        normalize(minute, TIME_PARTS.minute);
        announce('reset', false);
      });
    }

    normalize(hour, TIME_PARTS.hour);
    normalize(minute, TIME_PARTS.minute);
    announce('initial', false);
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'time-field', enhance, destroy };
