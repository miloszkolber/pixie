// -- Date Picker ------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('date-picker');

const weekdayFormatter = new Intl.DateTimeFormat(undefined, { weekday: 'short' });
const longWeekdayFormatter = new Intl.DateTimeFormat(undefined, { weekday: 'long' });
const monthFormatter = new Intl.DateTimeFormat(undefined, { month: 'long' });
const dateFormatter = new Intl.DateTimeFormat(undefined, { dateStyle: 'long' });

const DAYS = Array.from({ length: 7 }, (_, i) => weekdayFormatter.format(new Date(2024, 0, i)));
const LONG_DAYS = Array.from({ length: 7 }, (_, i) =>
  longWeekdayFormatter.format(new Date(2024, 0, i))
);

const daysInMonth = (year, month) => new Date(year, month + 1, 0).getDate();

const isToday = (date) => {
  const now = new Date();
  return (
    now.getFullYear() === date.getFullYear() &&
    now.getMonth() === date.getMonth() &&
    now.getDate() === date.getDate()
  );
};

const dateKey = (date) =>
  [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, '0'),
    String(date.getDate()).padStart(2, '0')
  ].join('-');

const dateFromKey = (value) => {
  const [year, month, day] = value.split('-').map(Number);
  return new Date(year, month - 1, day);
};

const moveMonth = (state, offset) => {
  const next = new Date(state.year, state.month + offset, 1);
  state.year = next.getFullYear();
  state.month = next.getMonth();
};

const setTabStop = (datePicker, activeButton) => {
  datePicker.querySelectorAll('.date-picker-day button').forEach((button) => {
    button.tabIndex = button === activeButton ? 0 : -1;
  });
};

const focusDate = (datePicker, value) => {
  const button = Array.from(datePicker.querySelectorAll('.date-picker-day button')).find(
    (candidate) => candidate.dataset.date === value
  );
  if (!button) return;
  setTabStop(datePicker, button);
  button.focus();
};

const renderDatePicker = (el, year, month, selectedDay) => {
  const documentRoot = el.ownerDocument;
  const heading = el.querySelector('.date-picker-heading');
  const grid = el.querySelector('.date-picker-grid');
  if (!grid) return;

  const headingText = `${monthFormatter.format(new Date(year, month, 1))} ${year}`;
  if (heading) {
    heading.textContent = headingText;
    heading.setAttribute('aria-live', 'polite');
  }

  grid.setAttribute('role', 'grid');
  grid.setAttribute('aria-label', headingText);

  const thead = documentRoot.createElement('thead');
  const headerRow = documentRoot.createElement('tr');
  headerRow.setAttribute('role', 'row');
  DAYS.forEach((day, index) => {
    const label = documentRoot.createElement('th');
    label.className = 'date-picker-day-label';
    label.setAttribute('role', 'columnheader');
    label.scope = 'col';
    label.abbr = LONG_DAYS[index];
    label.textContent = day;
    headerRow.append(label);
  });
  thead.append(headerRow);

  const tbody = documentRoot.createElement('tbody');
  const total = daysInMonth(year, month);
  const startDay = new Date(year, month, 1).getDay();
  const rows = Math.ceil((startDay + total) / 7);
  let hasTabStop = false;

  for (let row = 0; row < rows; row++) {
    const tableRow = documentRoot.createElement('tr');
    tableRow.setAttribute('role', 'row');
    for (let column = 0; column < 7; column++) {
      const cellIndex = row * 7 + column;
      const date = new Date(year, month, cellIndex - startDay + 1);
      const outside = date.getMonth() !== month;
      const selected = !outside && date.getDate() === selectedDay;
      const cell = documentRoot.createElement('td');
      const button = documentRoot.createElement('button');

      cell.className = 'date-picker-day';
      cell.setAttribute('role', 'gridcell');
      cell.setAttribute('aria-selected', String(selected));
      button.type = 'button';
      button.dataset.day = String(date.getDate());
      button.dataset.date = dateKey(date);
      button.setAttribute('aria-label', dateFormatter.format(date));
      button.textContent = String(date.getDate());

      if (outside) {
        const direction = date < new Date(year, month, 1) ? 'prev' : 'next';
        cell.dataset.outside = '';
        button.dataset.outside = direction;
        button.tabIndex = -1;
      } else {
        button.tabIndex = selected || (!hasTabStop && selectedDay === null) ? 0 : -1;
        hasTabStop ||= button.tabIndex === 0;

        if (isToday(date)) {
          cell.dataset.today = '';
          cell.setAttribute('aria-current', 'date');
        }
        if (selected) cell.dataset.selected = '';
      }

      cell.append(button);
      tableRow.append(cell);
    }
    tbody.append(tableRow);
  }

  grid.replaceChildren(thead, tbody);
};

export function enhance(root) {
  queryAll(root, '.date-picker').forEach((datePicker) => {
    datePicker.dataset.init = '';
    if (lifecycle.has(datePicker)) return;
    datePicker.dataset.mewaDatePickerInit = '';
    const now = new Date();
    const state = {
      year: now.getFullYear(),
      month: now.getMonth(),
      selected: null
    };

    renderDatePicker(datePicker, state.year, state.month, state.selected);

    lifecycle.listen(datePicker, datePicker, 'click', (event) => {
      const nav = event.target.closest('.date-picker-nav');
      if (nav) {
        const action = nav.dataset.action;
        if (action === 'prev-month') moveMonth(state, -1);
        if (action === 'next-month') moveMonth(state, 1);
        if (action === 'prev-month' || action === 'next-month') {
          state.selected = null;
          renderDatePicker(datePicker, state.year, state.month, state.selected);
        }
        return;
      }

      const dayButton = event.target.closest('.date-picker-day button');
      if (!dayButton || dayButton.disabled || dayButton.closest('[data-disabled]')) return;

      const selectedDate = dateFromKey(dayButton.dataset.date);
      state.year = selectedDate.getFullYear();
      state.month = selectedDate.getMonth();
      state.selected = selectedDate.getDate();
      renderDatePicker(datePicker, state.year, state.month, state.selected);
      focusDate(datePicker, dateKey(selectedDate));

      datePicker.dispatchEvent(
        new CustomEvent('date-picker:select', {
          detail: { date: selectedDate },
          bubbles: true
        })
      );
    });

    lifecycle.listen(datePicker, datePicker, 'keydown', (event) => {
      const dayButton = event.target.closest('.date-picker-day button');
      if (!dayButton) return;

      const allButtons = Array.from(datePicker.querySelectorAll('.date-picker-day button'));
      const index = allButtons.indexOf(dayButton);
      let next = null;

      switch (event.key) {
        case 'ArrowRight':
          event.preventDefault();
          next = allButtons[index + 1];
          break;
        case 'ArrowLeft':
          event.preventDefault();
          next = allButtons[index - 1];
          break;
        case 'ArrowDown':
          event.preventDefault();
          next = allButtons[index + 7];
          break;
        case 'ArrowUp':
          event.preventDefault();
          next = allButtons[index - 7];
          break;
      }

      if (next) {
        setTabStop(datePicker, next);
        next.focus();
      }
    });
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'date-picker', enhance, destroy };
