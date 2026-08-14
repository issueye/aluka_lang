// Strict TypeScript Module (.ts)
export interface HashResult<T = string> {
  algorithm: string;
  original: T;
  digest: string;
  rounds: number;
}

export class CryptoService<T = string> {
  #salt: string;
  #rounds: number;

  constructor(salt = "aluka_salt", rounds = 3) {
    this.#salt = salt;
    this.#rounds = rounds;
  }

  async hash(data: T): Promise<HashResult<T>> {
    let current = `${this.#salt}:${String(data)}`;
    for (let i = 0; i < this.#rounds; i++) {
      let h = 0x811c9dc5;
      for (let j = 0; j < current.length; j++) {
        h ^= current.charCodeAt(j);
        h = Math.imul(h, 0x01000193);
      }
      current = (h >>> 0).toString(16).padStart(8, "0");
    }

    return {
      algorithm: "fnv1a-multi-round",
      original: data,
      digest: current,
      rounds: this.#rounds,
    };
  }

  validate(data: T, expectedDigest: string): Promise<boolean> {
    return this.hash(data).then((res) => res.digest === expectedDigest);
  }
}
