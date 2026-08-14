export const GraphMeta = Symbol("graph.meta");
export const InternalId = Symbol("internal.id");
export const Serializable = Symbol("serializable");

export interface MetadataHolder {
  [GraphMeta]?: {
    created: number;
    tags: string[];
    version: bigint;
  };
  [InternalId]?: string;
  [Serializable]?(): string;
}
