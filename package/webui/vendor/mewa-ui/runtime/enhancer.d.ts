import type { MewaBehavior } from "./core.js";

export interface MewaEnhancer {
  register(behavior: MewaBehavior): () => void;
  enhance(root?: ParentNode): void;
  destroy(root: ParentNode): void;
  observe(root?: Node): () => void;
  disconnect(): void;
  readonly behaviors: MewaBehavior[];
}

export declare function createEnhancer(initialBehaviors?: MewaBehavior[]): MewaEnhancer;
export declare function registerBehavior(behavior: MewaBehavior): () => void;
export declare function enhance(root?: ParentNode): void;
export declare function observe(root?: Node): () => void;
export declare function disconnect(): void;
