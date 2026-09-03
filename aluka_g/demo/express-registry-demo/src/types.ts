/** 服务实例（注册中心） */
export interface ServiceInstance {
  instanceId: string;
  serviceName: string;
  host: string;
  port: number;
  metadata: Record<string, string>;
  weight: number;
  /** 是否临时实例：TTL 内无心跳则剔除 */
  ephemeral: boolean;
  ttlMs: number;
  healthy: boolean;
  registeredAt: number;
  lastHeartbeat: number;
}

export interface RegisterRequest {
  serviceName: string;
  host: string;
  port: number;
  instanceId?: string;
  metadata?: Record<string, string>;
  weight?: number;
  ephemeral?: boolean;
  ttlMs?: number;
}

export type ConfigContentType = "json" | "text" | "properties" | "yaml";

/** 配置条目（配置中心） */
export interface ConfigItem {
  namespace: string;
  group: string;
  dataId: string;
  content: string;
  contentType: ConfigContentType;
  version: number;
  md5: string;
  updatedAt: number;
  updatedBy: string;
}

export interface ConfigRevision {
  version: number;
  content: string;
  contentType: ConfigContentType;
  md5: string;
  updatedAt: number;
  updatedBy: string;
}

export interface PublishConfigRequest {
  content: string;
  contentType?: ConfigContentType;
  updatedBy?: string;
}

export interface WatchQuery {
  namespace: string;
  group: string;
  dataId: string;
  version: number;
}
