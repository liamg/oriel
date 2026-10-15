"""Rounding."""


def round_half_even(v, p):
    scaled = v.shift(p)
    t = scaled.truncate(0)
    match (scaled - t).compare(HALF):
        case 1:
            return t.add(sign(v)).shift(-p)
        case 0:
            if t.int_part() % 2 == 0:
                return t.shift(-p)
            return t.add(sign(v)).shift(-p)
    return t.shift(-p)


class Money:
    def round_minor(self):
        p = PRECISION.get(self.currency)
        if p is None:
            raise ValueError(f"unknown currency {self.currency}")
        rnd = lambda v: round_half_even(v, p)
        for i, line in enumerate(self.lines):
            self.lines[i] = rnd(line)
        return Money(self.currency, rnd(self.v))
