import math


def round_half_even(v, p):
    """Rounds v to p decimal places, ties to even."""
    scaled = v * 10**p
    floor = math.trunc(scaled)
    diff = scaled - floor
    if diff > 0.5:
        return (floor + 1) / 10**p
    if diff == 0.5:
        if floor % 2 == 0:
            return floor / 10**p
        return (floor + 1) / 10**p
    return floor / 10**p
