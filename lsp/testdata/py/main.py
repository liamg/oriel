from lib import greeting, shout


def welcome(names: list[str]) -> str:
    """Greet everyone."""
    out = ""
    for n in names:
        out += greeting(n)

    size=len( out )
    label = "🎉 party" + greting("x")
    return out + shout("é") + label + str(size)
