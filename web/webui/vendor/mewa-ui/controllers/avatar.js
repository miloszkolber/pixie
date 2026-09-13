// -- Avatar ---------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('avatar');

export function enhance(root) {
  queryAll(root, '.avatar-image').forEach((img) => {
    img.dataset.init = '';
    if (lifecycle.has(img)) return;
    img.dataset.mewaAvatarInit = '';
    const display = img.style.display;
    const sync = () => {
      const failed = img.complete && img.naturalWidth === 0;
      img.toggleAttribute('data-error', failed);
      img.style.display = failed ? 'none' : display;
    };
    lifecycle.listen(img, img, 'error', sync);
    lifecycle.listen(img, img, 'load', sync);
    lifecycle.add(img, () => {
      img.style.display = display;
      img.removeAttribute('data-error');
    });
    sync();
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'avatar', enhance, destroy };
