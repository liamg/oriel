import pytest

from pricing import round_half_even


@pytest.mark.parametrize("v,p,want", [
    pytest.param(2.5, 0, 2, id="2.5"),
    pytest.param(3.5, 0, 4, id="3.5"),
    pytest.param(-2.5, 0, -2, id="-2.5"),
    pytest.param(-3.5, 0, -4, id="-3.5"),
])
def test_round_half_even(v, p, want):
    assert round_half_even(v, p) == want
