// -- Date Range Picker ------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('date-range-picker');

const DATE_VALUE = /^\d{4}-\d{2}-\d{2}$/;
const ORDER_MESSAGE = 'End date must be on or after the start date.';
const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'long',
  timeZone: 'UTC'
});

const isDateValue = (value) => DATE_VALUE.test(value);

const maxDateValue = (...values) =>
  values
    .filter(Boolean)
    .reduce((current, value) => (!current || value > current ? value : current), '');

const minDateValue = (...values) =>
  values
    .filter(Boolean)
    .reduce((current, value) => (!current || value < current ? value : current), '');

const formatDate = (value) => {
  if (!isDateValue(value)) return '';
  const [year, month, day] = value.split('-').map(Number);
  return dateFormatter.format(new Date(Date.UTC(year, month - 1, day)));
};

const setConstraint = (input, name, value) => {
  input[name] = value || '';
};

const rangeDetail = (start, end, startValue, endValue, orderInvalid) => ({
  start: startValue || null,
  end: endValue || null,
  complete: Boolean(startValue && endValue),
  valid: !orderInvalid && start.validity.valid && end.validity.valid
});

const updateStatus = (status, startValue, endValue, orderInvalid, announceOrder) => {
  if (!status) return;

  // Values that were present before enhancement are allowed to remain dormant.
  // Do not put the chronological error into a live region until an interaction
  // has made that error actionable.
  if (orderInvalid && !announceOrder) return;

  if (!startValue && !endValue) {
    status.textContent = 'No dates selected';
    return;
  }

  if (orderInvalid) {
    status.textContent = ORDER_MESSAGE;
    return;
  }

  if (startValue && endValue) {
    status.textContent = `Selected ${formatDate(startValue)} to ${formatDate(endValue)}`;
    return;
  }

  status.textContent = startValue
    ? `Start date ${formatDate(startValue)} selected. Choose an end date.`
    : `End date ${formatDate(endValue)} selected. Choose a start date.`;
};

const attributeState = (element, name) => ({
  present: element.hasAttribute(name),
  value: element.getAttribute(name)
});

const restoreAttribute = (element, name, state) => {
  if (state.present) element.setAttribute(name, state.value ?? '');
  else element.removeAttribute(name);
};

const hasCustomError = (input) => Boolean(input.validity && input.validity.customError);

export function enhance(root) {
  queryAll(root, '.date-range-picker').forEach((picker) => {
    picker.dataset.init = '';
    if (lifecycle.has(picker)) return;
    picker.dataset.mewaDateRangePickerInit = '';

    const start = picker.querySelector('[data-range-start]');
    const end = picker.querySelector('[data-range-end]');
    if (!start || !end) return;

    const status = picker.querySelector('[data-range-status]');
    const error = picker.querySelector('[data-range-error]');
    const base = {
      startMin: start.min,
      startMax: start.max,
      endMin: end.min,
      endMax: end.max
    };
    const initial = {
      pickerInvalid: attributeState(picker, 'data-invalid'),
      orderInvalid: attributeState(picker, 'data-range-order-invalid'),
      startAriaInvalid: attributeState(start, 'aria-invalid'),
      endAriaInvalid: attributeState(end, 'aria-invalid'),
      endErrorMessage: attributeState(end, 'aria-errormessage'),
      errorHidden: error ? error.hidden : true,
      errorText: error ? error.textContent : '',
      statusText: status ? status.textContent : ''
    };
    let interactionStarted = false;
    let managedEndInvalid = false;
    let managedEndErrorMessage = false;
    let managedPickerInvalid = false;
    let managedOrderAttribute = false;
    let managedErrorVisibility = false;
    let managedErrorText = false;
    let managedStatus = false;
    let managedCustomValidity = false;
    let managedOrderInvalid = false;

    const initialStartValue = isDateValue(start.value) ? start.value : '';
    const initialEndValue = isDateValue(end.value) ? end.value : '';
    const initialOrderInvalid = Boolean(
      initialStartValue && initialEndValue && initialStartValue > initialEndValue
    );
    const initialServerOrderInvalid =
      initialOrderInvalid &&
      (initial.pickerInvalid.present ||
        initial.orderInvalid.present ||
        initial.startAriaInvalid.value === 'true' ||
        initial.endAriaInvalid.value === 'true' ||
        (error && !initial.errorHidden));

    const clearManagedCustomValidity = () => {
      if (!managedCustomValidity) return;
      if (end.validationMessage === ORDER_MESSAGE) end.setCustomValidity('');
      managedCustomValidity = false;
    };

    const applyOrderValidity = () => {
      // A server or application script may already own custom validity. Keep it
      // intact and let the visible range error communicate this second issue.
      if (managedCustomValidity && end.validationMessage !== ORDER_MESSAGE) {
        managedCustomValidity = false;
      }
      if (!managedCustomValidity && !hasCustomError(end)) {
        end.setCustomValidity(ORDER_MESSAGE);
        managedCustomValidity = true;
      }
    };

    const showManagedInvalidState = () => {
      applyOrderValidity();

      if (error?.id) {
        if (!end.hasAttribute('aria-errormessage')) {
          end.setAttribute('aria-errormessage', error.id);
          managedEndErrorMessage = true;
        }
      }

      if (!end.hasAttribute('aria-invalid') || end.getAttribute('aria-invalid') !== 'true') {
        end.setAttribute('aria-invalid', 'true');
        managedEndInvalid = true;
      }
      if (!picker.hasAttribute('data-invalid')) {
        picker.dataset.invalid = '';
        managedPickerInvalid = true;
      }
      if (!picker.hasAttribute('data-range-order-invalid')) {
        picker.dataset.rangeOrderInvalid = '';
        managedOrderAttribute = true;
      }
      if (error) {
        if (error.hidden) {
          error.hidden = false;
          managedErrorVisibility = true;
        }
        if (!error.textContent.trim()) {
          error.textContent = ORDER_MESSAGE;
          managedErrorText = true;
        }
      }
    };

    const restoreManagedInvalidState = () => {
      clearManagedCustomValidity();

      if (managedEndInvalid && end.getAttribute('aria-invalid') === 'true') {
        restoreAttribute(end, 'aria-invalid', initial.endAriaInvalid);
      }
      managedEndInvalid = false;

      if (managedEndErrorMessage && end.getAttribute('aria-errormessage') === error?.id) {
        restoreAttribute(end, 'aria-errormessage', initial.endErrorMessage);
      }
      managedEndErrorMessage = false;

      if (managedPickerInvalid && picker.getAttribute('data-invalid') === '') {
        restoreAttribute(picker, 'data-invalid', initial.pickerInvalid);
      }
      managedPickerInvalid = false;

      if (managedOrderAttribute && picker.getAttribute('data-range-order-invalid') === '') {
        restoreAttribute(picker, 'data-range-order-invalid', initial.orderInvalid);
      }
      managedOrderAttribute = false;

      if (error && managedErrorVisibility && !error.hidden) {
        error.hidden = initial.errorHidden;
      }
      managedErrorVisibility = false;

      if (error && managedErrorText && error.textContent === ORDER_MESSAGE) {
        error.textContent = initial.errorText;
      }
      managedErrorText = false;
    };

    const sync = ({ announceOrder = interactionStarted } = {}) => {
      const startValue = isDateValue(start.value) ? start.value : '';
      const endValue = isDateValue(end.value) ? end.value : '';
      const orderInvalid = Boolean(startValue && endValue && startValue > endValue);
      const applyOrderConstraints =
        !initialOrderInvalid || interactionStarted || initialServerOrderInvalid;

      setConstraint(start, 'min', base.startMin);
      setConstraint(
        start,
        'max',
        applyOrderConstraints ? minDateValue(base.startMax, endValue) : base.startMax
      );
      setConstraint(
        end,
        'min',
        applyOrderConstraints ? maxDateValue(base.endMin, startValue) : base.endMin
      );
      setConstraint(end, 'max', base.endMax);

      if (orderInvalid) {
        if (announceOrder || initialServerOrderInvalid) showManagedInvalidState();
      } else {
        restoreManagedInvalidState();
      }

      if (orderInvalid && !announceOrder && !initialServerOrderInvalid) {
        if (managedStatus && status) status.textContent = initial.statusText;
        managedStatus = false;
      } else if (status) {
        updateStatus(
          status,
          startValue,
          endValue,
          orderInvalid,
          announceOrder || initialServerOrderInvalid
        );
        managedStatus = true;
      }
      return {
        orderInvalid,
        detail: rangeDetail(start, end, startValue, endValue, orderInvalid)
      };
    };

    const emitInvalid = (result) => {
      if (result.orderInvalid === managedOrderInvalid) return;
      managedOrderInvalid = result.orderInvalid;
      picker.dispatchEvent(
        new CustomEvent('date-range:invalid', {
          bubbles: true,
          detail: { ...result.detail, reason: 'order' }
        })
      );
    };

    const emitChange = () => {
      interactionStarted = true;
      const result = sync({ announceOrder: true });
      emitInvalid(result);
      picker.dispatchEvent(
        new CustomEvent('date-range:change', {
          bubbles: true,
          detail: result.detail
        })
      );
    };

    const handleInput = () => {
      interactionStarted = true;
      emitInvalid(sync({ announceOrder: true }));
    };
    lifecycle.listen(picker, start, 'input', handleInput);
    lifecycle.listen(picker, end, 'input', handleInput);
    lifecycle.listen(picker, start, 'change', emitChange);
    lifecycle.listen(picker, end, 'change', emitChange);

    lifecycle.reset(picker, start.form || end.form || picker.closest('form'), () => {
      interactionStarted = false;
      const result = sync({ announceOrder: initialServerOrderInvalid });
      managedOrderInvalid = initialServerOrderInvalid && result.orderInvalid;
      if (result.orderInvalid && !initialServerOrderInvalid) {
        restoreManagedInvalidState();
        if (status && managedStatus) status.textContent = initial.statusText;
        managedStatus = false;
      }
    });

    lifecycle.add(picker, () => {
      restoreManagedInvalidState();
      setConstraint(start, 'min', base.startMin);
      setConstraint(start, 'max', base.startMax);
      setConstraint(end, 'min', base.endMin);
      setConstraint(end, 'max', base.endMax);
    });
    const initialState = sync({ announceOrder: initialServerOrderInvalid });
    managedOrderInvalid = initialServerOrderInvalid && initialState.orderInvalid;
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'date-range-picker', enhance, destroy };
