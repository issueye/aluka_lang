export async function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function retryWithBackoff<T>(
  fn: (attempt: number) => Promise<T>,
  maxRetries = 3,
  baseDelay = 10
): Promise<T> {
  let attempt = 0;
  while (true) {
    try {
      return await fn(attempt);
    } catch (err) {
      attempt++;
      if (attempt > maxRetries) {
        throw err;
      }
      const delay = baseDelay * 2 ** (attempt - 1);
      await sleep(delay);
    }
  }
}

export class BatchBuffer<T> {
  private buffer: T[] = [];
  private flushCallback: ((items: T[]) => Promise<void>) | null = null;

  constructor(private readonly maxSize: number) {}

  onFlush(fn: (items: T[]) => Promise<void>) {
    this.flushCallback = fn;
    return this;
  }

  async push(item: T): Promise<void> {
    this.buffer.push(item);
    if (this.buffer.length >= this.maxSize) {
      await this.flush();
    }
  }

  async flush(): Promise<void> {
    if (this.buffer.length === 0) return;
    const items = [...this.buffer];
    this.buffer = [];
    if (this.flushCallback) {
      await this.flushCallback(items);
    }
  }

  get length() {
    return this.buffer.length;
  }
}
