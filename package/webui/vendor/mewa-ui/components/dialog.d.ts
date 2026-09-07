import type { MewaBehavior } from "../runtime/core.js";

export declare const behaviors: readonly MewaBehavior[];
export declare function enhance(root?: ParentNode, options?: unknown): unknown[];
export declare function destroy(root?: ParentNode, states?: unknown[]): void;
export declare const behavior: MewaBehavior;
