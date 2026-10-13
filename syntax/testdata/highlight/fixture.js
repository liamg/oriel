// prices
import { round } from "./round.js";

export class Tier {
  #rate = 1.5;

  constructor(name) {
    this.name = name;
  }

  price(base) {
    return round(base * this.#rate, 2);
  }
}

const gold = new Tier("gold");
console.log(`${gold.name}: ${gold.price(10)}`);
