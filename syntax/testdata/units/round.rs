// Rounding.
pub fn round_half_even(v: Decimal, p: i32) -> Decimal {
    let scaled = v.shift(p);
    let t = scaled.trunc();
    match (scaled - t).abs().cmp(&HALF) {
        Ordering::Greater => t.add(sign(v)).shift(-p),
        Ordering::Equal => {
            if t.int_part() % 2 == 0 {
                return t.shift(-p);
            }
            t.add(sign(v)).shift(-p)
        }
        Ordering::Less => t.shift(-p),
    }
}

pub struct Money {
    currency: String,
    v: Decimal,
}

impl Money {
    pub fn round_minor(&self) -> Result<Money, String> {
        let p = precision(&self.currency).ok_or("unknown currency")?;
        let round = |v: Decimal| round_half_even(v, p);
        for line in self.lines.iter_mut() {
            *line = round(*line);
        }
        Ok(Money { currency: self.currency.clone(), v: round(self.v) })
    }
}
