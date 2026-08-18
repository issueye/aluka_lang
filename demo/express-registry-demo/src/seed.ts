import type { HubStore } from "./store.ts";

/** 预置几条服务与配置，打开控制台即可看到数据 */
export function seed(store: HubStore): void {
  store.register({
    serviceName: "order-service",
    host: "10.0.1.11",
    port: 8081,
    instanceId: "order-service-a",
    metadata: { zone: "cn-east-1a", env: "dev" },
    weight: 10,
    ephemeral: false,
  });
  store.register({
    serviceName: "order-service",
    host: "10.0.1.12",
    port: 8081,
    instanceId: "order-service-b",
    metadata: { zone: "cn-east-1b", env: "dev" },
    weight: 8,
    ephemeral: false,
  });
  store.register({
    serviceName: "user-service",
    host: "10.0.2.21",
    port: 8082,
    instanceId: "user-service-a",
    metadata: { zone: "cn-east-1a", env: "dev" },
    ephemeral: false,
  });
  const down = store.register({
    serviceName: "payment-service",
    host: "10.0.3.31",
    port: 8083,
    instanceId: "payment-service-stale",
    metadata: { zone: "cn-east-1c", env: "dev" },
    ephemeral: false,
  });
  down.healthy = false;

  store.publish("public", "DEFAULT", "application.json", {
    content: JSON.stringify(
      {
        appName: "aluka-hub",
        logLevel: "info",
        feature: { newCheckout: false },
      },
      null,
      2,
    ),
    contentType: "json",
    updatedBy: "seed",
  });
  store.publish("order-service", "DEFAULT", "datasource.json", {
    content: JSON.stringify(
      {
        driver: "postgres",
        host: "db.internal",
        port: 5432,
        database: "orders",
        pool: { min: 2, max: 16 },
      },
      null,
      2,
    ),
    contentType: "json",
    updatedBy: "seed",
  });
  store.publish("user-service", "feature", "flags.json", {
    content: JSON.stringify(
      {
        enableAvatar: true,
        maxLoginAttempts: 5,
        experiment: "holdout",
      },
      null,
      2,
    ),
    contentType: "json",
    updatedBy: "seed",
  });
}
