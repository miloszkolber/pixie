// -- Carousel -------------------------------------------------

import { queryAll, createLifecycle } from '../runtime/core.js';


const lifecycle = createLifecycle('carousel');

export function enhance(root) {
  queryAll(root, '.carousel').forEach((carousel) => {
    carousel.dataset.init = '';
    if (lifecycle.has(carousel)) return;
    carousel.dataset.mewaCarouselInit = '';

    const viewport = carousel.querySelector('.carousel-viewport');
    const prevBtn = carousel.querySelector('.carousel-prev');
    const nextBtn = carousel.querySelector('.carousel-next');
    const dotsContainer = carousel.querySelector('.carousel-dots');
    const counter = carousel.querySelector('.carousel-counter');
    if (!viewport) return;

    const slides = () => Array.from(viewport.querySelectorAll('.carousel-slide'));
    const isLoop = carousel.hasAttribute('data-loop');

    let currentIndex = 0;

    // ── ARIA setup ───────────────────────────────
    if (!carousel.hasAttribute('role')) carousel.setAttribute('role', 'region');
    carousel.setAttribute('aria-roledescription', 'carousel');
    if (!carousel.hasAttribute('aria-label')) carousel.setAttribute('aria-label', 'Carousel');

    slides().forEach((slide, i) => {
      slide.setAttribute('role', 'group');
      slide.setAttribute('aria-roledescription', 'slide');
      if (!slide.hasAttribute('aria-label')) {
        slide.setAttribute('aria-label', `${i + 1} of ${slides().length}`);
      }
    });

    // ── Scroll to index ─────────────────────────
    const scrollToIndex = (index) => {
      const allSlides = slides();
      if (!allSlides.length) return;

      let target = index;
      if (isLoop) {
        target = ((index % allSlides.length) + allSlides.length) % allSlides.length;
      } else {
        target = Math.max(0, Math.min(index, allSlides.length - 1));
      }

      const slide = allSlides[target];
      viewport.scrollTo({ left: slide.offsetLeft - viewport.offsetLeft, behavior: 'auto' });
    };

    // ── Update state (buttons, dots, counter) ───
    const updateState = (index) => {
      const allSlides = slides();
      if (!allSlides.length) return;
      currentIndex = index;

      if (!isLoop) {
        if (prevBtn) prevBtn.disabled = currentIndex <= 0;
        if (nextBtn) nextBtn.disabled = currentIndex >= allSlides.length - 1;
      }

      if (dotsContainer) {
        const dots = dotsContainer.querySelectorAll('.carousel-dot');
        dots.forEach((dot, i) => {
          dot.setAttribute('aria-current', i === currentIndex ? 'true' : 'false');
        });
      }

      if (counter) {
        counter.textContent = `Slide ${currentIndex + 1} of ${allSlides.length}`;
      }

      allSlides.forEach((slide, i) => {
        slide.setAttribute('aria-label', `${i + 1} of ${allSlides.length}`);
      });
    };

    // ── IntersectionObserver for current slide ──
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && entry.intersectionRatio >= 0.5) {
            const idx = slides().indexOf(entry.target);
            if (idx !== -1) updateState(idx);
          }
        }
      },
      { root: viewport, threshold: 0.5 }
    );

    slides().forEach((slide) => observer.observe(slide));
    lifecycle.add(carousel, () => observer.disconnect());

    // ── Navigation ──────────────────────────────
    const goNext = () => scrollToIndex(currentIndex + 1);
    const goPrev = () => scrollToIndex(currentIndex - 1);

    if (prevBtn) lifecycle.listen(carousel, prevBtn, 'click', goPrev);
    if (nextBtn) lifecycle.listen(carousel, nextBtn, 'click', goNext);

    // ── Dot click handlers ──────────────────────
    if (dotsContainer) {
      const allSlides = slides();
      if (!dotsContainer.children.length && allSlides.length) {
        allSlides.forEach((_, i) => {
          const dot = carousel.ownerDocument.createElement('button');
          dot.type = 'button';
          dot.className = 'carousel-dot';
          dot.setAttribute('aria-label', `Go to slide ${i + 1}`);
          dot.setAttribute('aria-current', i === 0 ? 'true' : 'false');
          dotsContainer.appendChild(dot);
        });
      }

      lifecycle.listen(carousel, dotsContainer, 'click', (e) => {
        const dot = e.target.closest('.carousel-dot');
        if (!dot) return;
        const dots = Array.from(dotsContainer.querySelectorAll('.carousel-dot'));
        const idx = dots.indexOf(dot);
        if (idx !== -1) scrollToIndex(idx);
      });
    }

    // ── Keyboard navigation ─────────────────────
    lifecycle.listen(carousel, carousel, 'keydown', (e) => {
      if (e.defaultPrevented) return;
      if (e.target !== carousel) {
        for (let target = e.target; target && target !== carousel; target = target.parentElement) {
          if (
            target.matches(
              'button, a[href], input, select, textarea, summary, [contenteditable], [role="button"], [role="link"], [role="textbox"], [role="searchbox"], [role="combobox"], [role="listbox"], [role="slider"], [role="spinbutton"], [role="switch"], [role="checkbox"], [role="radio"], [role="tab"], [role="menuitem"], [role="menuitemcheckbox"], [role="menuitemradio"], [role="option"], [role="treeitem"]'
            )
          ) {
            if (
              !target.hasAttribute('contenteditable') ||
              target.getAttribute('contenteditable') !== 'false'
            )
              return;
          }
          if (target.hasAttribute('tabindex')) return;
        }
      }
      const prevKey = 'ArrowLeft';
      const nextKey = 'ArrowRight';
      if (e.key === prevKey) {
        e.preventDefault();
        goPrev();
      }
      if (e.key === nextKey) {
        e.preventDefault();
        goNext();
      }
      if (e.key === 'Home') {
        e.preventDefault();
        scrollToIndex(0);
      }
      if (e.key === 'End') {
        e.preventDefault();
        scrollToIndex(slides().length - 1);
      }
    });

    if (!carousel.hasAttribute('tabindex')) {
      carousel.setAttribute('tabindex', '0');
    }

    viewport.setAttribute('aria-live', 'polite');

    // ── Initial state ───────────────────────────
    updateState(0);
  });
}

export function destroy(root) {
  lifecycle.destroy(root);
}

export const behavior = { name: 'carousel', enhance, destroy };
