export function add(a: number, b: number): number { return a + b; }
export interface Shape { area(): number; }
export class Point {
  x: number;
  y: number = 5;
  constructor(x: number) { this.x = x; }
  sum(): number { return this.x + this.y; }
}
export const label: string = 'ts-ok';
