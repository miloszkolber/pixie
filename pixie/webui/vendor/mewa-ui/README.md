# mewa-ui 0.1.2

This is the generated, framework-neutral mewa_ui core package from the GitHub release.

## Plain HTML

Load `css/base.css`, `css/tokens.css`, and one dependency-aware component entry such as `css/dialog.css`.

Load `auto/dialog.js` as a module when plain HTML should initialize Dialog automatically.

## Application lifecycle

Import `behavior` from `components/dialog.js` and pass it to `createController` from `index.js`. Component entries include the behavior dependencies declared by the manifest. Use `controllers/dialog.js` only when an integration manages those dependencies itself.

Controllers have no automatic DOM side effects. Automatic entries share one document observer.

Read `integration.md` for a complete vanilla example, lifecycle ownership, and browser capabilities. Document-level adapters remain shared for the document lifetime.

## Optional assets

Fonts remain opt-in under `fonts/`. SVG icons ship in the separate `mewa-icons` release archive. Upstream Geist and Lucide notices are preserved under `licenses/`.

Read `manifest.json` for component files and dependencies. Use `checksums.json` to verify every packaged file.

Clean Git builds include immutable component contract links in the manifest. A modified checkout or a source archive without Git metadata leaves those links empty; use the matching source checkout for its contracts. Do not substitute documentation from a different revision.

The mewa_ui code is MIT licensed. Bundled Geist fonts remain under the SIL Open Font License 1.1, and bundled Lucide-derived glyphs retain their upstream ISC and MIT notices. See `licenses/` and `LICENSE`.
