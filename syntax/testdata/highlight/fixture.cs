using System;
using System.Collections.Generic;

namespace Pricing
{
    // A tier's price, in pence.
    public record Tier(string Name, int Pence);

    public static class Prices
    {
        public static int For(Tier tier, int qty = 1)
        {
            var total = tier.Pence * qty;
            return qty >= 100 ? (int)(total * 0.9) : total;
        }

        public static void Main()
        {
            var tiers = new List<Tier> { new("basic", 100), new("pro", 450) };
            foreach (var t in tiers)
            {
                Console.WriteLine($"{t.Name}: {For(t, 120)}");
            }
        }
    }
}
