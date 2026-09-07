import type { MewaBehavior } from './behavior.js';
export type { MewaBehavior } from './behavior.js';

export interface MewaController<Options = unknown> {
  readonly element: ParentNode;
  update(options?: Options): void;
  destroy(): void;
}

export declare function queryAll(root: ParentNode | null | undefined, selector: string): Element[];
export declare function createController<State, Options>(behavior: MewaBehavior<State, Options>, root: ParentNode, options?: Options): MewaController<Options>;
export interface BehaviorLease<State, Options> {
  readonly state: State | void;
  update(options?: Options): void;
  destroy(): void;
}
export declare function acquireBehavior<State, Options>(behavior: MewaBehavior<State, Options>, root: ParentNode, options?: Options): BehaviorLease<State, Options>;
export interface Lifecycle {
  has(owner: Node): boolean;
  add(owner: Node, cleanup: () => void): () => void;
  listen(owner: Node, target: EventTarget | null | undefined, type: string, listener: EventListener, options?: boolean | AddEventListenerOptions): void;
  reset(owner: Node, form: HTMLFormElement | null | undefined, callback: () => void): void;
  destroy(root: Node): void;
  onUpdate(owner: Node, callback: () => void): void;
  refresh(root?: Node | null, ancestors?: boolean): void;
}
export declare function createLifecycle(name: string): Lifecycle;
