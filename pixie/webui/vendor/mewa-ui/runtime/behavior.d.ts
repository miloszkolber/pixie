export interface MewaBehavior<State = unknown, Options = unknown> {
  readonly name: string;
  enhance(root: ParentNode, options?: Options): State | void;
  destroy?(root: ParentNode, state?: State): void;
}
