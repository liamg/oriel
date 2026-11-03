import pytest

from calc import add, div


def test_pass():
    assert add(1, 2) == 3


def test_fail():
    print("about to add")
    assert add(2, 2) == 5


def test_raises():
    div(1, 0)


@pytest.mark.skip(reason="not today")
def test_skip():
    pass


def test_skip_inside():
    pytest.skip("skipped inside")


@pytest.mark.xfail(reason="known bug")
def test_xfail():
    assert add(1, 1) == 3


@pytest.fixture
def broken():
    raise RuntimeError("fixture broke")


def test_setup_error(broken):
    pass


class TestNested:
    def test_inner(self):
        assert add(0, 0) == 0

    class TestDeeper:
        def test_deep(self):
            assert add(1, 1) == 2

    def helper(self):
        pass


class TestWithInit:
    def __init__(self):
        pass

    def test_never_collected(self):
        pass


@pytest.mark.parametrize("a,b,want", [(1, 1, 2), (2, 2, 5), (-1, 1, 0)])
def test_add_table(a, b, want):
    assert add(a, b) == want


@pytest.mark.parametrize(
    "word",
    [
        "hi",
        "two words",
        pytest.param("named", id="custom-id"),
        3.5,
        True,
        None,
        [1, 2],
    ],
)
def test_words(word):
    assert word != "two words"


@pytest.mark.parametrize("x", [0, 1], ids=["zero", "one"])
@pytest.mark.parametrize("y", [2, 3])
def test_stacked(x, y):
    pass
