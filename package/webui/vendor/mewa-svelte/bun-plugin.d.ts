import type { BunPlugin } from 'bun';
export type { BunPlugin } from 'bun';

export interface SveltePluginOptions {
  dev?: boolean;
  onwarn?: (warning: import("svelte/compiler").Warning) => void;
}

export declare function sveltePlugin(options?: SveltePluginOptions): BunPlugin;
