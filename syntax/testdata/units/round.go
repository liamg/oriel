package pricing

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

type Money struct {
	Currency string
	V        Decimal
}

func (m Money) RoundMinor() (Money, error) {
	p, ok := currency.Precision(m.Currency)
	if !ok {
		return Money{}, fmt.Errorf("unknown currency %q", m.Currency)
	}
	round := func(v Decimal) Decimal { return roundHalfEven(v, p) }
	for i := range m.Lines {
		m.Lines[i] = round(m.Lines[i])
	}
	return Money{m.Currency, round(m.V)}, nil
}
