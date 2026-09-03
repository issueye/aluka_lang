import express from "express";
import fs from "node:fs";
import path from "node:path";
import { mountConfig } from "./config-center.ts";
import { mountRegistry } from "./registry.ts";
import { seed } from "./seed.ts";
import { HubStore } from "./store.ts";

const PORT = Number(process.env.PORT || 3100);
const store = new HubStore();
seed(store);

const app = express();
app.disable("x-powered-by");
app.use(express.json({ limit: "1mb" }));

app.use((req, res, next) => {
  const started = Date.now();
  res.on("finish", () => {
    const ms = Date.now() - started;
    console.log(`${req.method} ${req.originalUrl} ${res.statusCode} ${ms}ms`);
  });
  next();
});

app.get("/api/v1/health", (_req, res) => {
  const services = store.listServices();
  const instances = store.listInstances();
  res.json({
    ok: true,
    role: "registry+config",
    uptimeSec: Math.round(process.uptime()),
    services: services.length,
    instances: instances.length,
    healthy: instances.filter((i) => i.healthy).length,
    configs: store.listConfigs().length,
  });
});

mountRegistry(app, store);
mountConfig(app, store);

const dashboard = fs.readFileSync(path.join(import.meta.dir, "public", "index.html"), "utf8");
app.get("/", (_req, res) => {
  res.setHeader("Content-Type", "text/html; charset=utf-8");
  res.end(dashboard);
});

function errorHandler(err, _req, res, _next) {
  const message = err instanceof Error ? err.message : String(err);
  console.error("unhandled:", message);
  res.status(500).json({ ok: false, error: message });
}
app.use(errorHandler);

const server = app.listen(PORT, () => {
  console.log(`Aluka registry+config listening on http://127.0.0.1:${PORT}`);
  console.log("  dashboard  GET  /");
  console.log("  health     GET  /api/v1/health");
  console.log("  register   POST /api/v1/instances");
  console.log("  discover   GET  /api/v1/services/:name");
  console.log("  publish    PUT  /api/v1/configs/:ns/:group/:dataId");
  console.log("  watch      GET  /api/v1/watch?namespace=&group=&dataId=&version=");
});

const sweeper = setInterval(() => {
  store.sweepExpired();
}, 2000);

function shutdown(signal: string): void {
  console.log(`received ${signal}, closing...`);
  clearInterval(sweeper);
  server.close(() => {
    console.log("server closed gracefully");
    process.exit(0);
  });
  setTimeout(() => process.exit(1), 5000).unref();
}

process.on("SIGINT", () => shutdown("SIGINT"));
process.on("SIGTERM", () => shutdown("SIGTERM"));
