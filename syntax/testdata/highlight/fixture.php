<?php

namespace Pricing;

// A tier's price, in pence.
final class Tier
{
    public function __construct(public string $name, public int $pence) {}
}

function priceFor(Tier $tier, int $qty = 1): int
{
    $total = $tier->pence * $qty;
    return $qty >= 100 ? (int) ($total * 0.9) : $total;
}

foreach ([new Tier('basic', 100), new Tier('pro', 450)] as $t) {
    echo "{$t->name}: " . priceFor($t, 120) . "\n";
}
