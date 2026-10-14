// Rounding.
export function roundHalfEven(v: Decimal, p: number): Decimal {
  const scaled = v.shift(p);
  const t = scaled.truncate(0);
  switch (scaled.sub(t).abs().cmp(HALF)) {
    case 1:
      return t.add(sign(v)).shift(-p);
    case 0:
      if (t.intPart() % 2 === 0) {
        return t.shift(-p);
      }
      return t.add(sign(v)).shift(-p);
  }
  return t.shift(-p);
}

export class Money {
  roundMinor(): Money {
    const p = PRECISION.get(this.currency);
    if (p === undefined) {
      throw new Error(`unknown currency ${this.currency}`);
    }
    const round = (v: Decimal) => roundHalfEven(v, p);
    for (let i = 0; i < this.lines.length; i++) {
      this.lines[i] = round(this.lines[i]);
    }
    return new Money(this.currency, round(this.v));
  }
}
