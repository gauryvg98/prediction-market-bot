package market

import (
	"math"
	"testing"
)

func approx(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %v, want %v (tol %v)", what, got, want, tol)
	}
}

func TestOddsToPrice(t *testing.T) {
	for _, c := range []struct{ odds, price float64 }{
		{1, 0.5}, {2, 1.0 / 3}, {3, 0.25}, {5, 1.0 / 6},
	} {
		p, err := PriceFromOdds(c.odds)
		if err != nil {
			t.Fatal(err)
		}
		approx(t, p, c.price, 1e-12, "price")
	}
}

func TestOddsRoundTrip(t *testing.T) {
	for _, p := range []float64{0.1, 0.25, 0.5, 0.8} {
		o, err := OddsFromPrice(p)
		if err != nil {
			t.Fatal(err)
		}
		back, _ := PriceFromOdds(o)
		approx(t, back, p, 1e-12, "roundtrip")
	}
}

func TestNonPositiveOddsRefused(t *testing.T) {
	if _, err := PriceFromOdds(0); err == nil {
		t.Fatal("expected error on zero odds")
	}
}

func TestBreakevenIsOneWithoutFees(t *testing.T) {
	approx(t, Fees{}.BreakevenSum(), 1, 1e-15, "breakeven")
}

func TestFeesTightenBreakeven(t *testing.T) {
	if (Fees{StakeRate: 0.025}).BreakevenSum() >= 1 {
		t.Fatal("stake fee must tighten the threshold")
	}
	if (Fees{PayoutRate: 0.02}).BreakevenSum() >= 1 {
		t.Fatal("payout fee must tighten the threshold")
	}
}

func book(up, down float64) *Book {
	return &Book{RoundID: "r", ExpiryMs: 900_000,
		Up: []Level{{up, 1000}}, Down: []Level{{down, 1000}}}
}

func TestSumIsTheInvariant(t *testing.T) {
	s, ok := book(0.25, 0.80).Sum()
	if !ok {
		t.Fatal("expected a sum")
	}
	approx(t, s, 1.05, 1e-12, "S")
	o, _ := book(0.25, 0.80).Overround()
	approx(t, o, 0.05, 1e-12, "overround")
}

func TestHealthyMarketIsNotAFreeLock(t *testing.T) {
	// A live two-sided quote sums ABOVE 1 -- that gap is the maker's revenue,
	// and it is why simultaneous both-sides buying can never be free.
	if book(0.25, 0.80).IsRiskFreeLock(Fees{}) {
		t.Fatal("S>1 must not read as a risk-free lock")
	}
}

func TestInvertedQuoteIsAFreeLock(t *testing.T) {
	if !book(0.25, 0.60).IsRiskFreeLock(Fees{}) {
		t.Fatal("S<1 is genuine arbitrage")
	}
}

func TestFeesEraseAMarginalLock(t *testing.T) {
	b := book(0.49, 0.50) // S = 0.99, a 1c edge
	if !b.IsRiskFreeLock(Fees{}) {
		t.Fatal("should lock without fees")
	}
	if b.IsRiskFreeLock(Fees{StakeRate: 0.025}) {
		t.Fatal("a 1c edge must not survive a 2.5% spread")
	}
}

func TestUnquotedSideHasNoSum(t *testing.T) {
	b := &Book{Up: []Level{{0.5, 1}}}
	if _, ok := b.Sum(); ok {
		t.Fatal("one-sided book must not produce a sum")
	}
}

func TestOtherIsInvolution(t *testing.T) {
	if Up.Other() != Down || Up.Other().Other() != Up {
		t.Fatal("Other must flip and be self-inverse")
	}
}
