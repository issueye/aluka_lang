import { GraphMeta, InternalId, Serializable, type MetadataHolder } from "./symbols.ts";

export abstract class BaseNode implements MetadataHolder {
  private static counter = 1000;
  static nodeTypeRegistry = new Map<string, typeof BaseNode>();

  readonly id: string;
  #state: Record<string, any> = {};
  #version = 1n;

  [GraphMeta]: {
    created: number;
    tags: string[];
    version: bigint;
  };
  [InternalId]: string;

  constructor(public readonly name: string, tags: string[] = []) {
    BaseNode.counter++;
    this.id = `node_${BaseNode.counter}`;
    this[InternalId] = `uuid_${Math.random().toString(36).slice(2, 9)}`;
    this[GraphMeta] = {
      created: Date.now(),
      tags: [...tags, "base"],
      version: this.#version,
    };
  }

  get state() {
    return { ...this.#state };
  }

  set state(update: Record<string, any>) {
    this.#state = { ...this.#state, ...update };
    this.#version += 1n;
    this[GraphMeta].version = this.#version;
  }

  updateState(key: string, value: any) {
    this.#state[key] = value;
    this.#state.lastUpdated ??= Date.now();
    this.#state.hitCount ||= 0;
    this.#state.hitCount += 1;
    return this.#state;
  }

  [Serializable]() {
    return JSON.stringify({
      id: this.id,
      name: this.name,
      state: this.#state,
      version: this.#version.toString(),
      tags: this[GraphMeta].tags,
    });
  }

  abstract execute(input: any): Promise<any>;
}

export class ComputeNode extends BaseNode {
  #ops: Array<(x: number) => number> = [];

  constructor(name: string, private readonly multiplier: number = 1) {
    super(name, ["compute", "math"]);
  }

  addOperation(fn: (x: number) => number) {
    this.#ops.push(fn);
    return this;
  }

  override async execute(input: number): Promise<{ result: number; history: number[] }> {
    let current = input * this.multiplier;
    const history: number[] = [current];

    for (const op of this.#ops) {
      current = op(current);
      history.push(current);
    }

    this.updateState("lastResult", current);
    return { result: current, history };
  }
}

export class TransformNode<T = any, R = any> extends BaseNode {
  constructor(
    name: string,
    private readonly transformFn: (data: T, ctx?: { id: string }) => R | Promise<R>
  ) {
    super(name, ["transform"]);
  }

  override async execute(input: T): Promise<R> {
    const res = await this.transformFn(input, { id: this.id });
    this.updateState("processedItems", (this.state.processedItems ?? 0) + 1);
    return res;
  }
}
