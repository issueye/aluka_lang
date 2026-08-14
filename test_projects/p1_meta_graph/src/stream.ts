export async function* rangeStream(start: number, end: number, step = 1) {
  for (let i = start; i <= end; i += step) {
    // yield with simulated microtask delay
    await Promise.resolve();
    yield i;
  }
}

export async function* mapStream<T, R>(
  source: AsyncIterable<T>,
  transform: (item: T, index: number) => Promise<R> | R
) {
  let index = 0;
  for await (const item of source) {
    yield await transform(item, index++);
  }
}

export async function* filterStream<T>(
  source: AsyncIterable<T>,
  predicate: (item: T) => Promise<boolean> | boolean
) {
  for await (const item of source) {
    if (await predicate(item)) {
      yield item;
    }
  }
}

export class AsyncPipeline<T> {
  constructor(private iterable: AsyncIterable<T>) {}

  map<R>(fn: (item: T, idx: number) => Promise<R> | R): AsyncPipeline<R> {
    return new AsyncPipeline<R>(mapStream(this.iterable, fn));
  }

  filter(fn: (item: T) => Promise<boolean> | boolean): AsyncPipeline<T> {
    return new AsyncPipeline<T>(filterStream(this.iterable, fn));
  }

  async toArray(): Promise<T[]> {
    const results: T[] = [];
    for await (const item of this.iterable) {
      results.push(item);
    }
    return results;
  }

  async reduce<R>(fn: (acc: R, item: T) => R, initial: R): Promise<R> {
    let acc = initial;
    for await (const item of this.iterable) {
      acc = fn(acc, item);
    }
    return acc;
  }

  [Symbol.asyncIterator]() {
    return this.iterable[Symbol.asyncIterator]();
  }
}
