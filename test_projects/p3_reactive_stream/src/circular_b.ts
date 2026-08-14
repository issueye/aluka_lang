import { ServiceA, getAName } from "./circular_a.ts";

export class ServiceB {
  constructor(public readonly name = "ServiceB") {}

  describe(): string {
    return `${this.name} -> ${getAName()}`;
  }

  createSibling(): ServiceA {
    return new ServiceA("CreatedByB");
  }
}

export function getBName(): string {
  return "NameFromB";
}
