// Package vol turns "the chart is about to swing" into a number.
//
// The strategy does not need a direction. Buying a cheap out-of-the-money leg
// and hedging once it moves is a LONG VOLATILITY position: it pays when the
// market travels further than the price implied, whichever way it goes. So the
// entry question is not "will BTC go up?" but "will BTC move more than the
// market is charging me for?" -- and that is answerable.
//
// A binary's price IS an implied probability, so it can be inverted for the
// market's implied volatility. Under driftless GBM, P(S_T > K) = N(d2) with
// d2 = [ln(S/K) - sigma^2*T/2] / (sigma*sqrt(T)). Over a 5-15 minute window the
// sigma^2*T/2 term is ~1e-6 and utterly negligible against ln(S/K)/(sigma*sqrt T),
// so we use the short-horizon form
//
//	p = N( ln(S/K) / (sigma*sqrt(T)) )   =>   sigma = ln(S/K) / (N^-1(p) * sqrt(T))
//
// which is both accurate here and far better conditioned.
//
// ONE STRUCTURAL LIMIT, and it shapes the strategy: at the money (S == K) the
// binary is 0.50 for ANY volatility, so implied vol is not recoverable. Price
// carries volatility information only when spot is away from the strike. That is
// no accident and no obstacle -- it is exactly the situation the strategy enters
// in, buying a cheap leg that is far from the money.
//
// Volatility is expressed per sqrt(second) throughout, so it composes directly
// with a time-to-expiry in seconds and never needs an annualization convention.
package vol

import (
	"errors"
	"math"
)

var (
	// ErrAtTheMoney means spot sits on the strike, where the binary price is
	// 0.50 regardless of volatility and carries no information to invert.
	ErrAtTheMoney = errors.New("vol: spot at strike, implied vol not recoverable")
	// ErrDegenerate means the quoted price is at or beyond certainty, where the
	// inversion has no finite solution.
	ErrDegenerate = errors.New("vol: price at 0 or 1, no finite implied vol")
	ErrBadInput   = errors.New("vol: non-positive spot, strike, or horizon")
)

// SecondsPerYear is only for rendering a human-readable annualized figure.
const SecondsPerYear = 31_557_600.0

// NormCDF is the standard normal CDF.
func NormCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

// NormInv is the inverse standard normal CDF (Acklam's rational approximation,
// |relative error| < 1.15e-9 -- far tighter than any quoted price's precision).
func NormInv(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	a := [6]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [5]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01}
	c := [6]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [4]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00}
	const plow = 0.02425
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > 1-plow:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	default:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
}

// MinLogMoneyness is how far spot must sit from the strike before an implied vol
// is trusted. Inside this band the inversion divides by a near-zero numerator and
// the answer is noise. 1e-5 in log terms is ~$0.80 on an $80k BTC.
const MinLogMoneyness = 1e-5

// Implied recovers the market's implied volatility (per sqrt-second) from a
// binary quote. price is cost per $1 of payout for the outcome "spot > strike at
// expiry" -- i.e. the UP leg.
//
// Returns ErrAtTheMoney when spot is too close to the strike to invert, which is
// a normal condition to skip on, not a failure.
func Implied(spot, strike, secondsToExpiry, price float64) (float64, error) {
	if spot <= 0 || strike <= 0 || secondsToExpiry <= 0 {
		return 0, ErrBadInput
	}
	if price <= 0 || price >= 1 {
		return 0, ErrDegenerate
	}
	x := math.Log(spot / strike)
	if math.Abs(x) < MinLogMoneyness {
		return 0, ErrAtTheMoney
	}
	z := NormInv(price)
	if math.Abs(z) < 1e-12 {
		return 0, ErrAtTheMoney
	}
	sigma := x / (z * math.Sqrt(secondsToExpiry))
	if sigma <= 0 || math.IsNaN(sigma) || math.IsInf(sigma, 0) {
		// A negative solution means the quote is inconsistent with the model --
		// e.g. the underdog side priced above 0.5. Treat as no signal.
		return 0, ErrDegenerate
	}
	return sigma, nil
}

// Realized is the volatility (per sqrt-second) of a series of prices sampled
// dtSeconds apart, from close-to-close log returns.
//
// Uses the sample standard deviation about the MEAN rather than about zero. Over
// a few minutes the mean return is indistinguishable from noise, and subtracting
// it keeps a single strong trend from being read as volatility -- which matters,
// because a steady drift is exactly the case where this strategy's hedge never
// appears.
func Realized(prices []float64, dtSeconds float64) (float64, error) {
	if len(prices) < 3 || dtSeconds <= 0 {
		return 0, ErrBadInput
	}
	rets := make([]float64, 0, len(prices)-1)
	for i := 1; i < len(prices); i++ {
		if prices[i] <= 0 || prices[i-1] <= 0 {
			return 0, ErrBadInput
		}
		rets = append(rets, math.Log(prices[i]/prices[i-1]))
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var ss float64
	for _, r := range rets {
		ss += (r - mean) * (r - mean)
	}
	variance := ss / float64(len(rets)-1)
	return math.Sqrt(variance / dtSeconds), nil
}

// Annualized renders a per-sqrt-second sigma as a human-facing annual figure.
func Annualized(sigmaPerSqrtSecond float64) float64 {
	return sigmaPerSqrtSecond * math.Sqrt(SecondsPerYear)
}

// TouchProbability is P(the price touches `barrier` before expiry), for a
// driftless walk, via the reflection principle: P(touch) = 2*P(finish beyond).
//
// This is the number that makes the strategy feel like it works, and it belongs
// in the open where it can be reasoned about. A lock triggers on a TOUCH, not on
// the final outcome, so the hit rate really is about double the terminal
// probability -- genuinely high, and genuinely paid for by the rounds that never
// come back.
func TouchProbability(spot, barrier, secondsToExpiry, sigma float64) float64 {
	if secondsToExpiry <= 0 || sigma <= 0 || spot <= 0 || barrier <= 0 {
		return 0
	}
	d := math.Abs(math.Log(barrier/spot)) / (sigma * math.Sqrt(secondsToExpiry))
	p := 2 * (1 - NormCDF(d))
	return math.Min(1, math.Max(0, p))
}
