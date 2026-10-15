class Point:
    def __init__(self, x, y):
        self.x = x
        self.y = y


def half(v):
    scaled = v * 10
    p = Point(v, scaled)
    out = p.y // 2
    return out


def main():
    total = 0
    for i in range(1, 5):
        total += half(i)
    print("total", total)


main()
