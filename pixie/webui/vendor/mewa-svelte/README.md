# Svelte integration

The optional `mewa-svelte` release supports Svelte 5.29 through 5.x. The core package remains framework-neutral. The attachment has no runtime imports and supports direct vendor paths.

Extract the core and adapter archives into the application's `vendor/mewa-ui` and `vendor/mewa-svelte` directories. Install Svelte in the application. This example uses relative vendor imports:

```svelte
<script>
  import { mewa } from './vendor/mewa-svelte/index.js';
  import { behavior } from './vendor/mewa-ui/components/toggle.js';
  import './vendor/mewa-ui/css/base.css';
  import './vendor/mewa-ui/css/tokens.css';
  import './vendor/mewa-ui/css/toggle.css';

  let visible = $state(true);
</script>

<button type="button" onclick={() => visible = !visible}>Show or hide tool</button>
{#if visible}
  <button class="toggle" type="button" aria-pressed="false" {@attach mewa(behavior)}>
    Pin
  </button>
{/if}
```

The attachment enhances after mounting and calls cleanup when Svelte removes the element or reruns the attachment. Keep the behavior and attachment options stable unless reinitialization is intended.

Choose one owner for each state and DOM region. For this uncontrolled Toggle, the controller owns `aria-pressed`. Do not also bind it to Svelte state. For native form controls, use their documented input/change events and Svelte bindings. For components generating internal DOM, leave that region to Mewa; replace the component through a keyed block when its structural markup changes.

Do not import `auto.js` into a Svelte-owned region. Keep popup targets within the owned region when possible. Document-level shortcuts remain shared for the document lifetime.

## Bun build

```js
import { sveltePlugin } from './vendor/mewa-svelte/bun-plugin.js';

const result = await Bun.build({
  entrypoints: ['./index.html'],
  outdir: './public',
  target: 'browser',
  plugins: [sveltePlugin({ dev: false })]
});
if (!result.success) throw new AggregateError(result.logs, 'Build failed');
```

The plugin compiles client `.svelte` components and `.svelte.js`/`.svelte.ts` rune modules. TypeScript rune modules are stripped before compilation. Compiler warnings are printed with source locations; `onwarn(warning)` can route or reject them in CI. Run a separate TypeScript check: compilation strips types without validating them.

Set `dev: true` for Svelte development diagnostics. Bun's output source maps refer to the compiled JavaScript; the tested Bun 1.4 plugin path does not chain the compiler's map back to original Svelte source. Server rendering, hydration builds, HMR, and original-source map chaining are not provided by this plugin.

Component-local CSS is injected into the document at runtime. Applications with a Content Security Policy that blocks injected styles need a build integration that extracts or authorizes those styles. The separately imported Mewa CSS remains a static stylesheet.
