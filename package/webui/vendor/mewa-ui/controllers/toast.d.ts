import type { MewaBehavior } from "../runtime/core.js";

export declare function enhance(root?: ParentNode): import("../runtime/events.js").ToastApi | undefined;
export declare function destroy(root?: ParentNode): void;
export declare const behavior: MewaBehavior;
