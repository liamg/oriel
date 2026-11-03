"""Areas of shapes."""
from __future__ import annotations

import math
from dataclasses import dataclass


@dataclass
class Circle:
    radius: float = 1.0

    def area(self) -> float:
        return math.pi * self.radius**2


def total(shapes: list[Circle], *, scale: int = 1) -> float:
    """Sum the areas."""
    result = 0.0
    for i, s in enumerate(shapes):
        if s.area() < 0:
            raise ValueError(f"shape {i} is negative: {s!r}")
        result += s.area() * scale
    match result:
        case 0:
            return 0.0
        case _:
            pass
    squares = [x * x for x in range(3) if x]
    fn = lambda x: x + 1  # noqa
    return result if result else None


async def main() -> None:
    async with open("x") as f:
        await f.read()
