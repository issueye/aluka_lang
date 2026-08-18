# Express 注册中心 + 配置中心 Demo

用 TypeScript + Express 5 写的内存版 **服务注册中心** 与 **配置中心**，可直接用 Aluka 运行（无需 `tsc`）。

## 能力

- **注册中心**：实例注册 / 心跳 / 下线 / 按服务名发现；临时实例超时先标 `DOWN`，再过一个 TTL 剔除
- **配置中心**：`namespace / group / dataId` 三维键；发布后 `version++` 并保留最近 20 条历史；`GET /watch` 长轮询
- **控制台**：打开 `/` 可点选实例心跳、发布配置

## 运行

```bash
cd demo/express-registry-demo
aluka install
aluka run src/index.ts
```

浏览器打开 http://127.0.0.1:3100

另开终端模拟一个 worker（注册 + 心跳 + 监听 `application.json`）：

```bash
aluka run scripts/client.ts
```

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/health` | 平面健康与计数 |
| POST | `/api/v1/instances` | 注册实例 |
| PUT | `/api/v1/instances/:id/heartbeat` | 心跳 |
| DELETE | `/api/v1/instances/:id` | 下线 |
| GET | `/api/v1/services` | 服务列表 |
| GET | `/api/v1/services/:name` | 发现实例（默认只返回健康） |
| GET | `/api/v1/configs` | 配置列表 |
| GET/PUT/DELETE | `/api/v1/configs/:ns/:group/:dataId` | 读 / 发布 / 删除 |
| GET | `/api/v1/configs/:ns/:group/:dataId/history` | 历史版本 |
| GET | `/api/v1/watch?namespace=&group=&dataId=&version=` | 长轮询 |

```bash
curl -s http://127.0.0.1:3100/api/v1/services/order-service
curl -s -X PUT http://127.0.0.1:3100/api/v1/configs/public/DEFAULT/application.json \
  -H "Content-Type: application/json" \
  -d "{\"content\":\"{\\\"logLevel\\\":\\\"debug\\\"}\",\"updatedBy\":\"curl\"}"
```
