package pricing

import kotlin.math.roundToInt

// A tier's price, in pence.
data class Tier(val name: String, val pence: Int)

fun priceFor(tier: Tier, qty: Int = 1): Int {
    val total = tier.pence * qty
    return when {
        qty >= 100 -> (total * 0.9).roundToInt()
        else -> total
    }
}

fun main() {
    val tiers = listOf(Tier("basic", 100), Tier("pro", 450))
    for (t in tiers) {
        println("${t.name}: ${priceFor(t, 120)}")
    }
}
