import type { Attachment } from "svelte/attachments";

import type { MewaBehavior } from './behavior.js';
export type { MewaBehavior } from './behavior.js';

export declare function mewa<State = unknown, Options = unknown>(
  behavior: MewaBehavior<State, Options>,
  options?: Options
): Attachment<HTMLElement>;

export { mewa as attachBehavior };
