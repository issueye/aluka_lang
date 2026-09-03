import type { HubStore } from "./store.ts";
import type { RegisterRequest } from "./types.ts";

function sendError(res: { status: (code: number) => { json: (body: unknown) => unknown } }, status: number, message: string) {
  res.status(status).json({ ok: false, error: message });
}

/** 直接挂在 app 上，避免 Express Router 前缀裁剪在 Aluka 上踩 req.url */
export function mountRegistry(app, store: HubStore): void {
  app.post("/api/v1/instances", (req, res) => {
    try {
      const body = (req.body || {}) as RegisterRequest;
      const instance = store.register(body);
      res.status(201).json({ ok: true, instance });
    } catch (err) {
      sendError(res, 400, err instanceof Error ? err.message : String(err));
    }
  });

  app.put("/api/v1/instances/:id/heartbeat", (req, res) => {
    try {
      const instance = store.heartbeat(String(req.params.id));
      res.json({ ok: true, instance });
    } catch (err) {
      sendError(res, 404, err instanceof Error ? err.message : String(err));
    }
  });

  app.delete("/api/v1/instances/:id", (req, res) => {
    const removed = store.deregister(String(req.params.id));
    if (!removed) {
      sendError(res, 404, "instance not found");
      return;
    }
    res.json({ ok: true, instanceId: req.params.id });
  });

  app.get("/api/v1/instances", (_req, res) => {
    res.json({ ok: true, instances: store.listInstances() });
  });

  app.get("/api/v1/instances/:id", (req, res) => {
    const instance = store.getInstance(String(req.params.id));
    if (!instance) {
      sendError(res, 404, "instance not found");
      return;
    }
    res.json({ ok: true, instance });
  });

  app.get("/api/v1/services", (_req, res) => {
    res.json({ ok: true, services: store.listServices() });
  });

  app.get("/api/v1/services/:name", (req, res) => {
    const healthyOnly = String(req.query.healthy || "1") !== "0";
    const instances = store.discover(String(req.params.name), healthyOnly);
    res.json({
      ok: true,
      serviceName: req.params.name,
      healthyOnly,
      instances,
    });
  });
}
