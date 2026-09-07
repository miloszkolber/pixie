// -- Tag Input --------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('tag-input');

function escapeCharacterClass(value) {
  return value.replace(/[\\\]\-^]/g, '\\$&');
}

export function enhance(root) {
  lifecycle.refresh(root);
  queryAll(root, '[data-tag-input]').forEach((tagInput) => {
    tagInput.dataset.init = '';
    if (lifecycle.has(tagInput)) return;
    tagInput.dataset.mewaTagInputInit = '';
    const doc = tagInput.ownerDocument;

    const field = tagInput.querySelector('[data-tag-input-field]');
    const valueInput = tagInput.querySelector('.tag-input-fallback[type="text"]');
    const status = tagInput.querySelector('[data-tag-input-status]');
    if (!field || !valueInput || !status || !valueInput.id) {
      tagInput.removeAttribute('data-mewa-tag-input-init');
      return;
    }

    const inputId = valueInput.id;
    const initialValue = valueInput.defaultValue;
    const originalAttributes = ['id', 'aria-describedby', 'aria-invalid', 'placeholder'].map(
      (name) => [name, valueInput.getAttribute(name)]
    );
    const describedBy = valueInput.getAttribute('aria-describedby');
    const invalid = valueInput.getAttribute('aria-invalid');
    const placeholder = valueInput.getAttribute('placeholder') || '';
    const autocomplete = valueInput.getAttribute('autocomplete') || 'off';
    const delimiters = Array.from(tagInput.dataset.delimiters || ',');
    const delimiterPattern = `[${escapeCharacterClass(delimiters.join(''))}]`;
    const delimiterRegex = new RegExp(delimiterPattern);
    const splitRegex = new RegExp(`${delimiterPattern}|\\r?\\n`, 'g');
    const maxTags = Number.parseInt(tagInput.dataset.maxTags || '', 10);
    const allowDuplicates = tagInput.hasAttribute('data-allow-duplicates');

    let tags = valueInput.value
      .split(splitRegex)
      .map((tag) => tag.trim())
      .filter((tag, index, all) => tag && (allowDuplicates || all.indexOf(tag) === index));

    valueInput.type = 'hidden';
    valueInput.removeAttribute('id');
    valueInput.removeAttribute('aria-describedby');
    valueInput.removeAttribute('aria-invalid');
    valueInput.removeAttribute('placeholder');

    const list = doc.createElement('div');
    list.className = 'tag-input-list';
    list.setAttribute('role', 'list');

    const entry = doc.createElement('span');
    entry.className = 'tag-input-entry';
    entry.setAttribute('role', 'listitem');

    const draft = doc.createElement('input');
    draft.className = 'tag-input-control';
    draft.id = inputId;
    draft.type = 'text';
    draft.placeholder = placeholder;
    draft.autocomplete = autocomplete;
    draft.spellcheck = valueInput.spellcheck;
    draft.disabled = valueInput.disabled;
    draft.readOnly = valueInput.readOnly;
    if (valueInput.hasAttribute('form'))
      draft.setAttribute('form', valueInput.getAttribute('form'));
    if (describedBy) draft.setAttribute('aria-describedby', describedBy);
    if (invalid) draft.setAttribute('aria-invalid', invalid);

    entry.append(draft);
    list.append(entry);
    field.append(list);

    const announce = (message, isError = false) => {
      status.textContent = message;
      if (isError) tagInput.dataset.state = 'error';
      else if (tagInput.dataset.state === 'error') delete tagInput.dataset.state;
    };

    const writeValue = (source, emit = true) => {
      valueInput.value = tags.join(', ');
      draft.required = valueInput.required && tags.length === 0;
      if (!emit) return;

      valueInput.dispatchEvent(new Event('input', { bubbles: true }));
      valueInput.dispatchEvent(new Event('change', { bubbles: true }));
      tagInput.dispatchEvent(
        new CustomEvent('tag-input:change', {
          bubbles: true,
          detail: { tags: tags.slice(), source }
        })
      );
    };

    const removeTag = (index, source) => {
      if (draft.matches(':disabled') || draft.readOnly) return;
      const removed = tags[index];
      if (removed === undefined) return;
      tags = tags.filter((_tag, tagIndex) => tagIndex !== index);
      render();
      writeValue(source);
      announce(`Removed ${removed}.`);
      draft.focus();
    };

    const render = () => {
      list.querySelectorAll('.tag-input-tag').forEach((tag) => tag.remove());

      tags.forEach((tag, index) => {
        const item = doc.createElement('span');
        item.className = 'tag-input-tag';
        item.setAttribute('role', 'listitem');

        const label = doc.createElement('span');
        label.className = 'tag-input-tag-label';
        label.textContent = tag;
        label.title = tag;

        const remove = doc.createElement('button');
        remove.type = 'button';
        remove.className = 'tag-input-remove';
        remove.setAttribute('aria-label', `Remove ${tag}`);
        remove.disabled = valueInput.matches(':disabled') || valueInput.readOnly;
        remove.dataset.tagIndex = String(index);

        const icon = doc.createElementNS('http://www.w3.org/2000/svg', 'svg');
        icon.setAttribute('viewBox', '0 0 16 16');
        icon.setAttribute('width', '12');
        icon.setAttribute('height', '12');
        icon.setAttribute('fill', 'none');
        icon.setAttribute('aria-hidden', 'true');

        const path = doc.createElementNS('http://www.w3.org/2000/svg', 'path');
        path.setAttribute('d', 'M4 4l8 8m0-8-8 8');
        path.setAttribute('stroke', 'currentColor');
        path.setAttribute('stroke-width', '1.5');
        icon.append(path);
        remove.append(icon);

        item.append(label, remove);
        list.insertBefore(item, entry);
      });
    };

    const addParts = (parts, source) => {
      if (draft.matches(':disabled') || draft.readOnly) return false;
      let added = 0;
      let lastError = '';

      parts.forEach((part) => {
        const tag = part.trim();
        if (!tag) return;

        if (!allowDuplicates && tags.includes(tag)) {
          lastError = `${tag} is already added.`;
          return;
        }

        if (Number.isFinite(maxTags) && tags.length >= maxTags) {
          lastError = `Add no more than ${maxTags} tags.`;
          return;
        }

        tags.push(tag);
        added += 1;
      });

      if (added > 0) {
        render();
        writeValue(source);
      }

      if (lastError) announce(lastError, true);
      else if (added > 0) announce(`${added} ${added === 1 ? 'tag' : 'tags'} added.`);

      return added > 0;
    };

    lifecycle.listen(tagInput, draft, 'keydown', (event) => {
      if (event.isComposing || draft.matches(':disabled') || draft.readOnly) return;
      if (event.key === 'Backspace' && draft.value === '' && tags.length > 0) {
        event.preventDefault();
        removeTag(tags.length - 1, 'backspace');
        return;
      }

      if (event.key !== 'Enter' && !delimiters.includes(event.key)) return;
      event.preventDefault();
      if (addParts([draft.value], 'keyboard') || !draft.value.trim()) draft.value = '';
    });

    lifecycle.listen(tagInput, draft, 'input', (event) => {
      if (event.isComposing || draft.matches(':disabled') || draft.readOnly) return;
      if (!delimiterRegex.test(draft.value)) return;
      const parts = draft.value.split(splitRegex);
      const trailing = parts.pop() || '';
      addParts(parts, 'delimiter');
      draft.value = trailing;
    });

    lifecycle.listen(tagInput, draft, 'paste', (event) => {
      if (draft.matches(':disabled') || draft.readOnly) return;
      const text = event.clipboardData?.getData('text') || '';
      if (!delimiterRegex.test(text) && !/[\r\n]/.test(text)) return;

      event.preventDefault();
      const parts = `${draft.value}${text}`.split(splitRegex);
      draft.value = '';
      addParts(parts, 'paste');
    });

    lifecycle.listen(tagInput, list, 'click', (event) => {
      const button = event.target.closest('.tag-input-remove');
      if (!button || button.disabled) return;
      const index = Number.parseInt(button.dataset.tagIndex || '', 10);
      if (Number.isInteger(index)) removeTag(index, 'remove');
    });

    lifecycle.listen(tagInput, field, 'click', (event) => {
      if (event.target === field || event.target === list || event.target === entry) draft.focus();
    });

    lifecycle.listen(tagInput, valueInput.form, 'submit', (event) => {
      if (!draft.value.trim()) return;
      if (addParts([draft.value], 'submit')) draft.value = '';
      else event.preventDefault();
    });

    lifecycle.reset(tagInput, valueInput.form, () => {
      tags = initialValue
        .split(splitRegex)
        .map((tag) => tag.trim())
        .filter((tag, index, all) => tag && (allowDuplicates || all.indexOf(tag) === index));
      draft.value = '';
      render();
      writeValue('reset', false);
      announce('');
    });
    lifecycle.add(tagInput, () => {
      const currentValue = tags.join(', ');
      list.remove();
      valueInput.type = 'text';
      valueInput.defaultValue = initialValue;
      valueInput.value = currentValue;
      for (const [name, value] of originalAttributes) {
        if (value === null) valueInput.removeAttribute(name);
        else valueInput.setAttribute(name, value);
      }
      delete tagInput.dataset.enhanced;
    });

    lifecycle.onUpdate(tagInput, () => {
      draft.disabled = valueInput.disabled;
      draft.readOnly = valueInput.readOnly;
      draft.required = valueInput.required && tags.length === 0;
      list.querySelectorAll('.tag-input-remove').forEach((button) => {
        button.disabled = valueInput.matches(':disabled') || valueInput.readOnly;
      });
    });
    render();
    writeValue('initial', false);
    tagInput.dataset.enhanced = '';
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'tag-input', enhance, destroy };
