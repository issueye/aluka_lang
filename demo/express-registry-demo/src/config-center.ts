import type { HubStore } from "./store.ts";
import type { PublishConfigRequest } from "./types.ts";

function sendError(res: { status: (code: number) => { json: (body: unknown) => unknown } }, status: number, message: string) {
  res.status(status).json({ ok: false, error: message });
}

const DEFAULT_WATCH_MS = 25_000;
const MAX_WATCH_MS = 60_000;

/** 直接挂在 app 上，避免 Express Router 前缀裁剪在 Aluka 上踩 req.url */
export function mountConfig(app, store: HubStore): void {
  app.get("/api/v1/configs/:namespace/:group/:dataId/history", (req, res) => {
    const item = store.getConfig(req.params.namespace, req.params.group, req.params.dataId);
    if (!item) {
      sendError(res, 404, "config not found");
      return;
    }
    res.json({ ok: true, current: item, history: store.historyOf(item.namespace, item.group, item.dataId) });
  });

  app.get("/api/v1/configs/:namespace/:group/:dataId", (req, res) => {
    const item = store.getConfig(req.params.namespace, req.params.group, req.params.dataId);
    if (!item) {
      sendError(res, 404, "config not found");
      return;
    }
    res.json({ ok: true, config: item });
  });

  app.put("/api/v1/configs/:namespace/:group/:dataId", (req, res) => {
    try {
      const body = (req.body || {}) as PublishConfigRequest;
      const item = store.publish(req.params.namespace, req.params.group, req.params.dataId, body);
      res.json({ ok: true, config: item });
    } catch (err) {
      sendError(res, 400, err instanceof Error ? err.message : String(err));
    }
  });

  app.delete("/api/v1/configs/:namespace/:group/:dataId", (req, res) => {
    const removed = store.deleteConfig(req.params.namespace, req.params.group, req.params.dataId);
    if (!removed) {
      sendError(res, 404, "config not found");
      return;
    }
    res.json({ ok: true, deleted: true });
  });

  app.get("/api/v1/configs", (req, res) => {
    const namespace = typeof req.query.namespace === "string" ? req.query.namespace : undefined;
    const group = typeof req.query.group === "string" ? req.query.group : undefined;
    res.json({ ok: true, configs: store.listConfigs(namespace, group) });
  });

  app.get("/api/v1/watch", async (req, res) => {
    const namespace = String(req.query.namespace || "");
    const group = String(req.query.group || "DEFAULT");
    const dataId = String(req.query.dataId || "");
    const version = Number(req.query.version || 0);
    const timeoutMs = Math.min(
      Math.max(Number(req.query.timeoutMs || DEFAULT_WATCH_MS), 1000),
      MAX_WATCH_MS,
    );
    if (!namespace || !dataId) {
      sendError(res, 400, "namespace and dataId are required");
      return;
    }
    const item = await store.watch({ namespace, group, dataId, version }, timeoutMs);
    if (!item) {
      res.json({ ok: true, changed: false });
      return;
    }
    res.json({ ok: true, changed: true, config: item });
  });
}
