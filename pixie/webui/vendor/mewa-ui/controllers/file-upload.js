// -- File Upload -----------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('file-upload');

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes < 1024) return `${bytes} B`;

  const units = ['KB', 'MB', 'GB'];
  let value = bytes;
  let unit = 0;
  do {
    value /= 1024;
    unit += 1;
  } while (value >= 1024 && unit < units.length);

  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${units[unit - 1]}`;
}

function matchesAccept(file, accept) {
  if (!accept) return true;

  const name = file.name.toLocaleLowerCase();
  const type = file.type.toLocaleLowerCase();
  return accept.split(',').some((entry) => {
    const rule = entry.trim().toLocaleLowerCase();
    if (!rule) return false;
    if (rule.startsWith('.')) return name.endsWith(rule);
    if (rule.endsWith('/*')) return type.startsWith(rule.slice(0, -1));
    return type === rule;
  });
}

function droppedDirectory(dataTransfer) {
  return Array.from(dataTransfer.items || []).some((item) => {
    if (item.kind !== 'file' || typeof item.webkitGetAsEntry !== 'function') return false;
    return Boolean(item.webkitGetAsEntry()?.isDirectory);
  });
}

export function enhance(root) {
  const uploads = queryAll(root, '[data-file-upload]');
  const ancestor = root?.nodeType === 1 ? root.closest?.('[data-file-upload]') : null;
  if (ancestor) uploads.push(ancestor);

  new Set(uploads).forEach((upload) => {
    upload.dataset.init = '';
    if (lifecycle.has(upload)) return;
    upload.dataset.mewaFileUploadInit = '';

    const dropzone = upload.querySelector('.file-upload-dropzone');
    const input = upload.querySelector('.file-upload-input[type="file"]');
    const list = upload.querySelector('[data-file-upload-list]');
    const status = upload.querySelector('[data-file-upload-status]');
    const error = upload.querySelector('[data-file-upload-error]');

    if (!dropzone || !input || !list || !status || !error) {
      upload.removeAttribute('data-mewa-file-upload-init');
      return;
    }

    let files = Array.from(input.files || []);
    let previewUrls = [];
    let dragDepth = 0;
    let synchronizing = false;

    const maxFiles = Number.parseInt(upload.dataset.maxFiles || '', 10);
    const maxSize = Number.parseInt(upload.dataset.maxSize || '', 10);

    const clearPreviews = () => {
      previewUrls.forEach((url) => URL.revokeObjectURL(url));
      previewUrls = [];
    };

    const setError = (message) => {
      error.textContent = message;
      error.hidden = false;
      upload.dataset.state = 'error';
    };

    const clearError = () => {
      error.textContent = '';
      error.hidden = true;
      if (upload.dataset.state === 'error') delete upload.dataset.state;
    };

    const validate = (incoming, existingCount = 0) => {
      if (incoming.some((file) => !matchesAccept(file, input.accept))) {
        return 'One or more files do not match the accepted file types.';
      }

      if (Number.isFinite(maxSize) && incoming.some((file) => file.size > maxSize)) {
        return `Each file must be ${formatBytes(maxSize)} or smaller.`;
      }

      if (Number.isFinite(maxFiles) && existingCount + incoming.length > maxFiles) {
        return `Choose no more than ${maxFiles} files.`;
      }

      return '';
    };

    const render = () => {
      clearPreviews();
      list.replaceChildren();

      files.forEach((file, index) => {
        const item = upload.ownerDocument.createElement('li');
        item.className = 'file-upload-item';

        if (upload.hasAttribute('data-preview') && file.type.startsWith('image/')) {
          const image = upload.ownerDocument.createElement('img');
          const url = URL.createObjectURL(file);
          previewUrls.push(url);
          image.className = 'file-upload-preview';
          image.src = url;
          image.alt = '';
          item.append(image);
        } else {
          const marker = upload.ownerDocument.createElement('span');
          marker.className = 'file-upload-preview';
          marker.setAttribute('aria-hidden', 'true');
          marker.textContent = 'FILE';
          item.append(marker);
        }

        const details = upload.ownerDocument.createElement('span');
        details.className = 'file-upload-file';

        const name = upload.ownerDocument.createElement('span');
        name.className = 'file-upload-name';
        name.textContent = file.name;
        name.title = file.name;

        const meta = upload.ownerDocument.createElement('span');
        meta.className = 'file-upload-meta';
        meta.textContent = `${file.type || 'File'} · ${formatBytes(file.size)}`;

        details.append(name, meta);

        const remove = upload.ownerDocument.createElement('button');
        remove.type = 'button';
        remove.className = 'file-upload-remove';
        remove.textContent = 'Remove';
        remove.setAttribute('aria-label', `Remove ${file.name}`);
        remove.disabled = input.matches(':disabled');
        remove.dataset.fileIndex = String(index);

        item.append(details, remove);
        list.append(item);
      });

      status.textContent =
        files.length === 0
          ? ''
          : `${files.length} ${files.length === 1 ? 'file' : 'files'} selected.`;
    };

    const assignNativeFiles = (next) => {
      if (typeof DataTransfer !== 'function') return false;
      try {
        const transfer = new DataTransfer();
        next.forEach((file) => transfer.items.add(file));
        input.files = transfer.files;
        return true;
      } catch {
        return false;
      }
    };

    const emitChange = (source) => {
      upload.dispatchEvent(
        new CustomEvent('file-upload:change', {
          bubbles: true,
          detail: { files: files.slice(), source }
        })
      );
    };

    const commit = (next, source, dispatchNative = false) => {
      if (!assignNativeFiles(next)) {
        setError('This browser cannot add dropped files to the form control. Use the file picker.');
        return false;
      }

      files = next;
      clearError();
      render();

      if (dispatchNative) {
        synchronizing = true;
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new Event('change', { bubbles: true }));
        synchronizing = false;
      }

      emitChange(source);
      return true;
    };

    lifecycle.listen(upload, input, 'change', () => {
      if (synchronizing) return;

      const incoming = Array.from(input.files || []);
      const validationError = validate(incoming);
      if (validationError) {
        input.value = '';
        files = [];
        render();
        setError(validationError);
        return;
      }

      files = incoming;
      clearError();
      render();
      emitChange('picker');
    });

    lifecycle.listen(upload, dropzone, 'dragenter', (event) => {
      if (
        input.matches(':disabled') ||
        !Array.from(event.dataTransfer?.types || []).includes('Files')
      )
        return;
      event.preventDefault();
      dragDepth += 1;
      upload.dataset.dragging = '';
    });

    lifecycle.listen(upload, dropzone, 'dragover', (event) => {
      if (input.matches(':disabled') || !event.dataTransfer) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'copy';
      upload.dataset.dragging = '';
    });

    lifecycle.listen(upload, dropzone, 'dragleave', () => {
      dragDepth = Math.max(0, dragDepth - 1);
      if (dragDepth === 0) delete upload.dataset.dragging;
    });

    lifecycle.listen(upload, dropzone, 'drop', (event) => {
      if (input.matches(':disabled') || !event.dataTransfer) return;
      event.preventDefault();
      dragDepth = 0;
      delete upload.dataset.dragging;

      if (droppedDirectory(event.dataTransfer)) {
        setError('Choose files instead of a folder.');
        return;
      }

      const dropped = Array.from(event.dataTransfer.files || []);
      const incoming = input.multiple ? dropped : dropped.slice(0, 1);
      const existingCount = input.multiple ? files.length : 0;
      const validationError = validate(incoming, existingCount);
      if (validationError) {
        setError(validationError);
        return;
      }

      const next = input.multiple ? [...files, ...incoming] : incoming;
      commit(next, 'drop', true);
    });

    lifecycle.listen(upload, list, 'click', (event) => {
      const button = event.target.closest('.file-upload-remove');
      if (!button || button.matches(':disabled') || input.matches(':disabled')) return;

      const index = Number.parseInt(button.dataset.fileIndex || '', 10);
      if (!Number.isInteger(index) || !files[index]) return;
      commit(
        files.filter((_file, fileIndex) => fileIndex !== index),
        'remove',
        true
      );
    });

    lifecycle.reset(upload, input.form, () => {
      files = Array.from(input.files || []);
      dragDepth = 0;
      delete upload.dataset.dragging;
      clearError();
      render();
    });
    lifecycle.add(upload, () => {
      clearPreviews();
      list.replaceChildren();
      delete upload.dataset.enhanced;
    });
    render();
    upload.dataset.enhanced = '';
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'file-upload', enhance, destroy };
