package rig

// ExplainBase is a round.go as a branch's base has it, for explaining a change.
const ExplainBase = `package pricing

// roundHalfEven rounds v to p places, ties to even.
func roundHalfEven(v Decimal, p int32) Decimal {
	scaled := v.Shift(p)
	t := scaled.Truncate(0)
	switch scaled.Sub(t).Abs().Cmp(half) {
	case 1:
		return t.Add(sign(v)).Shift(-p)
	case 0:
		return t.Floor().Shift(-p)
	}
	return t.Shift(-p)
}

func RoundMinor(amt Money) (Money, error) {
	p, ok := currency.Precision(amt.Currency)
	if !ok {
		return Money{}, errUnknown
	}
	return Money{amt.Currency, roundHalfEven(amt.V, p)}, nil
}

func untouched() int { return 1 }
`

// ExplainWork is ExplainBase as the branch has it: lines 7, 8 and 12-15 in roundHalfEven, and 23 in RoundMinor.
const ExplainWork = `package pricing

// roundHalfEven rounds v to p places, ties to even.
func roundHalfEven(v Decimal, p int32) Decimal {
	scaled := v.Shift(p)
	t := scaled.Truncate(0)
	diff := scaled.Sub(t).Abs()
	switch diff.Cmp(half) {
	case 1:
		return t.Add(sign(v)).Shift(-p)
	case 0:
		if t.IntPart()%2 == 0 {
			return t.Shift(-p)
		}
		return t.Add(sign(v)).Shift(-p)
	}
	return t.Shift(-p)
}

func RoundMinor(amt Money) (Money, error) {
	p, ok := currency.Precision(amt.Currency)
	if !ok {
		return Money{}, fmt.Errorf("unknown currency %q", amt.Currency)
	}
	return Money{amt.Currency, roundHalfEven(amt.V, p)}, nil
}

func untouched() int { return 1 }
`

// InvoiceBase is an invoice.go as a branch's base has it.
const InvoiceBase = `package billing

// NewLine prices one metered line for an invoice.
func NewLine(m Meter, t Tier) (Line, error) {
	raw := t.Price(m.Qty)
	return Line{Meter: m.ID, Amount: raw}, nil
}
`

// InvoiceWork is InvoiceBase as the branch has it: lines 6-10 new.
const InvoiceWork = `package billing

// NewLine prices one metered line for an invoice.
func NewLine(m Meter, t Tier) (Line, error) {
	raw := t.Price(m.Qty)
	amt, err := pricing.RoundMinor(raw)
	if err != nil {
		return Line{}, fmt.Errorf("line %s: %w", m.ID, err)
	}
	return Line{Meter: m.ID, Amount: amt, Rounding: pricing.HalfEven}, nil
}
`
