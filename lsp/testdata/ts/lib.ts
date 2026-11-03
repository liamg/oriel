/** Say hello to name. */
export function greeting(name: string): string {
  return `héllo, ${name}`;
}

/** Greet name loudly. */
export function shout(name: string): string {
  return greeting(name) + "!";
}
