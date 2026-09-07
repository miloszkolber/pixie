/* -- Image component ----------------------------------------- */

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('image');

const initializedDocuments = new WeakSet();

export function enhance(root) {
  const ownerDocument =
    root?.nodeType === 9
      ? root
      : root?.ownerDocument || (typeof document === 'undefined' ? null : document);
  if (!ownerDocument) return;
  installPreviewListener(ownerDocument);
  /* -- Fallback: mark images that fail to load ----------------- */
  queryAll(root, '.image > img').forEach((img) => {
    img.closest('.image').dataset.init = '';
    const figure = img.closest('.image');
    if (lifecycle.has(figure)) return;
    figure.dataset.mewaImageInit = '';
    lifecycle.add(figure, () => delete img.dataset.error);

    if (img.complete && img.naturalWidth === 0) {
      img.dataset.error = '';
    }

    lifecycle.listen(figure, img, 'error', () => {
      img.dataset.error = '';
    });

    lifecycle.listen(figure, img, 'load', () => {
      delete img.dataset.error;
    });

    if (!figure.hasAttribute('data-preview')) return;

    figure.setAttribute('tabindex', '0');
    if (!figure.hasAttribute('role')) figure.setAttribute('role', 'button');
    if (!figure.hasAttribute('aria-label') && !figure.hasAttribute('aria-labelledby')) {
      figure.setAttribute(
        'aria-label',
        img.alt ? `Open image preview: ${img.alt}` : 'Open image preview'
      );
    }

    lifecycle.listen(figure, figure, 'keydown', (event) => {
      if (event.key !== 'Enter' && event.key !== ' ') return;
      if (img.dataset.error !== undefined) return;
      event.preventDefault();
      openLightbox(img.ownerDocument, img.src, img.alt);
    });
  });
}

/* -- Lightbox ------------------------------------------------ */
let lightbox = null;
let lightboxImg = null;
let zoom = 1;
let rotation = 0;

function getLightbox(ownerDocument) {
  if (lightbox && lightbox.isConnected && lightbox.ownerDocument === ownerDocument) return lightbox;

  // SPA navigation can remove the shared dialog from the document. Do not
  // retain the detached node or its image reference when recreating it.
  lightbox = null;
  lightboxImg = null;

  lightbox = ownerDocument.createElement('dialog');
  lightbox.className = 'image-lightbox';
  lightbox.setAttribute('aria-label', 'Image preview');

  lightbox.innerHTML = `
    <div class="image-lightbox-content">
      <img src="" alt="" />
    </div>
    <div class="image-lightbox-toolbar">
      <button class="image-lightbox-btn" type="button" data-action="zoom-in" aria-label="Zoom in">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="11" y1="8" x2="11" y2="14"/><line x1="8" y1="11" x2="14" y2="11"/></svg>
      </button>
      <button class="image-lightbox-btn" type="button" data-action="zoom-out" aria-label="Zoom out">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="8" y1="11" x2="14" y2="11"/></svg>
      </button>
      <button class="image-lightbox-btn" type="button" data-action="rotate-left" aria-label="Rotate left">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 2v6h6"/><path d="M2.66 15.57a10 10 0 1 0 .57-8.38"/></svg>
      </button>
      <button class="image-lightbox-btn" type="button" data-action="rotate-right" aria-label="Rotate right">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.5 2v6h-6"/><path d="M21.34 15.57a10 10 0 1 1-.57-8.38"/></svg>
      </button>
      <button class="image-lightbox-btn" type="button" data-action="reset" aria-label="Reset">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
      </button>
      <button class="image-lightbox-btn" type="button" data-action="close" aria-label="Close">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
      </button>
    </div>`;

  lightboxImg = lightbox.querySelector('.image-lightbox-content > img');

  lifecycle.listen(lightbox, lightbox.querySelector('.image-lightbox-toolbar'), 'click', (e) => {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;

    const action = btn.dataset.action;
    if (action === 'zoom-in') zoom = Math.min(zoom + 0.25, 5);
    else if (action === 'zoom-out') zoom = Math.max(zoom - 0.25, 0.25);
    else if (action === 'rotate-left') rotation -= 90;
    else if (action === 'rotate-right') rotation += 90;
    else if (action === 'reset') {
      zoom = 1;
      rotation = 0;
    } else if (action === 'close') {
      lightbox.close();
      return;
    }

    applyTransform();
  });

  lifecycle.listen(lightbox, lightbox, 'click', (e) => {
    if (e.target === lightbox) lightbox.close();
  });

  ownerDocument.body.appendChild(lightbox);
  return lightbox;
}

function applyTransform() {
  if (lightboxImg) {
    lightboxImg.style.transform = `scale(${zoom}) rotate(${rotation}deg)`;
  }
}

function openLightbox(ownerDocument, src, alt) {
  const lb = getLightbox(ownerDocument);
  zoom = 1;
  rotation = 0;
  lightboxImg.src = src;
  lightboxImg.alt = alt || '';
  lightboxImg.style.transform = '';
  if (!lb.open && typeof lb.showModal === 'function') lb.showModal();
}

/* -- Attach preview click handlers --------------------------- */
function installPreviewListener(ownerDocument) {
  if (initializedDocuments.has(ownerDocument)) return;
  initializedDocuments.add(ownerDocument);
  lifecycle.add(ownerDocument, () => initializedDocuments.delete(ownerDocument));

  lifecycle.listen(ownerDocument, ownerDocument, 'click', (e) => {
    const figure = e.target.closest('.image[data-preview][data-mewa-image-init]');
    if (!figure) return;

    const img = figure.querySelector('img');
    if (!img || img.dataset.error !== undefined) return;

    openLightbox(ownerDocument, img.src, img.alt);
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
  if (root?.nodeType === 9 && lightbox?.ownerDocument === root) {
    lightbox.remove();
    lightbox = null;
    lightboxImg = null;
  }
}

export const behavior = { name: 'image', enhance, destroy };
