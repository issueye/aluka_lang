export type EventListener<T = any> = (data: T) => void | Promise<void>;

export class AsyncEventEmitter {
  private listeners = new Map<string, Set<EventListener>>();
  private onceListeners = new WeakSet<EventListener>();

  on<T = any>(event: string, listener: EventListener<T>, options?: { signal?: AbortSignal }) {
    if (options?.signal?.aborted) {
      return this;
    }

    if (!this.listeners.has(event)) {
      this.listeners.set(event, new Set());
    }
    this.listeners.get(event)!.add(listener);

    if (options?.signal) {
      options.signal.addEventListener("abort", () => {
        this.off(event, listener);
      }, { once: true });
    }

    return this;
  }

  once<T = any>(event: string, listener: EventListener<T>) {
    const wrapped: EventListener<T> = async (data) => {
      this.off(event, wrapped);
      await listener(data);
    };
    this.onceListeners.add(wrapped);
    return this.on(event, wrapped);
  }

  off(event: string, listener: EventListener) {
    const set = this.listeners.get(event);
    if (set) {
      set.delete(listener);
      if (set.size === 0) {
        this.listeners.delete(event);
      }
    }
    return this;
  }

  async emit<T = any>(event: string, data: T): Promise<void> {
    const set = this.listeners.get(event);
    if (!set) return;

    const list = Array.from(set);
    const promises = list.map(async (fn) => {
      try {
        await fn(data);
      } catch (err) {
        console.error(`[AsyncEventEmitter] Error in listener for ${event}:`, err);
      }
    });

    await Promise.all(promises);
  }

  listenerCount(event: string): number {
    return this.listeners.get(event)?.size ?? 0;
  }
}
