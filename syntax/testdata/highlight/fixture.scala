package pricing

// A tier's price, in pence.
final case class Tier(name: String, pence: Int)

object Prices {
  def priceFor(tier: Tier, qty: Int = 1): Either[String, Int] =
    if (qty <= 0) Left(s"bad quantity $qty")
    else if (qty >= 100) Right((tier.pence * qty * 0.9).toInt)
    else Right(tier.pence * qty)

  def main(args: Array[String]): Unit =
    List(Tier("basic", 100), Tier("pro", 450)).foreach(t => println(priceFor(t, 120)))
}
