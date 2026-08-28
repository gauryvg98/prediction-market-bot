package vol

import (
	"errors"
	"math"
	"testing"
)

func TestNormCDFKnownValues(t *testing.T) {
	if d := math.Abs(NormCDF(0) - 0.5); d > 1e-12 {
		t.Fatalf("N(0) = %v", NormCDF(0))
	}
	if d := math.Abs(NormCDF(1.96) - 0.975); d > 1e-4 {
		t.Fatalf("N(1.96) = %v", NormCDF(1.96))
	}
}

func TestNormInvIsInverseOfNormCDF(t *testing.T) {
	for _, p := range []float64{0.001, 0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 0.999} {
		if d := math.Abs(NormCDF(NormInv(p)) - p); d > 1e-8 {
			t.Fatalf("roundtrip at p=%v drifted by %v", p, d)
		}
	}
}

// The keystone: price a binary from a known sigma, then recover that sigma.
func TestImpliedVolRecoversTheVolItWasPricedWith(t *testing.T) {
	const strike = 80000.0
	seconds := 900.0 // a 15-minute round
	// Parameterize by how many sigmas from the strike, so every case stays in a
	// price range a market could actually quote. Beyond ~5 sigma the binary
	// prices to exactly 0 or 1 in float64 and genuinely carries no information --
	// that is a real limit of the instrument, not of this code.
	for _, sigma := range []float64{2e-5, 5e-5, 1e-4} {
		for _, z := range []float64{-3, -1.5, -0.5, 0.5, 1.5, 3} {
			spot := strike * math.Exp(z*sigma*math.Sqrt(seconds))
			p := NormCDF(math.Log(spot/strike) / (sigma * math.Sqrt(seconds)))
			got, err := Implied(spot, strike, seconds, p)
			if err != nil {
				t.Fatalf("z=%v sigma=%v: %v", z, sigma, err)
			}
			if rel := math.Abs(got-sigma) / sigma; rel > 1e-6 {
				t.Fatalf("z=%v: recovered %v, priced with %v", z, got, sigma)
			}
		}
	}
}

func TestAtTheMoneyCarriesNoVolInformation(t *testing.T) {
	// S == K gives 0.50 for ANY sigma, so the inversion has nothing to invert.
	// This is a structural limit, and a normal reason to skip a round.
	_, err := Implied(80000, 80000, 900, 0.5)
	if !errors.Is(err, ErrAtTheMoney) {
		t.Fatalf("expected ErrAtTheMoney, got %v", err)
	}
}

func TestDegenerateQuotesRejected(t *testing.T) {
	if _, err := Implied(79000, 80000, 900, 0); !errors.Is(err, ErrDegenerate) {
		t.Fatalf("price 0 must be degenerate, got %v", err)
	}
	if _, err := Implied(79000, 80000, 900, 1); !errors.Is(err, ErrDegenerate) {
		t.Fatalf("price 1 must be degenerate, got %v", err)
	}
}

func TestImpliedRejectsBadHorizon(t *testing.T) {
	if _, err := Implied(79000, 80000, 0, 0.3); !errors.Is(err, ErrBadInput) {
		t.Fatalf("zero horizon must be rejected, got %v", err)
	}
}

func TestRealizedVolScalesWithMovement(t *testing.T) {
	calm := []float64{80000, 80001, 80000, 80001, 80000, 80001, 80000}
	wild := []float64{80000, 80100, 79950, 80200, 79900, 80300, 79850}
	c, err := Realized(calm, 1)
	if err != nil {
		t.Fatal(err)
	}
	w, err := Realized(wild, 1)
	if err != nil {
		t.Fatal(err)
	}
	if w <= c*10 {
		t.Fatalf("wild (%v) should dwarf calm (%v)", w, c)
	}
}

func TestSteadyDriftIsNotVolatility(t *testing.T) {
	// A clean one-way trend has near-zero variance about its own mean. This
	// matters: a steady drift is exactly the case where the hedge never appears,
	// so it must not be mistaken for the movement the strategy needs.
	drift := make([]float64, 60)
	for i := range drift {
		drift[i] = 80000 * math.Exp(float64(i)*1e-4)
	}
	v, err := Realized(drift, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v > 1e-9 {
		t.Fatalf("pure drift read as vol %v", v)
	}
}

func TestRealizedNeedsEnoughSamples(t *testing.T) {
	if _, err := Realized([]float64{1, 2}, 1); !errors.Is(err, ErrBadInput) {
		t.Fatal("two points is not a volatility estimate")
	}
}

func TestTouchProbabilityExceedsTerminalProbability(t *testing.T) {
	// The reflection principle: P(touch) ~ 2*P(finish beyond). This is exactly
	// why the strategy wins most rounds -- and why that says nothing about edge.
	const spot, strike, secs, sigma = 80000, 80000, 900, 1e-4
	barrier := 80200.0
	touch := TouchProbability(spot, barrier, secs, sigma)
	terminal := 1 - NormCDF(math.Log(barrier/spot)/(sigma*math.Sqrt(secs)))
	if math.Abs(touch-2*terminal) > 1e-9 {
		t.Fatalf("touch %v should be 2x terminal %v", touch, terminal)
	}
	if touch <= terminal {
		t.Fatal("touch must exceed terminal")
	}
}

func TestTouchProbabilityIsClamped(t *testing.T) {
	if p := TouchProbability(80000, 80000.001, 900, 1e-3); p > 1 {
		t.Fatalf("probability escaped [0,1]: %v", p)
	}
}

func TestFarFromTheMoneyPricesToCertaintyAndIsRejected(t *testing.T) {
	// A leg many sigmas away quotes at 1.0 and stops carrying vol information.
	// Rejecting it is correct: there is nothing left to invert.
	sigma, strike, seconds := 2e-5, 80000.0, 900.0
	spot := strike * math.Exp(10*sigma*math.Sqrt(seconds))
	p := NormCDF(math.Log(spot/strike) / (sigma * math.Sqrt(seconds)))
	if p != 1 {
		t.Skipf("float64 kept %v below 1; nothing to assert", p)
	}
	if _, err := Implied(spot, strike, seconds, p); !errors.Is(err, ErrDegenerate) {
		t.Fatalf("expected ErrDegenerate at certainty, got %v", err)
	}
}
