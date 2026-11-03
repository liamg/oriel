def greeting(name: str) -> str:
    """Say hello to name."""
    return f"héllo, {name}"


def shout(name: str) -> str:
    """Greet name loudly."""
    return greeting(name) + "!"
