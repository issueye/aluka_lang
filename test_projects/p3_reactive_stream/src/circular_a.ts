import { ServiceB, getBName } from "./circular_b.ts";

export class ServiceA {
  constructor(public readonly name = "ServiceA") {}

  describe(): string {
    return `${this.name} -> ${getBName()}`;
  }

  createSibling(): ServiceB {
    return new ServiceB("CreatedByA");
  }
}

export function getAName(): string {
  return "NameFromA";
}
