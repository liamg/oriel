// Areas of shapes.
import { readFile } from "node:fs/promises";

export interface Shape {
  area(): number;
  readonly name?: string;
}

enum Kind { Circle = 1, Square }

export class Circle implements Shape {
  #secret = 42;
  constructor(private radius: number) {}
  area(): number {
    return Math.PI * this.radius ** 2;
  }
}

type Pair<T> = [T, T];

export async function total(shapes: Shape[]): Promise<number> {
  let sum = 0;
  for (const s of shapes) {
    if (s.area() < 0) throw new Error(`bad ${s.name ?? "shape"}`);
    sum += s.area();
  }
  const re = /a+b/gi;
  const f = (x: number): boolean => x > 0 && re.test(String(x));
  await readFile("x.txt", "utf8");
  return f(sum) ? sum : 0;
}
