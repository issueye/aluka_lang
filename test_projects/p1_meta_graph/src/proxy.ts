import { BaseNode } from "./node.ts";

export interface GraphDSL {
  [key: string]: any;
}

export function createGraphProxy(nodes: Map<string, BaseNode>): GraphDSL {
  const dynamicHandlers = new Map<string, Function>();

  const target = {
    _nodes: nodes,
    count() {
      return nodes.size;
    },
    list() {
      return Array.from(nodes.keys());
    },
  };

  return new Proxy(target, {
    get(t: any, prop: string | symbol, receiver: any) {
      if (typeof prop === "symbol") {
        return Reflect.get(t, prop, receiver);
      }

      if (prop in t) {
        return Reflect.get(t, prop, receiver);
      }

      if (prop.startsWith("get_")) {
        const nodeName = prop.slice(4);
        return () => nodes.get(nodeName);
      }

      if (prop.startsWith("exec_")) {
        const nodeName = prop.slice(5);
        return async (arg: any) => {
          const node = nodes.get(nodeName);
          if (!node) {
            throw new Error(`Node not found: ${nodeName}`);
          }
          return await node.execute(arg);
        };
      }

      if (nodes.has(prop)) {
        return nodes.get(prop);
      }

      return undefined;
    },

    set(t: any, prop: string | symbol, value: any, receiver: any) {
      if (typeof prop === "string" && value instanceof BaseNode) {
        nodes.set(prop, value);
        return true;
      }
      return Reflect.set(t, prop, value, receiver);
    },

    has(t: any, prop: string | symbol) {
      if (typeof prop === "string" && nodes.has(prop)) {
        return true;
      }
      return Reflect.has(t, prop);
    },

    ownKeys(t: any) {
      const baseKeys = Reflect.ownKeys(t);
      const nodeKeys = Array.from(nodes.keys());
      return Array.from(new Set([...baseKeys, ...nodeKeys]));
    },

    getOwnPropertyDescriptor(t: any, prop: string | symbol) {
      if (typeof prop === "string" && nodes.has(prop)) {
        return {
          enumerable: true,
          configurable: true,
          value: nodes.get(prop),
        };
      }
      return Reflect.getOwnPropertyDescriptor(t, prop);
    },
  });
}
