// -- Data Table ------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('data-table');

const rootSelector = '.data-table';

function getNumber(value, fallback = 0) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

export function enhance(scope) {
  lifecycle.refresh(scope);
  queryAll(scope, rootSelector).forEach((root) => {
    root.dataset.init = '';
    if (lifecycle.has(root)) return;
    root.dataset.mewaDataTableInit = '';

    const table = root.matches('table') ? root : root.querySelector('table');
    const body = table?.tBodies?.[0];
    if (!table || !body) {
      root.removeAttribute('data-mewa-data-table-init');
      return;
    }

    const filters = () =>
      queryAll(root, '[data-table-filter], .data-table-filter').filter((element) =>
        element.matches('input, textarea')
      );
    const status = () =>
      queryAll(root, '[data-table-status], .data-table-summary[role="status"], [role="status"]')[0];
    const emptyState = () => queryAll(root, '[data-table-empty], .data-table-empty')[0];
    const range = () => queryAll(root, '[data-table-range], .data-table-range')[0];
    const pagination = () => queryAll(root, '[data-table-pagination], .data-table-pagination')[0];
    const pageControls = () => {
      const nav = pagination();
      if (!nav) return [];
      return queryAll(
        nav,
        '[data-table-page], .data-table-pagination-link, .data-table-pagination-button'
      );
    };
    const rows = () => Array.from(body.rows);
    const sortButtons = () =>
      Array.from(
        table.querySelectorAll(
          'th[aria-sort] button, th button[data-table-sort], th .data-table-sort'
        )
      );
    const sortHeaders = () =>
      sortButtons()
        .map((button) => button.closest('th'))
        .filter((header, index, headers) => header && headers.indexOf(header) === index);
    const pageSize = () => {
      const configured = root.dataset.pageSize || table.dataset.pageSize;
      const value = Math.floor(getNumber(configured));
      return value > 0 ? value : 0;
    };

    const originalOrder = new WeakMap();
    const sortLabels = new WeakMap();
    const linkTabIndexes = new WeakMap();
    const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
    let nextOrder = 0;
    let currentPage = 1;

    rows().forEach((row) => originalOrder.set(row, nextOrder++));
    pageControls().forEach((control) => {
      if (control.hasAttribute('tabindex'))
        linkTabIndexes.set(control, control.getAttribute('tabindex'));
    });

    const getFilterValue = () =>
      filters()
        .map((input) => input.value.trim())
        .filter(Boolean)
        .join(' ');

    const getLabels = () => {
      const summary = status();
      const singular = summary?.dataset.singular || root.dataset.singular || 'result';
      const plural = summary?.dataset.plural || root.dataset.plural || 'results';
      const rangeLabel = range()?.dataset.rangeLabel || root.dataset.rangeLabel || plural;
      return { singular, plural, rangeLabel };
    };

    const getSortValue = (row, columnIndex) => {
      const cell = row.cells[columnIndex];
      if (!cell) return '';
      return cell.dataset.sortValue || cell.textContent.trim();
    };

    const compareRows = (first, second, columnIndex, type, direction) => {
      const firstValue = getSortValue(first, columnIndex);
      const secondValue = getSortValue(second, columnIndex);
      const firstEmpty = firstValue === '';
      const secondEmpty = secondValue === '';

      if (firstEmpty !== secondEmpty) return firstEmpty ? 1 : -1;

      let result;
      if (type === 'number' || type === 'numeric') {
        const firstNumber = getNumber(String(firstValue).replace(/[^\d+-.]/g, ''), NaN);
        const secondNumber = getNumber(String(secondValue).replace(/[^\d+-.]/g, ''), NaN);
        if (Number.isFinite(firstNumber) && Number.isFinite(secondNumber)) {
          result = firstNumber - secondNumber;
        }
      } else if (type === 'date') {
        const firstDate = Date.parse(firstValue);
        const secondDate = Date.parse(secondValue);
        if (Number.isFinite(firstDate) && Number.isFinite(secondDate))
          result = firstDate - secondDate;
      }

      if (result === undefined || Number.isNaN(result)) {
        result = collator.compare(firstValue, secondValue);
      }

      if (result !== 0) return result * direction;
      return (originalOrder.get(first) ?? nextOrder) - (originalOrder.get(second) ?? nextOrder);
    };

    const updateSortLabels = () => {
      sortButtons().forEach((button) => {
        const header = button.closest('th');
        if (!header) return;
        const label =
          sortLabels.get(button) ||
          button.getAttribute('aria-label')?.replace(/^sort by\s+/i, '') ||
          button.textContent.trim() ||
          'column';
        sortLabels.set(button, label);
        const direction = header.getAttribute('aria-sort');
        if (direction === 'ascending' || direction === 'descending') {
          const nextDirection = direction === 'ascending' ? 'descending' : 'ascending';
          button.setAttribute(
            'aria-label',
            `Sort by ${label}. Currently ${direction}. Activate to sort ${nextDirection}.`
          );
        } else if (!button.hasAttribute('aria-label')) {
          button.setAttribute('aria-label', `Sort by ${label}. Activate to sort ascending.`);
        }
      });
    };

    const getPage = (control, pageCount) => {
      const value = control.dataset.tablePage || control.textContent.trim();
      if (/^\d+$/.test(value)) return getNumber(value, 1);
      if (value === 'previous' || value === 'prev') return Math.max(1, currentPage - 1);
      if (value === 'next') return Math.min(pageCount, currentPage + 1);
      return 0;
    };

    const updatePagination = (pageCount) => {
      const controls = pageControls();
      const nav = pagination();
      if (!nav || !pageSize()) return;

      controls.forEach((control) => {
        const value = control.dataset.tablePage || control.textContent.trim();
        const numericPage = /^\d+$/.test(value) ? getNumber(value) : 0;
        const isPrevious = value === 'previous' || value === 'prev';
        const isNext = value === 'next';
        const disabled = (isPrevious && currentPage <= 1) || (isNext && currentPage >= pageCount);

        if (numericPage) {
          const listItem = control.closest('li');
          const isOutOfRange = numericPage > pageCount;
          if (listItem) listItem.hidden = isOutOfRange;
          else control.hidden = isOutOfRange;
          if (numericPage === currentPage) control.setAttribute('aria-current', 'page');
          else control.removeAttribute('aria-current');
        }

        if (control.tagName === 'BUTTON') {
          control.disabled = disabled;
        } else if (isPrevious || isNext) {
          if (disabled) {
            control.setAttribute('aria-disabled', 'true');
            control.setAttribute('tabindex', '-1');
          } else {
            control.removeAttribute('aria-disabled');
            const initialTabIndex = linkTabIndexes.get(control);
            if (initialTabIndex === undefined) control.removeAttribute('tabindex');
            else control.setAttribute('tabindex', initialTabIndex);
          }
        }
      });
    };

    const render = () => {
      const allRows = rows();
      allRows.forEach((row) => {
        if (!originalOrder.has(row)) originalOrder.set(row, nextOrder++);
      });

      const needle = getFilterValue().toLocaleLowerCase();
      const matchingRows = allRows.filter((row) =>
        row.textContent.toLocaleLowerCase().includes(needle)
      );
      const size = pageSize();
      const pageCount = size ? Math.max(1, Math.ceil(matchingRows.length / size)) : 1;
      currentPage = Math.min(Math.max(currentPage, 1), pageCount);
      const firstIndex = size ? (currentPage - 1) * size : 0;
      const pageRows = size ? matchingRows.slice(firstIndex, firstIndex + size) : matchingRows;
      const pageSet = new Set(pageRows);

      allRows.forEach((row) => {
        row.hidden = !pageSet.has(row);
      });

      const empty = emptyState();
      if (empty) empty.hidden = matchingRows.length !== 0;

      const summary = status();
      if (summary) {
        const labels = getLabels();
        summary.setAttribute('role', 'status');
        if (!summary.hasAttribute('aria-live')) summary.setAttribute('aria-live', 'polite');
        summary.textContent = `${matchingRows.length} ${matchingRows.length === 1 ? labels.singular : labels.plural}`;
      }

      const rangeOutput = range();
      if (rangeOutput) {
        const labels = getLabels();
        const first = matchingRows.length ? firstIndex + 1 : 0;
        const last = matchingRows.length ? firstIndex + pageRows.length : 0;
        rangeOutput.textContent = matchingRows.length
          ? `Showing ${first}\u2013${last} of ${matchingRows.length} ${labels.rangeLabel}`
          : `Showing 0 of ${matchingRows.length} ${labels.rangeLabel}`;
      }

      updatePagination(pageCount);
      updateSortLabels();
      return { allRows, matchingRows, pageCount, pageRows };
    };

    const sortBy = (button) => {
      const header = button.closest('th');
      if (!header) return;
      const headerRow = header.parentElement;
      const columnIndex = Array.from(headerRow?.cells || []).indexOf(header);
      if (columnIndex < 0) return;

      const ascending = header.getAttribute('aria-sort') !== 'ascending';
      const direction = ascending ? 'ascending' : 'descending';
      const multiplier = ascending ? 1 : -1;
      sortHeaders().forEach((item) =>
        item.setAttribute('aria-sort', item === header ? direction : 'none')
      );
      const orderedRows = rows().sort((first, second) =>
        compareRows(first, second, columnIndex, header.dataset.sortType || 'text', multiplier)
      );
      orderedRows.forEach((row, index) => {
        originalOrder.set(row, index);
        body.append(row);
      });
      currentPage = 1;
      render();
      root.dispatchEvent(
        new CustomEvent('data-table:sort', {
          bubbles: true,
          detail: { column: columnIndex, direction, header }
        })
      );
    };

    const emitFilter = () => {
      const result = render();
      root.dispatchEvent(
        new CustomEvent('data-table:filter', {
          bubbles: true,
          detail: {
            query: getFilterValue(),
            matching: result.matchingRows.length,
            total: result.allRows.length
          }
        })
      );
    };

    lifecycle.listen(root, root, 'click', (event) => {
      const target = event.target.closest?.('button, a');
      if (!target || !root.contains(target)) return;

      const sortButton =
        target.matches('[data-table-sort], .data-table-sort') || target.closest('th[aria-sort]')
          ? target
          : null;
      if (sortButton) {
        const header = sortButton.closest('th');
        if (
          !header ||
          (!header.hasAttribute('aria-sort') &&
            !sortButton.matches('[data-table-sort], .data-table-sort'))
        )
          return;
        if (!header.hasAttribute('aria-sort')) header.setAttribute('aria-sort', 'none');
        event.preventDefault();
        sortBy(sortButton);
        return;
      }

      if (target.matches('[data-table-clear]')) {
        event.preventDefault();
        filters().forEach((input) => {
          input.value = '';
        });
        currentPage = 1;
        emitFilter();
        filters()[0]?.focus();
        return;
      }

      const control = target.closest(
        '[data-table-page], .data-table-pagination-link, .data-table-pagination-button'
      );
      if (!control || !root.contains(control) || !pageSize()) return;
      const needle = getFilterValue().toLocaleLowerCase();
      const pageCount = Math.max(
        1,
        Math.ceil(
          rows().filter((row) => {
            return row.textContent.toLocaleLowerCase().includes(needle);
          }).length / pageSize()
        )
      );
      if (control.getAttribute('aria-disabled') === 'true' || control.disabled) {
        event.preventDefault();
        return;
      }
      const page = getPage(control, pageCount);
      if (!page || page === currentPage) {
        if (control.matches('a')) event.preventDefault();
        return;
      }
      event.preventDefault();
      currentPage = page;
      const result = render();
      root.dispatchEvent(
        new CustomEvent('data-table:page', {
          bubbles: true,
          detail: {
            page: currentPage,
            pageSize: pageSize(),
            pageCount: result.pageCount,
            matching: result.matchingRows.length,
            total: result.allRows.length
          }
        })
      );
    });

    lifecycle.listen(root, root, 'input', (event) => {
      const target = event.target;
      if (!target.matches?.('[data-table-filter], .data-table-filter')) return;
      currentPage = 1;
      emitFilter();
    });

    lifecycle.listen(root, root, 'submit', (event) => {
      const form = event.target.closest?.('form');
      if (!form || !root.contains(form) || !filters().some((input) => form.contains(input))) return;
      event.preventDefault();
      currentPage = 1;
      emitFilter();
    });

    sortButtons().forEach((button) => {
      const header = button.closest('th');
      if (header && !header.hasAttribute('aria-sort')) header.setAttribute('aria-sort', 'none');
    });
    const activePage = pageControls().find(
      (control) => control.getAttribute('aria-current') === 'page'
    );
    if (activePage) {
      const parsedPage = getPage(activePage, Number.MAX_SAFE_INTEGER);
      if (parsedPage) currentPage = parsedPage;
    }
    lifecycle.onUpdate(root, render);
    render();
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'data-table', enhance, destroy };
