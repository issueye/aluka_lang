/**
 * 模拟一个微服务客户端：
 *   1. 向注册中心登记自己
 *   2. 定时心跳
 *   3. 拉取配置，并用长轮询监听变更
 *
 * 用法（另开终端）：
 *   aluka run scripts/client.ts
 *   REGISTRY=http://127.0.0.1:3100 SERVICE=demo-worker aluka run scripts/client.ts
 */
const BASE = String(process.env.REGISTRY || "http://127.0.0.1:3100");
const SERVICE = String(process.env.SERVICE || "demo-worker");
const HOST = String(process.env.HOST || "127.0.0.1");
const PORT = Number(process.env.CLIENT_PORT || 9001);

interface JsonOk {
  ok?: boolean;
  error?: string;
  [k: string]: unknown;
}

async function api(method: string, path: string, body?: unknown): Promise<JsonOk> {
  const res = await fetch(BASE + path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = (await res.json()) as JsonOk;
  if (!res.ok || data.ok === false) {
    throw new Error(String(data.error || res.status));
  }
  return data;
}

async function main(): Promise<void> {
  console.log(`client → ${BASE} as ${SERVICE}`);

  const registered = await api("POST", "/api/v1/instances", {
    serviceName: SERVICE,
    host: HOST,
    port: PORT,
    metadata: { role: "worker", lang: "ts" },
    ttlMs: 12_000,
  });
  const instance = registered.instance as { instanceId: string };
  const instanceId = instance.instanceId;
  console.log("registered", instanceId);

  const discovered = await api("GET", `/api/v1/services/${encodeURIComponent(SERVICE)}`);
  const peers = (discovered.instances as unknown[]) || [];
  console.log(`discover ${SERVICE}: ${peers.length} healthy instance(s)`);

  const cfgPath = "/api/v1/configs/public/DEFAULT/application.json";
  let version = 0;
  try {
    const got = await api("GET", cfgPath);
    const config = got.config as { version: number; content: string };
    version = config.version;
    console.log("config v" + version + ":\n" + config.content);
  } catch (err) {
    console.log("no application.json yet:", err instanceof Error ? err.message : err);
  }

  const beat = setInterval(() => {
    api("PUT", `/api/v1/instances/${encodeURIComponent(instanceId)}/heartbeat`)
      .then(() => console.log("heartbeat", new Date().toISOString()))
      .catch((err) => console.error("heartbeat failed", err instanceof Error ? err.message : err));
  }, 4000);

  const watchLoop = async () => {
    while (true) {
      const result = await api(
        "GET",
        `/api/v1/watch?namespace=public&group=DEFAULT&dataId=application.json&version=${version}&timeoutMs=20000`,
      );
      if (result.changed) {
        const config = result.config as { version: number; content: string };
        version = config.version;
        console.log("config changed → v" + version + "\n" + config.content);
      } else {
        console.log("watch timeout, continue");
      }
    }
  };

  const onStop = async () => {
    clearInterval(beat);
    try {
      await api("DELETE", `/api/v1/instances/${encodeURIComponent(instanceId)}`);
      console.log("deregistered", instanceId);
    } catch {
      // ignore
    }
    process.exit(0);
  };
  process.on("SIGINT", onStop);
  process.on("SIGTERM", onStop);

  await watchLoop();
}

main().catch((err) => {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
});
