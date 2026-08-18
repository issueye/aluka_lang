import { createHash } from "node:crypto";
import type {
  ConfigItem,
  ConfigRevision,
  PublishConfigRequest,
  RegisterRequest,
  ServiceInstance,
  WatchQuery,
} from "./types.ts";

const DEFAULT_TTL_MS = 15_000;
const MAX_HISTORY = 20;

function now(): number {
  return Date.now();
}

function newId(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

function md5(text: string): string {
  return createHash("md5").update(text).digest("hex");
}

function configKey(namespace: string, group: string, dataId: string): string {
  return `${namespace}\0${group}\0${dataId}`;
}

interface Watcher {
  query: WatchQuery;
  resolve: (item: ConfigItem | null) => void;
  timer: unknown;
}

/** 进程内存储：注册表 + 配置 + 长轮询订阅 */
export class HubStore {
  private instances = new Map<string, ServiceInstance>();
  private configs = new Map<string, ConfigItem>();
  private history = new Map<string, ConfigRevision[]>();
  private watchers: Watcher[] = [];

  register(req: RegisterRequest): ServiceInstance {
    const serviceName = String(req.serviceName || "").trim();
    const host = String(req.host || "").trim();
    const port = Number(req.port);
    if (!serviceName) {
      throw new Error("serviceName is required");
    }
    if (!host) {
      throw new Error("host is required");
    }
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      throw new Error("port must be an integer in 1..65535");
    }

    const instanceId = String(req.instanceId || newId(serviceName)).trim();
    const existing = this.instances.get(instanceId);
    const ts = now();
    const instance: ServiceInstance = {
      instanceId,
      serviceName,
      host,
      port,
      metadata: req.metadata && typeof req.metadata === "object" ? { ...req.metadata } : {},
      weight: typeof req.weight === "number" && req.weight > 0 ? req.weight : 1,
      ephemeral: req.ephemeral !== false,
      ttlMs: typeof req.ttlMs === "number" && req.ttlMs >= 1000 ? req.ttlMs : DEFAULT_TTL_MS,
      healthy: true,
      registeredAt: existing ? existing.registeredAt : ts,
      lastHeartbeat: ts,
    };
    this.instances.set(instanceId, instance);
    return instance;
  }

  heartbeat(instanceId: string): ServiceInstance {
    const inst = this.instances.get(instanceId);
    if (!inst) {
      throw new Error("instance not found");
    }
    inst.lastHeartbeat = now();
    inst.healthy = true;
    return inst;
  }

  deregister(instanceId: string): boolean {
    return this.instances.delete(instanceId);
  }

  getInstance(instanceId: string): ServiceInstance | undefined {
    return this.instances.get(instanceId);
  }

  listServices(): Array<{ serviceName: string; total: number; healthy: number }> {
    const agg = new Map<string, { total: number; healthy: number }>();
    for (const inst of this.instances.values()) {
      const row = agg.get(inst.serviceName) || { total: 0, healthy: 0 };
      row.total += 1;
      if (inst.healthy) {
        row.healthy += 1;
      }
      agg.set(inst.serviceName, row);
    }
    return [...agg.entries()]
      .map(([serviceName, row]) => ({ serviceName, ...row }))
      .sort((a, b) => a.serviceName.localeCompare(b.serviceName));
  }

  discover(serviceName: string, healthyOnly: boolean): ServiceInstance[] {
    const list: ServiceInstance[] = [];
    for (const inst of this.instances.values()) {
      if (inst.serviceName !== serviceName) {
        continue;
      }
      if (healthyOnly && !inst.healthy) {
        continue;
      }
      list.push(inst);
    }
    return list.sort((a, b) => b.weight - a.weight || a.instanceId.localeCompare(b.instanceId));
  }

  listInstances(): ServiceInstance[] {
    return [...this.instances.values()].sort((a, b) => {
      const byService = a.serviceName.localeCompare(b.serviceName);
      if (byService !== 0) {
        return byService;
      }
      return a.instanceId.localeCompare(b.instanceId);
    });
  }

  /** 扫描过期临时实例：先标 DOWN，再过一个 TTL 后剔除 */
  sweepExpired(): { downed: number; evicted: number } {
    const ts = now();
    let downed = 0;
    let evicted = 0;
    for (const [id, inst] of [...this.instances.entries()]) {
      if (!inst.ephemeral) {
        continue;
      }
      const age = ts - inst.lastHeartbeat;
      if (age > inst.ttlMs * 2) {
        this.instances.delete(id);
        evicted += 1;
        continue;
      }
      if (age > inst.ttlMs && inst.healthy) {
        inst.healthy = false;
        downed += 1;
      }
    }
    return { downed, evicted };
  }

  getConfig(namespace: string, group: string, dataId: string): ConfigItem | undefined {
    return this.configs.get(configKey(namespace, group, dataId));
  }

  listConfigs(namespace?: string, group?: string): ConfigItem[] {
    const out: ConfigItem[] = [];
    for (const item of this.configs.values()) {
      if (namespace && item.namespace !== namespace) {
        continue;
      }
      if (group && item.group !== group) {
        continue;
      }
      out.push(item);
    }
    return out.sort((a, b) => a.namespace.localeCompare(b.namespace) || a.dataId.localeCompare(b.dataId));
  }

  publish(namespace: string, group: string, dataId: string, req: PublishConfigRequest): ConfigItem {
    const ns = String(namespace || "").trim();
    const gp = String(group || "").trim();
    const id = String(dataId || "").trim();
    if (!ns || !gp || !id) {
      throw new Error("namespace, group, dataId are required");
    }
    if (typeof req.content !== "string") {
      throw new Error("content must be a string");
    }

    const key = configKey(ns, gp, id);
    const prev = this.configs.get(key);
    const ts = now();
    const item: ConfigItem = {
      namespace: ns,
      group: gp,
      dataId: id,
      content: req.content,
      contentType: req.contentType || prev?.contentType || "json",
      version: (prev?.version || 0) + 1,
      md5: md5(req.content),
      updatedAt: ts,
      updatedBy: String(req.updatedBy || "api").trim() || "api",
    };

    if (prev) {
      const hist = this.history.get(key) || [];
      hist.unshift({
        version: prev.version,
        content: prev.content,
        contentType: prev.contentType,
        md5: prev.md5,
        updatedAt: prev.updatedAt,
        updatedBy: prev.updatedBy,
      });
      this.history.set(key, hist.slice(0, MAX_HISTORY));
    }

    this.configs.set(key, item);
    this.notifyWatchers(item);
    return item;
  }

  deleteConfig(namespace: string, group: string, dataId: string): boolean {
    const key = configKey(namespace, group, dataId);
    const existed = this.configs.delete(key);
    this.history.delete(key);
    if (existed) {
      this.notifyDeleted(namespace, group, dataId);
    }
    return existed;
  }

  historyOf(namespace: string, group: string, dataId: string): ConfigRevision[] {
    return [...(this.history.get(configKey(namespace, group, dataId)) || [])];
  }

  /** 长轮询：已有更新立即返回，否则等到超时 */
  watch(query: WatchQuery, timeoutMs: number): Promise<ConfigItem | null> {
    const current = this.getConfig(query.namespace, query.group, query.dataId);
    if (current && current.version > query.version) {
      return Promise.resolve(current);
    }
    return new Promise((resolve) => {
      const watcher: Watcher = {
        query,
        resolve,
        timer: setTimeout(() => {
          this.dropWatcher(watcher);
          resolve(null);
        }, timeoutMs),
      };
      this.watchers.push(watcher);
    });
  }

  private notifyWatchers(item: ConfigItem): void {
    const pending = this.watchers.slice();
    for (const w of pending) {
      if (
        w.query.namespace === item.namespace &&
        w.query.group === item.group &&
        w.query.dataId === item.dataId &&
        item.version > w.query.version
      ) {
        clearTimeout(w.timer);
        this.dropWatcher(w);
        w.resolve(item);
      }
    }
  }

  private notifyDeleted(namespace: string, group: string, dataId: string): void {
    const pending = this.watchers.slice();
    for (const w of pending) {
      if (w.query.namespace === namespace && w.query.group === group && w.query.dataId === dataId) {
        clearTimeout(w.timer);
        this.dropWatcher(w);
        w.resolve(null);
      }
    }
  }

  private dropWatcher(target: Watcher): void {
    this.watchers = this.watchers.filter((w) => w !== target);
  }
}
