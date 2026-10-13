package pricing;

import java.util.List;

/** Rounds prices. */
public final class Round {
    private static final int SCALE = 2;

    @Override
    public String toString() {
        return "Round(" + SCALE + ")";
    }

    public static long total(List<Long> prices) {
        long sum = 0;
        for (long p : prices) {
            sum += p;
        }
        return sum;
    }
}
