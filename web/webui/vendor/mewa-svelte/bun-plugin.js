import { compile, compileModule } from 'svelte/compiler';

/** Compile Svelte components and rune modules through Bun. */
export function sveltePlugin({
  dev = false,
  onwarn = (warning) =>
    console.warn(
      `${warning.filename || ''}:${warning.start?.line || 0}:${warning.start?.column || 0} [${warning.code}] ${warning.message}`
    )
} = {}) {
  return {
    name: 'mewa-svelte',
    setup(build) {
      build.onLoad({ filter: /\.svelte(?:\.[jt]s)?$/ }, async ({ path }) => {
        let source = await Bun.file(path).text();
        const module = /\.svelte\.[jt]s$/.test(path);
        if (path.endsWith('.svelte.ts'))
          source = new Bun.Transpiler({ loader: 'ts' }).transformSync(source);
        const options = { dev, filename: path, generate: 'client' };
        const compiled = module
          ? compileModule(source, options)
          : compile(source, { ...options, css: 'injected' });
        for (const warning of compiled.warnings) onwarn(warning);
        return { contents: compiled.js.code, loader: 'js' };
      });
    }
  };
}
