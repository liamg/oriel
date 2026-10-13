const std = @import("std");

/// A tier's price, in pence.
const Tier = struct {
    name: []const u8,
    pence: u32,
};

fn priceFor(tier: Tier, qty: u32) u32 {
    const total = tier.pence * qty;
    if (qty >= 100) return total * 9 / 10;
    return total;
}

pub fn main() !void {
    const tiers = [_]Tier{ .{ .name = "basic", .pence = 100 }, .{ .name = "pro", .pence = 450 } };
    for (tiers) |t| {
        std.debug.print("{s}: {d}\n", .{ t.name, priceFor(t, 120) });
    }
}
