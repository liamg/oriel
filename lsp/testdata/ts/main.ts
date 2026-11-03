import { greeting, shout } from "./lib";

/** Greet everyone. */
export function welcome(names: string[]): string {
  let out = "";
  for (const n of names) {
    out += greeting(n);
  }

  let   size=out.length;
  const label = "🎉 party" + greting("x");
  return out + shout("é") + label + size;
}
